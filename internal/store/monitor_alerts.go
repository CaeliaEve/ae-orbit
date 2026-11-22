package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/plugins"
	"github.com/jackc/pgx/v5"
)

type MonitorAlert struct {
	ID             string     `json:"id"`
	ObjectID       string     `json:"object_id"`
	ObjectName     string     `json:"object_name"`
	Kind           string     `json:"kind"`
	State          string     `json:"state"`
	Detail         string     `json:"detail"`
	TriggeredAt    time.Time  `json:"triggered_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	ResolveReason  string     `json:"resolve_reason,omitempty"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBy string     `json:"acknowledged_by,omitempty"`
}

func monitorTrigger(ctx context.Context, tx pgx.Tx, object, kind, detail string, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO monitor_alerts(id,object_id,kind,state,detail,triggered_at) VALUES($1,$2,$3,'active',$4,$5) ON CONFLICT(object_id,kind) WHERE state='active' DO NOTHING`, newID(), object, kind, detail, now)
	return err
}
func monitorResolve(ctx context.Context, tx pgx.Tx, object, kind, reason string, now time.Time) error {
	if _, err := tx.Exec(ctx, `DELETE FROM monitor_conditions WHERE object_id=$1 AND kind=$2`, object, kind); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE monitor_alerts SET state='resolved',resolved_at=$3,resolve_reason=$4 WHERE object_id=$1 AND kind=$2 AND state='active'`, object, kind, now, reason)
	return err
}
func monitorCondition(ctx context.Context, tx pgx.Tx, object, kind, detail string, duration time.Duration, now time.Time) error {
	var since time.Time
	if err := tx.QueryRow(ctx, `INSERT INTO monitor_conditions(object_id,kind,since) VALUES($1,$2,$3) ON CONFLICT(object_id,kind) DO UPDATE SET since=monitor_conditions.since RETURNING since`, object, kind, now).Scan(&since); err != nil {
		return err
	}
	if now.Sub(since) < duration {
		return nil
	}
	return monitorTrigger(ctx, tx, object, kind, detail, now)
}

// MaintainMonitoring runs independently of browser activity. No game calls.
func (s *Store) MaintainMonitoring(ctx context.Context) error {
	return s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		// Serialize maintenance across service instances without blocking them.
		var acquired bool
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(614238428)`).Scan(&acquired); err != nil {
			return err
		}
		if !acquired {
			return nil
		}
		type state struct {
			id, stored, capacity, error string
			settings                    plugins.Settings
			seen, observed, reported    *time.Time
			revoked                     bool
		}
		rows, err := tx.Query(ctx, `SELECT o.id,p.settings,c.last_seen,c.revoked,coalesce(l.stored_eu::text,''),coalesce(l.capacity_eu::text,''),l.observed_at,coalesce(l.reported_at,o.created_at),coalesce(l.error,'') FROM plugin_objects o JOIN plugin_installations p USING(server_group,plugin_id) JOIN monitor_collectors c ON c.id=o.collector_id LEFT JOIN monitor_latest l ON l.object_id=o.id WHERE o.enabled AND p.enabled AND (p.settings->>'alerts')::boolean ORDER BY o.alerts_checked_at,o.id LIMIT 64 FOR SHARE OF o,p`)
		if err != nil {
			return err
		}
		states := []state{}
		for rows.Next() {
			var v state
			var raw []byte
			if err = rows.Scan(&v.id, &raw, &v.seen, &v.revoked, &v.stored, &v.capacity, &v.observed, &v.reported, &v.error); err != nil {
				rows.Close()
				return err
			}
			if err = json.Unmarshal(raw, &v.settings); err != nil {
				rows.Close()
				return err
			}
			states = append(states, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		var now time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		for _, v := range states {
			if _, err = tx.Exec(ctx, `UPDATE plugin_objects SET alerts_checked_at=$2 WHERE id=$1`, v.id, now); err != nil {
				return err
			}
			stale := time.Duration(v.settings.OfflineSeconds) * time.Second
			offline := v.revoked || v.seen != nil && now.Sub(*v.seen) > stale
			if offline {
				if _, err = tx.Exec(ctx, `DELETE FROM monitor_conditions WHERE object_id=$1 AND kind='low_energy'`, v.id); err != nil {
					return err
				}
				continue
			}
			bad := v.error != "" || v.observed != nil && now.Sub(*v.observed) > stale || v.observed == nil && v.reported != nil && now.Sub(*v.reported) > stale
			if bad {
				if _, err = tx.Exec(ctx, `DELETE FROM monitor_conditions WHERE object_id=$1 AND kind='low_energy'`, v.id); err != nil {
					return err
				}
				continue
			}
			if v.observed == nil {
				continue
			}
			a, e := plugins.Energy(v.stored)
			if e != nil {
				return e
			}
			b, e := plugins.Energy(v.capacity)
			if e != nil {
				return e
			}
			low := a.Mul(a, big.NewInt(100)).Cmp(b.Mul(b, big.NewInt(int64(v.settings.LowPercent)))) < 0
			if low {
				detail := fmt.Sprintf("电量 %s%%，低于 %d%%，持续 %d 秒后告警；回升至 %d%%解除。", plugins.Percent(v.stored, v.capacity), v.settings.LowPercent, v.settings.LowDurationSeconds, v.settings.RecoverPercent)
				if err = monitorCondition(ctx, tx, v.id, "low_energy", detail, time.Duration(v.settings.LowDurationSeconds)*time.Second, now); err != nil {
					return err
				}
			} else {
				a, _ = plugins.Energy(v.stored)
				b, _ = plugins.Energy(v.capacity)
				recovered := a.Mul(a, big.NewInt(100)).Cmp(b.Mul(b, big.NewInt(int64(v.settings.RecoverPercent)))) >= 0
				if recovered {
					if err = monitorResolve(ctx, tx, v.id, "low_energy", "energy_recovered", now); err != nil {
						return err
					}
				} else {
					if _, err = tx.Exec(ctx, `DELETE FROM monitor_conditions WHERE object_id=$1 AND kind='low_energy' AND NOT EXISTS(SELECT 1 FROM monitor_alerts WHERE object_id=$1 AND kind='low_energy' AND state='active')`, v.id); err != nil {
						return err
					}
				}
			}
			if v.settings.FallingMinutes > 0 {
				// Require a continuous epoch and coverage of the configured window.
				var falling bool
				err = tx.QueryRow(ctx, `WITH x AS (SELECT r.*,lag(r.stored_eu) OVER(ORDER BY r.observed_at,r.id) prev FROM monitor_energy_records r JOIN monitor_latest l ON l.object_id=r.object_id AND l.epoch=r.epoch WHERE r.object_id=$1 AND r.observed_at>=$2::timestamptz-($3::int*interval '1 minute')-interval '30 seconds') SELECT coalesce(count(*)>=3 AND min(observed_at)<=$2::timestamptz-($3::int*interval '1 minute') AND bool_and(prev IS NULL OR stored_eu<=prev) AND min(stored_eu)<max(stored_eu),false) FROM x`, v.id, now, v.settings.FallingMinutes).Scan(&falling)
				if err != nil {
					return err
				}
				if falling {
					if err = monitorTrigger(ctx, tx, v.id, "falling", "电容库储能在配置时间内持续下降。净变化包含用电、自放电和无线转移。", now); err != nil {
						return err
					}
				} else if err = monitorResolve(ctx, tx, v.id, "falling", "trend_changed", now); err != nil {
					return err
				}
			} else if err = monitorResolve(ctx, tx, v.id, "falling", "rule_disabled", now); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) MonitorAlerts(ctx context.Context, member, group string) ([]MonitorAlert, error) {
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT a.id,a.object_id,o.name,a.kind,a.state,a.detail,a.triggered_at,a.resolved_at,a.resolve_reason,a.acknowledged_at,coalesce(m.name,'') FROM monitor_alerts a JOIN plugin_objects o ON o.id=a.object_id LEFT JOIN members m ON m.id=a.acknowledged_by WHERE o.server_group=$1 ORDER BY (a.state='active') DESC,a.triggered_at DESC,a.id LIMIT 200`, group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MonitorAlert{}
	for rows.Next() {
		var v MonitorAlert
		if err = rows.Scan(&v.ID, &v.ObjectID, &v.ObjectName, &v.Kind, &v.State, &v.Detail, &v.TriggeredAt, &v.ResolvedAt, &v.ResolveReason, &v.AcknowledgedAt, &v.AcknowledgedBy); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) AcknowledgeMonitorAlert(ctx context.Context, member, group, id string) error {
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return err
	}
	return s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE monitor_alerts a SET acknowledged_at=coalesce(a.acknowledged_at,now()),acknowledged_by=coalesce(a.acknowledged_by,$2),state=CASE WHEN a.kind='capacity_changed' THEN 'resolved' ELSE a.state END,resolved_at=CASE WHEN a.kind='capacity_changed' THEN coalesce(a.resolved_at,now()) ELSE a.resolved_at END,resolve_reason=CASE WHEN a.kind='capacity_changed' THEN 'acknowledged' ELSE a.resolve_reason END FROM plugin_objects o WHERE a.id=$1 AND o.id=a.object_id AND o.server_group=$3`, id, member, group)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		return monitorAudit(ctx, tx, group, member, "alert_acknowledged", id)
	})
}
