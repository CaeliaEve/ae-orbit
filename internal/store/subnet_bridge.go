package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

type SubnetHello struct {
	BootID           string             `json:"boot_id"`
	Components       []MonitorComponent `json:"components"`
	DiscoveryCapable bool               `json:"inventory_discovery,omitempty"`
	SnapshotCapable  bool               `json:"inventory_snapshot,omitempty"`
}
type SubnetRead struct {
	ItemID       string              `json:"item_id"`
	Item         protocol.Definition `json:"item"`
	AssignmentID string              `json:"assignment_id"`
	Revision     int64               `json:"revision"`
	TTL          int                 `json:"ttl_millis"`
	Purpose      string              `json:"purpose,omitempty"`
}
type SubnetWelcome struct {
	Session    string                    `json:"session_id"`
	Sequence   int64                     `json:"sequence"`
	SubnetID   string                    `json:"subnet_id"`
	Address    string                    `json:"component_address"`
	Revision   int64                     `json:"revision"`
	Interval   int                       `json:"interval_seconds"`
	PollMillis int                       `json:"poll_millis"`
	Budget     int                       `json:"budget"`
	TTL        int                       `json:"grant_ttl_millis"`
	GrantID    string                    `json:"grant_id"`
	Reads      []SubnetRead              `json:"reads"`
	ItemCount  int                       `json:"item_count"`
	Snapshot   *SubnetSnapshotAssignment `json:"snapshot,omitempty"`
}
type SubnetResult struct {
	ItemID       string             `json:"item_id"`
	AssignmentID string             `json:"assignment_id"`
	Revision     int64              `json:"revision"`
	SampleMillis int64              `json:"sample_millis"`
	Quantity     *protocol.Quantity `json:"quantity"`
	Error        string             `json:"error,omitempty"`
}
type SubnetPoll struct {
	Session     string                `json:"session_id"`
	Sequence    int64                 `json:"sequence"`
	FreeMemory  int64                 `json:"free_memory"`
	Samples     []SubnetResult        `json:"samples"`
	Components  *[]MonitorComponent   `json:"components,omitempty"`
	CycleMillis int64                 `json:"cycle_millis,omitempty"`
	Snapshot    *SubnetSnapshotResult `json:"snapshot,omitempty"`
}

func validSubnetComponents(cs []MonitorComponent) bool {
	if len(cs) > 8 {
		return false
	}
	seen := map[string]bool{}
	for _, c := range cs {
		if !componentAddress.MatchString(c.Address) || c.Type != "me_interface" && c.Type != "me_controller" || seen[c.Address] {
			return false
		}
		seen[c.Address] = true
	}
	return true
}
func lockSubnetCollector(ctx context.Context, tx pgx.Tx, a MonitorAuth) error {
	var id string
	return notFound(tx.QueryRow(ctx, `SELECT id FROM monitor_collectors WHERE id=$1 AND token_hash=$2 AND purpose='subnet' AND NOT revoked FOR UPDATE`, a.ID, a.Hash).Scan(&id))
}
func subnetWelcome(ctx context.Context, tx pgx.Tx, a MonitorAuth, w *SubnetWelcome) error {
	w.Reads = []SubnetRead{}
	w.PollMillis = 250
	err := tx.QueryRow(ctx, `SELECT id,component_address,revision,interval_seconds,(SELECT count(*) FROM subnet_items WHERE subnet_id=n.id AND enabled) FROM monitored_subnets n WHERE collector_id=$1 AND server_group=$2 FOR UPDATE OF n`, a.ID, a.Group).Scan(&w.SubnetID, &w.Address, &w.Revision, &w.Interval, &w.ItemCount)
	return notFound(err)
}
func (s *Store) SubnetHello(ctx context.Context, a MonitorAuth, h SubnetHello) (SubnetWelcome, error) {
	var w SubnetWelcome
	if a.Purpose != "subnet" || !monitorID.MatchString(h.BootID) || !validSubnetComponents(h.Components) {
		return w, ErrInvalid
	}
	err := s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		if err := lockSubnetCollector(ctx, tx, a); err != nil {
			return err
		}
		var current string
		var retired bool
		if err := tx.QueryRow(ctx, `SELECT current_session FROM monitor_collectors WHERE id=$1`, a.ID).Scan(&current); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT id,sequence,retired FROM monitor_sessions WHERE collector_id=$1 AND boot_id=$2`, a.ID, h.BootID).Scan(&w.Session, &w.Sequence, &retired)
		if err == nil {
			if retired || current != w.Session {
				return ErrStaleSession
			}
		} else if err == pgx.ErrNoRows {
			w.Session = newID()
			if _, err = tx.Exec(ctx, `UPDATE monitor_sessions SET retired=true WHERE collector_id=$1`, a.ID); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO monitor_sessions(id,collector_id,boot_id) VALUES($1,$2,$3)`, w.Session, a.ID, h.BootID); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `UPDATE subnet_rules SET since=NULL WHERE subnet_id IN(SELECT id FROM monitored_subnets WHERE collector_id=$1)`, a.ID); err != nil {
				return err
			}
		} else {
			return err
		}
		raw, _ := json.Marshal(h.Components)
		if _, err = tx.Exec(ctx, `UPDATE monitor_collectors SET current_session=$2,components=$3,modules='["aeorbit.subnet"]',last_seen=now() WHERE id=$1`, a.ID, w.Session, raw); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE monitor_sessions SET subnet_discovery_capable=$2,subnet_snapshot_capable=$3 WHERE id=$1`, w.Session, h.DiscoveryCapable, h.SnapshotCapable); err != nil {
			return err
		}
		return subnetWelcome(ctx, tx, a, &w)
	})
	return w, err
}
func (s *Store) SubnetPoll(ctx context.Context, a MonitorAuth, p SubnetPoll) (SubnetWelcome, error) {
	var w SubnetWelcome
	if a.Purpose != "subnet" || !monitorID.MatchString(p.Session) || p.Sequence < 1 || p.Sequence > 1000000000 || p.FreeMemory < 0 || p.FreeMemory > 1<<40 || len(p.Samples) > 16 || p.CycleMillis < 0 || p.CycleMillis > 86400000 || p.Components != nil && !validSubnetComponents(*p.Components) {
		return w, ErrInvalid
	}
	if p.Snapshot != nil && !validSubnetSnapshot(*p.Snapshot) {
		return w, ErrInvalid
	}
	seen := map[string]bool{}
	for _, r := range p.Samples {
		if !monitorID.MatchString(r.ItemID) || !monitorID.MatchString(r.AssignmentID) || seen[r.ItemID] || r.Revision < 1 || r.SampleMillis < 0 || r.SampleMillis > 9007199254740991 {
			return w, ErrInvalid
		}
		seen[r.ItemID] = true
		if r.Error != "" {
			if r.Quantity != nil || r.Error != "read_failed" && r.Error != "identity_mismatch" && r.Error != "component_missing" && r.Error != "memory_low" && r.Error != "interface_unsupported" {
				return w, ErrInvalid
			}
		} else if r.Quantity == nil || *r.Quantity < 0 {
			return w, ErrInvalid
		}
	}
	raw, _ := json.Marshal(p)
	hash := sha256.Sum256(raw)
	err := s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		if err := lockSubnetCollector(ctx, tx, a); err != nil {
			return err
		}
		var seq, priorMillis int64
		var priorAt *time.Time
		var priorHash, receipt []byte
		err := tx.QueryRow(ctx, `SELECT b.sequence,b.receipt_hash,b.receipt,b.last_sample_millis,b.last_sample_at FROM monitor_sessions b JOIN monitor_collectors c ON c.id=b.collector_id WHERE b.id=$1 AND b.collector_id=$2 AND c.current_session=b.id AND NOT b.retired FOR UPDATE OF b`, p.Session, a.ID).Scan(&seq, &priorHash, &receipt, &priorMillis, &priorAt)
		if err == pgx.ErrNoRows {
			return ErrStaleSession
		}
		if err != nil {
			return err
		}
		if p.Sequence == seq {
			if string(priorHash) != string(hash[:]) {
				return ErrSequence
			}
			if err = json.Unmarshal(receipt, &w); err != nil {
				return err
			}
			w.Reads = []SubnetRead{}
			w.Snapshot = nil
			w.Budget = 0
			w.GrantID = ""
			w.TTL = 0
			return nil
		}
		if p.Sequence != seq+1 {
			return ErrSequence
		}
		if err = subnetWelcome(ctx, tx, a, &w); err != nil {
			return err
		}
		w.Session = p.Session
		w.Sequence = p.Sequence
		if _, err = tx.Exec(ctx, `SELECT name FROM server_groups WHERE name=$1 FOR UPDATE`, a.Group); err != nil {
			return err
		}
		var enabled bool
		var stale, history int
		var now time.Time
		if err = tx.QueryRow(ctx, `SELECT n.enabled AND s.enabled,n.stale_seconds,n.history_seconds,clock_timestamp() FROM monitored_subnets n JOIN subnet_plugins s ON s.server_group=n.server_group WHERE n.id=$1`, w.SubnetID).Scan(&enabled, &stale, &history, &now); err != nil {
			return err
		}
		if p.Components != nil {
			raw, _ := json.Marshal(*p.Components)
			if _, err = tx.Exec(ctx, `UPDATE monitor_collectors SET components=$2 WHERE id=$1`, a.ID, raw); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE monitor_collectors SET last_seen=now(),free_memory=$2 WHERE id=$1`, a.ID, p.FreeMemory); err != nil {
			return err
		}
		if p.CycleMillis > 0 {
			if _, err = tx.Exec(ctx, `UPDATE monitored_subnets SET cycle_millis=$2 WHERE id=$1`, w.SubnetID, p.CycleMillis); err != nil {
				return err
			}
		}
		if p.Snapshot != nil && enabled {
			if err = s.acceptNativeSubnetSnapshot(ctx, tx, &w, *p.Snapshot, now); err != nil {
				return err
			}
		}
		latestMillis := priorMillis
		for _, r := range p.Samples {
			var valid bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subnet_items i WHERE subnet_id=$1 AND item_id=$2 AND assignment_id=$3 AND assignment_session=$4 AND assignment_revision=$5 AND enabled AND assignment_until>now())`, w.SubnetID, r.ItemID, r.AssignmentID, p.Session, r.Revision).Scan(&valid); err != nil {
				return err
			}
			if !enabled || r.Revision != w.Revision {
				continue
			}
			if priorAt != nil && priorMillis > 0 && now.Sub(*priorAt).Seconds()-float64(r.SampleMillis-priorMillis)/1000 > float64(stale) {
				continue
			}
			if !valid {
				accepted, e := acceptSubnetDiscovery(ctx, tx, &w, r, now)
				if e != nil {
					return e
				}
				if accepted && r.SampleMillis > latestMillis {
					latestMillis = r.SampleMillis
				}
				continue
			}
			if r.SampleMillis > latestMillis {
				latestMillis = r.SampleMillis
			}
			if err = acceptSubnetSample(ctx, tx, w.SubnetID, p.Session, r, stale, history, now); err != nil {
				return err
			}
			if err = recordSubnetDiscovered(ctx, tx, &w, r, now); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `UPDATE subnet_items SET assignment_id='',assignment_until=NULL,next_read_at=now()+$3::int*interval '1 second' WHERE subnet_id=$1 AND item_id=$2`, w.SubnetID, r.ItemID, w.Interval); err != nil {
				return err
			}
		}
		if err = queueSubnetNotifications(ctx, tx, w.SubnetID); err != nil {
			return err
		}
		if enabled && w.Address != "" && p.FreeMemory >= 262144 {
			if err = s.subnetGrant(ctx, tx, a, w.SubnetID, p.Session, &w); err != nil {
				return err
			}
			if w.Budget > 0 {
				if err = subnetReadAssignments(ctx, tx, &w); err != nil {
					return err
				}
				var discovery bool
				if err = tx.QueryRow(ctx, `SELECT b.subnet_discovery_capable AND n.discovery_mode='exact' FROM monitor_sessions b JOIN monitored_subnets n ON n.collector_id=b.collector_id WHERE b.id=$1`, w.Session).Scan(&discovery); err != nil {
					return err
				}
				if discovery {
					if err = subnetDiscoveryAssignments(ctx, tx, &w); err != nil {
						return err
					}
				}
				w.Budget = len(w.Reads)
			}
		}
		if enabled && w.Address != "" && p.FreeMemory >= 524288 {
			if err = nativeSubnetSnapshotAssignment(ctx, tx, &w); err != nil {
				return err
			}
		}
		body, _ := json.Marshal(w)
		if len(body) > 32768 {
			return ErrCapacity
		}
		_, err = tx.Exec(ctx, `UPDATE monitor_sessions SET sequence=$2,receipt_hash=$3,receipt=$4,last_sample_millis=$5,last_sample_at=CASE WHEN $5>last_sample_millis THEN $6 ELSE last_sample_at END WHERE id=$1`, p.Session, p.Sequence, hash[:], body, latestMillis, now)
		return err
	})
	return w, err
}
func subnetReadAssignments(ctx context.Context, tx pgx.Tx, w *SubnetWelcome) error {
	rows, err := tx.Query(ctx, `SELECT d.id,d.kind,d.name,d.damage,d.nbt,d.label FROM subnet_items i JOIN subnet_catalog d ON d.id=i.item_id WHERE i.subnet_id=$1 AND i.enabled AND i.next_read_at<=now() AND (i.assignment_until IS NULL OR i.assignment_until<=now() OR i.assignment_session<>$2 OR i.assignment_revision<>$3) ORDER BY i.next_read_at,d.id LIMIT $4 FOR UPDATE OF i`, w.SubnetID, w.Session, w.Revision, min(16, w.Budget))
	if err != nil {
		return err
	}
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
		r.Purpose = "monitor"
		w.Reads = append(w.Reads, r)
		b, _ := json.Marshal(w)
		if len(b) > 30000 {
			w.Reads = w.Reads[:len(w.Reads)-1]
			break
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, r := range w.Reads {
		if _, err = tx.Exec(ctx, `UPDATE subnet_items SET assignment_id=$3,assignment_until=now()+interval '5 seconds',assignment_session=$4,assignment_revision=$5 WHERE subnet_id=$1 AND item_id=$2`, w.SubnetID, r.ItemID, r.AssignmentID, w.Session, w.Revision); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) subnetGrant(ctx context.Context, tx pgx.Tx, a MonitorAuth, subnet, session string, w *SubnetWelcome) error {
	var due bool
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT next_scan_at<=now(),now() FROM server_groups WHERE name=$1`, a.Group).Scan(&due, &now); err != nil {
		return err
	}
	if due {
		if err := s.allocateAcquisitionRound(ctx, tx, a.Group, now); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO subnet_read_intents(subnet_id,session_id) VALUES($1,$2) ON CONFLICT(subnet_id) DO UPDATE SET session_id=excluded.session_id,last_intent_at=now(),grant_budget=CASE WHEN subnet_read_intents.session_id=excluded.session_id THEN subnet_read_intents.grant_budget ELSE 0 END,claimed=CASE WHEN subnet_read_intents.session_id=excluded.session_id THEN subnet_read_intents.claimed ELSE false END`, subnet, session); err != nil {
		return err
	}
	err := tx.QueryRow(ctx, `UPDATE subnet_read_intents SET claimed=true WHERE subnet_id=$1 AND session_id=$2 AND NOT claimed AND grant_budget>0 AND grant_until>now() RETURNING grant_budget,grant_id,greatest(1,floor(extract(epoch FROM grant_until-now())*1000))::int`, subnet, session).Scan(&w.Budget, &w.GrantID, &w.TTL)
	if err == pgx.ErrNoRows {
		return nil
	}
	return err
}
