package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/plugins"
	"github.com/jackc/pgx/v5"
)

func validMonitorComponents(cs []MonitorComponent) bool {
	if len(cs) > 32 {
		return false
	}
	seen := map[string]bool{}
	for _, c := range cs {
		if !componentAddress.MatchString(c.Address) || c.Type != "LSC" || seen[c.Address] {
			return false
		}
		seen[c.Address] = true
	}
	return true
}
func lockMonitor(ctx context.Context, tx pgx.Tx, a MonitorAuth) error {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM monitor_collectors WHERE id=$1 AND token_hash=$2 AND NOT revoked FOR UPDATE`, a.ID, a.Hash).Scan(&id)
	if err == pgx.ErrNoRows {
		return ErrUnauthorized
	}
	return err
}
func monitorAssignments(ctx context.Context, tx pgx.Tx, a MonitorAuth) ([]MonitorAssignment, error) {
	rows, err := tx.Query(ctx, `SELECT o.id,o.plugin_id,o.component_address,o.revision,p.revision,(p.settings->>'interval_seconds')::int FROM plugin_objects o JOIN plugin_installations p USING(server_group,plugin_id) JOIN monitor_collectors c ON c.id=o.collector_id WHERE o.collector_id=$1 AND o.enabled AND p.enabled AND c.modules ? o.plugin_id ORDER BY o.id LIMIT 16`, a.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MonitorAssignment{}
	for rows.Next() {
		var x MonitorAssignment
		if err = rows.Scan(&x.ID, &x.PluginID, &x.Address, &x.Revision, &x.InstallationRevision, &x.Interval); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) MonitorHello(ctx context.Context, a MonitorAuth, h MonitorHello) (MonitorWelcome, error) {
	var w MonitorWelcome
	if !monitorID.MatchString(h.BootID) || len(h.Modules) > 8 || !validMonitorComponents(h.Components) {
		return w, ErrInvalid
	}
	seen := map[string]bool{}
	for _, m := range h.Modules {
		if !plugins.Supported(m) || seen[m] {
			return w, ErrInvalid
		}
		seen[m] = true
	}
	if h.Modules == nil {
		h.Modules = []string{}
	}
	if h.Components == nil {
		h.Components = []MonitorComponent{}
	}
	err := s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		if err := lockMonitor(ctx, tx, a); err != nil {
			return err
		}
		var current string
		if err := tx.QueryRow(ctx, `SELECT current_session FROM monitor_collectors WHERE id=$1`, a.ID).Scan(&current); err != nil {
			return err
		}
		var retired bool
		err := tx.QueryRow(ctx, `SELECT id,sequence,retired FROM monitor_sessions WHERE collector_id=$1 AND boot_id=$2`, a.ID, h.BootID).Scan(&w.Session, &w.Sequence, &retired)
		if err == nil {
			if retired || current != w.Session {
				return ErrStaleSession
			}
		} else if err == pgx.ErrNoRows {
			w.Session = newID()
			if _, err = tx.Exec(ctx, `UPDATE monitor_sessions SET retired=true WHERE collector_id=$1 AND NOT retired`, a.ID); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO monitor_sessions(id,collector_id,boot_id) VALUES($1,$2,$3)`, w.Session, a.ID, h.BootID); err != nil {
				return err
			}
		} else {
			return err
		}
		modules, _ := json.Marshal(h.Modules)
		components, _ := json.Marshal(h.Components)
		if _, err = tx.Exec(ctx, `UPDATE monitor_collectors SET current_session=$2,modules=$3,components=$4,last_seen=now() WHERE id=$1`, a.ID, w.Session, modules, components); err != nil {
			return err
		}
		w.PollSeconds = 5
		w.Objects, err = monitorAssignments(ctx, tx, a)
		return err
	})
	return w, err
}

var sampleErrors = map[string]bool{"component_missing": true, "interface_unsupported": true, "read_failed": true, "invalid_energy": true, "memory_low": true}

func (s *Store) MonitorPoll(ctx context.Context, a MonitorAuth, p MonitorPoll) (MonitorWelcome, error) {
	var w MonitorWelcome
	if !monitorID.MatchString(p.Session) || p.Sequence < 1 || p.Sequence > 1000000000 || p.FreeMemory < 0 || p.FreeMemory > 1<<40 || len(p.Samples) > 16 || p.Components != nil && !validMonitorComponents(*p.Components) {
		return w, ErrInvalid
	}
	seen := map[string]bool{}
	for _, v := range p.Samples {
		if !monitorID.MatchString(v.ObjectID) || seen[v.ObjectID] || v.Revision < 1 || v.InstallationRevision < 1 || v.SampleMillis < 0 || v.SampleMillis > 9007199254740991 {
			return w, ErrInvalid
		}
		seen[v.ObjectID] = true
		if v.Error != "" {
			if !sampleErrors[v.Error] || v.Stored != "" || v.Capacity != "" {
				return w, ErrInvalid
			}
		} else if plugins.ValidateEnergy(v.Stored, v.Capacity) != nil {
			return w, ErrInvalid
		}
	}
	raw, _ := json.Marshal(p)
	hash := sha256.Sum256(raw)
	err := s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		if err := lockMonitor(ctx, tx, a); err != nil {
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
			return json.Unmarshal(receipt, &w)
		}
		if p.Sequence != seq+1 {
			return ErrSequence
		}
		// A fresh receipt cannot claim arbitrarily old OC samples as live data.
		var serverNow time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&serverNow); err != nil {
			return err
		}
		latestMillis := priorMillis
		for _, sample := range p.Samples {
			if sample.SampleMillis > latestMillis {
				latestMillis = sample.SampleMillis
			}
		}
		if p.Components != nil {
			b, _ := json.Marshal(*p.Components)
			if _, err = tx.Exec(ctx, `UPDATE monitor_collectors SET components=$2 WHERE id=$1`, a.ID, b); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE monitor_collectors SET last_seen=now(),free_memory=$2 WHERE id=$1`, a.ID, p.FreeMemory); err != nil {
			return err
		}
		for _, v := range p.Samples {
			var rev, installation int64
			var enabled bool
			var settingsRaw []byte
			err = tx.QueryRow(ctx, `SELECT o.revision,p.revision,o.enabled AND p.enabled,p.settings FROM plugin_objects o JOIN plugin_installations p USING(server_group,plugin_id) WHERE o.id=$1 AND o.collector_id=$2 AND o.server_group=$3 FOR SHARE OF o,p`, v.ObjectID, a.ID, a.Group).Scan(&rev, &installation, &enabled, &settingsRaw)
			if err == pgx.ErrNoRows {
				return ErrForbidden
			}
			if err != nil {
				return err
			}
			if !enabled || v.Revision != rev || v.InstallationRevision != installation {
				continue
			}
			var settings plugins.Settings
			if err = json.Unmarshal(settingsRaw, &settings); err != nil {
				return err
			}
			if priorAt != nil && priorMillis > 0 {
				lagSeconds := serverNow.Sub(*priorAt).Seconds() - float64(v.SampleMillis-priorMillis)/1000
				if lagSeconds > float64(settings.OfflineSeconds) {
					// ACK the packet, retain old observation, then reanchor the next
					// sample; stalled game ticks and delayed retries remain unknown.
					continue
				}
			}
			if err = acceptMonitorSample(ctx, tx, p.Session, v, settings); err != nil {
				return err
			}
		}
		w.Session = p.Session
		w.Sequence = p.Sequence
		w.PollSeconds = 5
		w.Objects, err = monitorAssignments(ctx, tx, a)
		if err != nil {
			return err
		}
		body, _ := json.Marshal(w)
		_, err = tx.Exec(ctx, `UPDATE monitor_sessions SET sequence=$2,receipt_hash=$3,receipt=$4,last_sample_millis=$5,last_sample_at=CASE WHEN $5>last_sample_millis THEN $6 ELSE last_sample_at END WHERE id=$1`, p.Session, p.Sequence, hash[:], body, latestMillis, serverNow)
		return err
	})
	return w, err
}
func acceptMonitorSample(ctx context.Context, tx pgx.Tx, session string, v MonitorSample, settings plugins.Settings) error {
	var priorSession, priorStored, priorCapacity, priorError string
	var sample, epoch int64
	var reported time.Time
	var observed *time.Time
	err := tx.QueryRow(ctx, `SELECT session_id,sample_millis,coalesce(stored_eu::text,''),coalesce(capacity_eu::text,''),epoch,reported_at,observed_at,error FROM monitor_latest WHERE object_id=$1 FOR UPDATE`, v.ObjectID).Scan(&priorSession, &sample, &priorStored, &priorCapacity, &epoch, &reported, &observed, &priorError)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	exists := err == nil
	if exists && priorSession == session && v.SampleMillis <= sample {
		return nil
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return err
	}
	if exists && now.Sub(reported) < time.Duration(settings.IntervalSeconds)*time.Second*8/10 {
		return nil
	}
	if epoch == 0 {
		epoch = 1
	}
	reset := exists && (priorSession != session || v.Error == "" && (priorCapacity != "" && v.Capacity != priorCapacity || observed != nil && now.Sub(*observed) > time.Duration(settings.OfflineSeconds)*time.Second || priorError != ""))
	if reset {
		epoch++
		if _, err = tx.Exec(ctx, `DELETE FROM monitor_conditions WHERE object_id=$1`, v.ObjectID); err != nil {
			return err
		}
	}
	if v.Error != "" {
		_, err = tx.Exec(ctx, `INSERT INTO monitor_latest(object_id,session_id,sample_millis,epoch,error,reported_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(object_id) DO UPDATE SET session_id=excluded.session_id,sample_millis=excluded.sample_millis,epoch=excluded.epoch,error=excluded.error,reported_at=excluded.reported_at`, v.ObjectID, session, v.SampleMillis, epoch, v.Error, now)
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO monitor_latest(object_id,session_id,sample_millis,stored_eu,capacity_eu,epoch,observed_at,reported_at,error) VALUES($1,$2,$3,$4::numeric,$5::numeric,$6,$7,$7,'') ON CONFLICT(object_id) DO UPDATE SET session_id=excluded.session_id,sample_millis=excluded.sample_millis,stored_eu=excluded.stored_eu,capacity_eu=excluded.capacity_eu,epoch=excluded.epoch,observed_at=excluded.observed_at,reported_at=excluded.reported_at,error=''`, v.ObjectID, session, v.SampleMillis, v.Stored, v.Capacity, epoch, now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO monitor_energy_records(object_id,epoch,session_id,stored_eu,capacity_eu,observed_at) VALUES($1,$2,$3,$4::numeric,$5::numeric,$6)`, v.ObjectID, epoch, session, v.Stored, v.Capacity, now)
	if err != nil {
		return err
	}
	for _, g := range []string{"hour", "day"} {
		_, err = tx.Exec(ctx, `INSERT INTO monitor_energy_rollups(object_id,epoch,granularity,bucket,capacity_eu,first_eu,last_eu,min_eu,max_eu,first_at,last_at) VALUES($1,$2,$3,date_trunc($3,$6::timestamptz AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',$5::numeric,$4::numeric,$4::numeric,$4::numeric,$4::numeric,$6,$6) ON CONFLICT(object_id,granularity,bucket,epoch) DO UPDATE SET last_eu=excluded.last_eu,min_eu=least(monitor_energy_rollups.min_eu,excluded.min_eu),max_eu=greatest(monitor_energy_rollups.max_eu,excluded.max_eu),last_at=excluded.last_at,samples=monitor_energy_rollups.samples+1`, v.ObjectID, epoch, g, v.Stored, v.Capacity, now)
		if err != nil {
			return err
		}
	}
	if exists && priorCapacity != "" && priorCapacity != v.Capacity && settings.Alerts {
		if err = monitorTrigger(ctx, tx, v.ObjectID, "capacity_changed", "电容库容量发生变化，请核对扩容或设备替换。", now); err != nil {
			return err
		}
	}
	return nil
}
