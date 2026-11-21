package store

import (
	"context"
	"time"
)

type MonitorEnergyPoint struct {
	At       time.Time `json:"at"`
	Stored   string    `json:"stored_eu"`
	Capacity string    `json:"capacity_eu"`
	Min      string    `json:"min_eu"`
	Max      string    `json:"max_eu"`
	Epoch    int64     `json:"epoch"`
	Samples  int64     `json:"samples"`
}
type MonitorHistory struct {
	Points          []MonitorEnergyPoint `json:"points"`
	Resolution      string               `json:"resolution"`
	StartedAt       *time.Time           `json:"started_at,omitempty"`
	RawRetainedFrom *time.Time           `json:"raw_retained_from,omitempty"`
	Truncated       bool                 `json:"truncated"`
}

func (s *Store) MonitorHistory(ctx context.Context, member, group, object string, from, to time.Time) (MonitorHistory, error) {
	h := MonitorHistory{Points: []MonitorEnergyPoint{}, Resolution: "raw"}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return h, err
	}
	if !monitorID.MatchString(object) || !from.Before(to) || to.Sub(from) > 3660*24*time.Hour {
		return h, ErrInvalid
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_objects WHERE id=$1 AND server_group=$2)`, object, group).Scan(&exists); err != nil {
		return h, err
	}
	if !exists {
		return h, ErrNotFound
	}
	if err := s.pool.QueryRow(ctx, `SELECT min(first_at),(SELECT min(observed_at) FROM monitor_energy_records WHERE object_id=$1) FROM monitor_energy_rollups WHERE object_id=$1`, object).Scan(&h.StartedAt, &h.RawRetainedFrom); err != nil {
		return h, err
	}
	allRawRetained := h.RawRetainedFrom != nil && h.StartedAt != nil && !h.RawRetainedFrom.After(*h.StartedAt)
	useRaw := to.Sub(from) <= 4*time.Hour && (allRawRetained || h.RawRetainedFrom == nil || !from.Before(*h.RawRetainedFrom))
	statement := `SELECT observed_at,stored_eu::text,capacity_eu::text,stored_eu::text,stored_eu::text,epoch,1::bigint FROM monitor_energy_records WHERE object_id=$1 AND observed_at>=$2 AND observed_at<=$3 ORDER BY observed_at,id LIMIT 2001`
	args := []any{object, from, to}
	if !useRaw {
		h.Resolution = "hour"
		if to.Sub(from) > 45*24*time.Hour {
			h.Resolution = "day"
		}
		statement = `WITH points AS (
 SELECT last_at at,last_eu::text stored,capacity_eu::text capacity,min_eu::text minimum,max_eu::text maximum,epoch,samples FROM monitor_energy_rollups WHERE object_id=$1 AND granularity=$4 AND first_at>=$2 AND last_at<=$3
 UNION ALL
 SELECT r.observed_at,r.stored_eu::text,r.capacity_eu::text,r.stored_eu::text,r.stored_eu::text,r.epoch,1::bigint FROM monitor_energy_records r WHERE r.object_id=$1 AND r.observed_at>=$2 AND r.observed_at<=$3 AND NOT EXISTS(SELECT 1 FROM monitor_energy_rollups h WHERE h.object_id=r.object_id AND h.epoch=r.epoch AND h.granularity=$4 AND h.first_at>=$2 AND h.last_at<=$3 AND r.observed_at BETWEEN h.first_at AND h.last_at)
 ) SELECT at,stored,capacity,minimum,maximum,epoch,samples FROM points ORDER BY at,epoch LIMIT 2001`
		args = append(args, h.Resolution)
	}
	rows, err := s.pool.Query(ctx, statement, args...)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		var p MonitorEnergyPoint
		if err = rows.Scan(&p.At, &p.Stored, &p.Capacity, &p.Min, &p.Max, &p.Epoch, &p.Samples); err != nil {
			return h, err
		}
		if len(h.Points) == 2000 {
			h.Truncated = true
			break
		}
		h.Points = append(h.Points, p)
	}
	return h, rows.Err()
}

// Raw history is separately configurable; zero means retain every sample.
// Long-term hour/day rollups are never removed by this task.
func (s *Store) PruneMonitorHistory(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM monitor_energy_records WHERE id IN(SELECT r.id FROM monitor_energy_records r JOIN plugin_objects o ON o.id=r.object_id JOIN plugin_installations p USING(server_group,plugin_id) WHERE (p.settings->>'raw_retention_days')::int>0 AND r.observed_at<now()-((p.settings->>'raw_retention_days')::int)*interval '1 day' ORDER BY r.id LIMIT 5000)`)
	return err
}
