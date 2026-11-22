package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

type SubnetDiscoveryStatus struct {
	Capable   bool       `json:"capable"`
	Checked   int64      `json:"checked"`
	Failed    int64      `json:"failed"`
	Total     int64      `json:"total"`
	Round     int64      `json:"round"`
	Found     int64      `json:"found"`
	Started   *time.Time `json:"started_at,omitempty"`
	Progress  *time.Time `json:"progress_at,omitempty"`
	Completed *time.Time `json:"completed_at,omitempty"`
	Mode      string     `json:"mode"`
	Error     string     `json:"error,omitempty"`
}
type SubnetDiscoveredItem struct {
	SubnetCatalogItem
	Quantity  protocol.Quantity `json:"quantity"`
	Observed  time.Time         `json:"observed_at"`
	Monitored bool              `json:"monitored"`
}
type SubnetDiscoveryPage struct {
	Items  []SubnetDiscoveredItem `json:"items"`
	Total  int64                  `json:"total"`
	Status SubnetDiscoveryStatus  `json:"status"`
}

func (s *Store) subnetDiscoveryStatus(ctx context.Context, id string) (SubnetDiscoveryStatus, error) {
	var v SubnetDiscoveryStatus
	err := s.pool.QueryRow(ctx, `SELECT coalesce(b.subnet_discovery_capable,false),coalesce(x.checked,0),coalesce(x.failed,0),coalesce(x.total,(SELECT count(*) FROM subnet_catalog)),coalesce(x.round,1),x.started_at,x.progress_at,x.completed_at,
 (SELECT count(*) FROM subnet_discovered d WHERE d.subnet_id=n.id AND d.revision=n.revision AND d.session_id=c.current_session AND d.quantity>0 AND d.error='' AND d.observed_at>now()-interval '10 minutes')
 FROM monitored_subnets n JOIN monitor_collectors c ON c.id=n.collector_id LEFT JOIN monitor_sessions b ON b.id=c.current_session LEFT JOIN subnet_discovery x ON x.subnet_id=n.id AND x.revision=n.revision WHERE n.id=$1`, id).Scan(&v.Capable, &v.Checked, &v.Failed, &v.Total, &v.Round, &v.Started, &v.Progress, &v.Completed, &v.Found)
	if err != nil {
		return v, notFound(err)
	}
	err = s.subnetNativeStatus(ctx, id, &v)
	return v, err
}
func (s *Store) SubnetDiscovered(ctx context.Context, member, group, id, kind, q string, offset int) (SubnetDiscoveryPage, error) {
	out := SubnetDiscoveryPage{Items: []SubnetDiscoveredItem{}}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return out, err
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM monitored_subnets WHERE id=$1 AND server_group=$2)`, id, group).Scan(&exists); err != nil {
		return out, err
	}
	if !exists {
		return out, ErrNotFound
	}
	if kind != "item" && kind != "fluid" || len(q) > 256 || offset < 0 || offset > 100000 {
		return out, ErrInvalid
	}
	args := []any{id, kind}
	conditions, err := searchConditions(q, "d.label", "d.search_label", func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) })
	if err != nil {
		return out, err
	}
	from := ` FROM subnet_discovered x JOIN subnet_catalog d ON d.id=x.item_id JOIN monitored_subnets n ON n.id=x.subnet_id JOIN monitor_collectors c ON c.id=n.collector_id JOIN subnet_plugins p ON p.server_group=n.server_group
 WHERE n.id=$1 AND d.kind=$2 AND n.enabled AND p.enabled AND NOT c.revoked AND c.last_seen>now()-n.stale_seconds*interval '1 second' AND x.revision=n.revision AND x.session_id=c.current_session AND x.quantity>0 AND x.error='' AND x.observed_at>now()-interval '10 minutes'`
	if len(conditions) > 0 {
		from += " AND " + strings.Join(conditions, " AND ")
	}
	if err = s.pool.QueryRow(ctx, "SELECT count(*)"+from, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	args = append(args, offset)
	rows, err := s.pool.Query(ctx, `SELECT d.id,d.kind,d.name,d.damage,d.nbt,d.label,x.quantity,x.observed_at,EXISTS(SELECT 1 FROM subnet_items i WHERE i.subnet_id=n.id AND i.item_id=d.id AND i.enabled)`+from+fmt.Sprintf(" ORDER BY d.label,d.id LIMIT 50 OFFSET $%d", len(args)), args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var i SubnetDiscoveredItem
		if err = rows.Scan(&i.ID, &i.Kind, &i.Name, &i.Damage, &i.NBT, &i.Label, &i.Quantity, &i.Observed, &i.Monitored); err != nil {
			rows.Close()
			return out, err
		}
		i.Exact = true
		s.subnetCatalogMeta(&i.SubnetCatalogItem)
		out.Items = append(out.Items, i)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.Status, err = s.subnetDiscoveryStatus(ctx, id)
	return out, err
}

// The discovery cursor has at most sixteen retained identities per subnet.
// Only positive observations are kept outside the selected monitoring list.
func ensureSubnetDiscovery(ctx context.Context, tx pgx.Tx, w *SubnetWelcome) error {
	_, err := tx.Exec(ctx, `INSERT INTO subnet_discovery(subnet_id,revision,total) VALUES($1,$2,(SELECT count(*) FROM subnet_catalog)) ON CONFLICT(subnet_id) DO UPDATE SET revision=excluded.revision,cursor=0,checked=0,failed=0,total=excluded.total,round=1,started_at=now(),progress_at=NULL,completed_at=NULL WHERE subnet_discovery.revision<>excluded.revision`, w.SubnetID, w.Revision)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM subnet_discovery_reads WHERE subnet_id=$1 AND revision<>$2`, w.SubnetID, w.Revision); err != nil {
		return err
	}
	// Newly selected items switch to the fast lane. Remove their old discovery
	// task so one poll never contains two assignments for the same identity.
	_, err = tx.Exec(ctx, `WITH removed AS (DELETE FROM subnet_discovery_reads x WHERE x.subnet_id=$1 AND EXISTS(SELECT 1 FROM subnet_items i WHERE i.subnet_id=x.subnet_id AND i.item_id=x.item_id AND i.enabled) RETURNING position) UPDATE subnet_discovery SET checked=checked+(SELECT count(*) FROM removed WHERE position>0) WHERE subnet_id=$1`, w.SubnetID)
	return err
}
func subnetDiscoveryAssignments(ctx context.Context, tx pgx.Tx, w *SubnetWelcome) error {
	capacity := min(8, 16-len(w.Reads), w.Budget-len(w.Reads))
	if capacity <= 0 {
		return nil
	}
	if err := ensureSubnetDiscovery(ctx, tx, w); err != nil {
		return err
	}
	var cursor, total int64
	var completed *time.Time
	if err := tx.QueryRow(ctx, `SELECT cursor,total,completed_at FROM subnet_discovery WHERE subnet_id=$1 FOR UPDATE`, w.SubnetID).Scan(&cursor, &total, &completed); err != nil {
		return err
	}
	if completed != nil && time.Since(*completed) > time.Hour {
		if _, err := tx.Exec(ctx, `UPDATE subnet_discovery SET cursor=0,checked=0,failed=0,round=round+1,started_at=now(),completed_at=NULL WHERE subnet_id=$1`, w.SubnetID); err != nil {
			return err
		}
		cursor = 0
		completed = nil
	}
	var retained int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM subnet_discovery_reads WHERE subnet_id=$1`, w.SubnetID).Scan(&retained); err != nil {
		return err
	}
	if retained < 16 {
		tag, err := tx.Exec(ctx, `INSERT INTO subnet_discovery_reads(subnet_id,item_id,position,revision) SELECT $1,d.item_id,0,$2 FROM subnet_discovered d WHERE d.subnet_id=$1 AND d.revision=$2 AND d.quantity>0 AND d.next_read_at<=now() AND NOT EXISTS(SELECT 1 FROM subnet_items i WHERE i.subnet_id=$1 AND i.item_id=d.item_id AND i.enabled) ORDER BY d.next_read_at,d.item_id LIMIT $3 ON CONFLICT DO NOTHING`, w.SubnetID, w.Revision, 16-retained)
		if err != nil {
			return err
		}
		retained += int(tag.RowsAffected())
	}
	if retained < 16 && completed == nil {
		rows, err := tx.Query(ctx, `SELECT d.id,d.scan_order FROM subnet_catalog d WHERE d.scan_order>$1 ORDER BY d.scan_order LIMIT $2`, cursor, 16-retained)
		if err != nil {
			return err
		}
		type candidate struct {
			id  string
			pos int64
		}
		cs := []candidate{}
		for rows.Next() {
			var c candidate
			if err = rows.Scan(&c.id, &c.pos); err != nil {
				rows.Close()
				return err
			}
			cs = append(cs, c)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		for _, c := range cs {
			var monitored bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subnet_items WHERE subnet_id=$1 AND item_id=$2 AND enabled)`, w.SubnetID, c.id).Scan(&monitored); err != nil {
				return err
			}
			if monitored {
				if _, err = tx.Exec(ctx, `UPDATE subnet_discovery SET checked=checked+1 WHERE subnet_id=$1`, w.SubnetID); err != nil {
					return err
				}
			} else if _, err = tx.Exec(ctx, `INSERT INTO subnet_discovery_reads(subnet_id,item_id,position,revision) VALUES($1,$2,$3,$4) ON CONFLICT(subnet_id,item_id) DO UPDATE SET position=greatest(subnet_discovery_reads.position,excluded.position)`, w.SubnetID, c.id, c.pos, w.Revision); err != nil {
				return err
			}
			cursor = c.pos
		}
		if _, err = tx.Exec(ctx, `UPDATE subnet_discovery SET cursor=$2 WHERE subnet_id=$1`, w.SubnetID, cursor); err != nil {
			return err
		}
	}
	rows, err := tx.Query(ctx, `SELECT d.id,d.kind,d.name,d.damage,d.nbt,d.label FROM subnet_discovery_reads x JOIN subnet_catalog d ON d.id=x.item_id WHERE x.subnet_id=$1 AND (x.assignment_until IS NULL OR x.assignment_until<=now() OR x.assignment_session<>$2) ORDER BY x.position,d.id LIMIT $3 FOR UPDATE OF x`, w.SubnetID, w.Session, capacity)
	if err != nil {
		return err
	}
	issued := []SubnetRead{}
	for rows.Next() {
		var r SubnetRead
		if err = rows.Scan(&r.ItemID, &r.Item.Kind, &r.Item.Name, &r.Item.Damage, &r.Item.NBT, &r.Item.Label); err != nil {
			rows.Close()
			return err
		}
		r.Item.Exact = true
		r.AssignmentID = newID()
		r.Revision = w.Revision
		r.TTL = 5000
		r.Purpose = "discovery"
		w.Reads = append(w.Reads, r)
		raw, _ := json.Marshal(w)
		if len(raw) > 30000 {
			w.Reads = w.Reads[:len(w.Reads)-1]
			break
		}
		issued = append(issued, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, r := range issued {
		if _, err = tx.Exec(ctx, `UPDATE subnet_discovery_reads SET assignment_id=$3,assignment_session=$4,assignment_until=now()+interval '5 seconds' WHERE subnet_id=$1 AND item_id=$2`, w.SubnetID, r.ItemID, r.AssignmentID, w.Session); err != nil {
			return err
		}
	}
	if cursor >= total {
		_, err = tx.Exec(ctx, `UPDATE subnet_discovery SET completed_at=coalesce(completed_at,now()) WHERE subnet_id=$1 AND NOT EXISTS(SELECT 1 FROM subnet_discovery_reads WHERE subnet_id=$1 AND position>0)`, w.SubnetID)
	}
	return err
}
func acceptSubnetDiscovery(ctx context.Context, tx pgx.Tx, w *SubnetWelcome, r SubnetResult, now time.Time) (bool, error) {
	var pos int64
	err := tx.QueryRow(ctx, `DELETE FROM subnet_discovery_reads WHERE subnet_id=$1 AND item_id=$2 AND assignment_id=$3 AND assignment_session=$4 AND revision=$5 AND assignment_until>now() RETURNING position`, w.SubnetID, r.ItemID, r.AssignmentID, w.Session, w.Revision).Scan(&pos)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if pos > 0 {
		if _, err = tx.Exec(ctx, `UPDATE subnet_discovery SET checked=checked+1,failed=failed+CASE WHEN $2 THEN 1 ELSE 0 END,progress_at=$3 WHERE subnet_id=$1`, w.SubnetID, r.Error != "", now); err != nil {
			return false, err
		}
	}
	return true, recordSubnetDiscovered(ctx, tx, w, r, now)
}
func recordSubnetDiscovered(ctx context.Context, tx pgx.Tx, w *SubnetWelcome, r SubnetResult, now time.Time) error {
	if r.Error != "" {
		_, err := tx.Exec(ctx, `UPDATE subnet_discovered SET error=$3,next_read_at=now()+interval '60 seconds' WHERE subnet_id=$1 AND item_id=$2`, w.SubnetID, r.ItemID, r.Error)
		return err
	}
	if *r.Quantity == 0 {
		_, err := tx.Exec(ctx, `DELETE FROM subnet_discovered WHERE subnet_id=$1 AND item_id=$2`, w.SubnetID, r.ItemID)
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO subnet_discovered(subnet_id,item_id,revision,session_id,quantity,observed_at,next_read_at) VALUES($1,$2,$3,$4,$5,$6::timestamptz,$6::timestamptz+interval '60 seconds') ON CONFLICT(subnet_id,item_id) DO UPDATE SET revision=excluded.revision,session_id=excluded.session_id,quantity=excluded.quantity,observed_at=excluded.observed_at,error='',next_read_at=excluded.next_read_at`, w.SubnetID, r.ItemID, w.Revision, w.Session, int64(*r.Quantity), now)
	return err
}
