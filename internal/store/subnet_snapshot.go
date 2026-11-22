package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

type SubnetSnapshotAssignment struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Revision int64  `json:"revision"`
	MaxItems int    `json:"max_items"`
	TTL      int    `json:"ttl_millis"`
}
type SubnetSnapshotItem struct {
	Item     protocol.Definition `json:"item"`
	Quantity protocol.Quantity   `json:"quantity"`
}
type SubnetSnapshotResult struct {
	ID       string               `json:"id"`
	Kind     string               `json:"kind"`
	Revision int64                `json:"revision"`
	Items    []SubnetSnapshotItem `json:"items"`
	Error    string               `json:"error,omitempty"`
}

func validSubnetSnapshot(r SubnetSnapshotResult) bool {
	if !monitorID.MatchString(r.ID) || r.Kind != "item" && r.Kind != "fluid" || r.Revision < 1 || len(r.Items) > 128 {
		return false
	}
	if r.Error != "" {
		return len(r.Items) == 0 && (r.Error == "read_failed" || r.Error == "memory_low" || r.Error == "too_many_items" || r.Error == "payload_large" || r.Error == "unsupported")
	}
	if r.Items == nil {
		return false
	}
	seen := map[string]bool{}
	for _, i := range r.Items {
		if i.Item.Kind != r.Kind || len(i.Item.NBT) > 5464 || r.Kind == "fluid" && (i.Item.NBT != "" || i.Item.Damage != 0) || i.Item.Validate() != nil || !i.Item.Exact || i.Quantity <= 0 || seen[i.Item.Key()] {
			return false
		}
		seen[i.Item.Key()] = true
	}
	return true
}
func nativeSubnetSnapshotAssignment(ctx context.Context, tx pgx.Tx, w *SubnetWelcome) error {
	var capable bool
	if err := tx.QueryRow(ctx, `SELECT n.discovery_mode='native' AND b.subnet_snapshot_capable FROM monitored_subnets n JOIN monitor_sessions b ON b.id=$2 AND b.collector_id=n.collector_id WHERE n.id=$1`, w.SubnetID, w.Session).Scan(&capable); err != nil {
		return err
	}
	if !capable {
		return nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO subnet_native_snapshots(subnet_id,kind) VALUES($1,'item'),($1,'fluid') ON CONFLICT DO NOTHING`, w.SubnetID); err != nil {
		return err
	}
	var kind string
	err := tx.QueryRow(ctx, `SELECT kind FROM subnet_native_snapshots WHERE subnet_id=$1 AND (revision<>$3 OR assignment_session<>$2 OR next_read_at<=now()) AND (assignment_until IS NULL OR assignment_until<=now() OR revision<>$3 OR assignment_session<>$2) ORDER BY next_read_at,kind DESC LIMIT 1 FOR UPDATE`, w.SubnetID, w.Session, w.Revision).Scan(&kind)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	// Responses can be lost; a lease prevents immediate repeated enumeration.
	a := &SubnetSnapshotAssignment{ID: newID(), Kind: kind, Revision: w.Revision, MaxItems: 128, TTL: 60000}
	if _, err = tx.Exec(ctx, `UPDATE subnet_native_snapshots SET assignment_id=$3,assignment_session=$4,assignment_until=now()+interval '60 seconds',revision=$5,next_read_at=now()+interval '30 seconds' WHERE subnet_id=$1 AND kind=$2`, w.SubnetID, kind, a.ID, w.Session, w.Revision); err != nil {
		return err
	}
	w.Snapshot = a
	return nil
}
func (s *Store) subnetNativeStatus(ctx context.Context, id string, v *SubnetDiscoveryStatus) error {
	var mode string
	var capable bool
	if err := s.pool.QueryRow(ctx, `SELECT n.discovery_mode,coalesce(b.subnet_snapshot_capable,false) FROM monitored_subnets n JOIN monitor_collectors c ON c.id=n.collector_id LEFT JOIN monitor_sessions b ON b.id=c.current_session WHERE n.id=$1`, id).Scan(&mode, &capable); err != nil {
		return err
	}
	v.Mode = mode
	if mode != "native" {
		return nil
	}
	v.Capable = capable
	v.Total = 0
	v.Checked = 0
	v.Failed = 0
	return s.pool.QueryRow(ctx, `SELECT max(x.observed_at),coalesce(string_agg(x.kind||':'||x.error,', ' ORDER BY x.kind) FILTER(WHERE x.error<>''),'') FROM subnet_native_snapshots x JOIN monitored_subnets n ON n.id=x.subnet_id JOIN monitor_collectors c ON c.id=n.collector_id WHERE x.subnet_id=$1 AND x.revision=n.revision AND x.assignment_session=c.current_session`, id).Scan(&v.Progress, &v.Error)
}
func (s *Store) acceptNativeSubnetSnapshot(ctx context.Context, tx pgx.Tx, w *SubnetWelcome, r SubnetSnapshotResult, now time.Time) error {
	if r.Revision != w.Revision {
		return nil
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subnet_native_snapshots x JOIN monitored_subnets n ON n.id=x.subnet_id WHERE x.subnet_id=$1 AND x.kind=$2 AND x.assignment_id=$3 AND x.assignment_session=$4 AND x.revision=$5 AND x.assignment_until>now() AND n.discovery_mode='native')`, w.SubnetID, r.Kind, r.ID, w.Session, r.Revision).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return nil
	}
	backoff := 30
	if r.Error == "too_many_items" || r.Error == "payload_large" {
		backoff = 3600
	}
	if _, err := tx.Exec(ctx, `UPDATE subnet_native_snapshots SET assignment_id='',assignment_until=NULL,next_read_at=$3::timestamptz+$4::int*interval '1 second',error=$5,observed_at=CASE WHEN $5='' THEN $3::timestamptz ELSE observed_at END,item_count=CASE WHEN $5='' THEN $6 ELSE item_count END WHERE subnet_id=$1 AND kind=$2`, w.SubnetID, r.Kind, now, backoff, r.Error, len(r.Items)); err != nil {
		return err
	}
	if r.Error != "" {
		_, err := tx.Exec(ctx, `UPDATE subnet_discovered x SET error=$3 FROM subnet_catalog d WHERE x.item_id=d.id AND x.subnet_id=$1 AND d.kind=$2`, w.SubnetID, r.Kind, r.Error)
		return err
	}
	// Replace only after the complete bounded snapshot is accepted. Partial or
	// failed uploads never erase the last data or synthesize zero quantities.
	if _, err := tx.Exec(ctx, `DELETE FROM subnet_discovered x USING subnet_catalog d WHERE x.item_id=d.id AND x.subnet_id=$1 AND d.kind=$2`, w.SubnetID, r.Kind); err != nil {
		return err
	}
	for _, i := range r.Items {
		h := sha256.Sum256([]byte(i.Item.Key()))
		id := hex.EncodeToString(h[:16])
		label := i.Item.Label
		if s.catalog != nil {
			label = s.catalog.DefinitionLabel(i.Item)
		}
		if label == "" {
			label = i.Item.Name
		}
		// NESQL metadata remains the authority when this exact identity is known.
		if _, err := tx.Exec(ctx, `INSERT INTO subnet_catalog(id,kind,name,damage,nbt,label,search_label) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`, id, i.Item.Kind, i.Item.Name, i.Item.Damage, i.Item.NBT, label, searchLabel(label)); err != nil {
			return err
		}
		quantity := i.Quantity
		sample := SubnetResult{ItemID: id, Revision: r.Revision, Quantity: &quantity}
		if err := recordSubnetDiscovered(ctx, tx, w, sample, now); err != nil {
			return err
		}
	}
	return nil
}
