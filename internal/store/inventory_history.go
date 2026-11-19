package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Only the backend's minute recorder reads the existing snapshot. It never
// queues game reads and does not depend on a browser being open.
func (s *Store) RecordInventoryHistory(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT id FROM networks ORDER BY id`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		err = s.transaction(ctx, func(tx pgx.Tx) error {
			// Same lock order as ingestion; capture a consistent point in time.
			if _, err := tx.Exec(ctx, `SELECT id FROM networks WHERE id=$1 FOR UPDATE`, id); err != nil {
				return err
			}
			var epoch int64
			if err := tx.QueryRow(ctx, `SELECT inventory_history_epoch($1)`, id).Scan(&epoch); err != nil {
				return err
			}
			var initialized bool
			if err := tx.QueryRow(ctx, `SELECT initialized FROM inventory_history_epochs WHERE id=$1`, epoch).Scan(&initialized); err != nil {
				return err
			}
			if !initialized {
				if _, err := tx.Exec(ctx, `WITH seeded AS (
                INSERT INTO inventory_history_items(epoch_id,item_id,name,damage,label,search_text,quantity,observed_at)
                SELECT $2,d.id,d.name,d.damage,coalesce(c.label,d.label),coalesce(c.search_text,d.search_label),i.quantity,i.observed_at
                FROM inventory i JOIN item_definitions d ON d.network_id=i.network_id AND d.id=i.item_id
                LEFT JOIN network_inventory_profiles p ON p.network_id=d.network_id
                LEFT JOIN plain_catalog_candidates c ON c.network_id=d.network_id AND c.item_id=d.id
                WHERE i.network_id=$1 AND d.kind='item' AND d.exact AND d.nbt=''
                  AND (p.network_id IS NULL OR (c.item_id IS NOT NULL AND i.observed_at>=p.enabled_at))
                ON CONFLICT(epoch_id,item_id) DO NOTHING RETURNING item_id,quantity,observed_at
            ) INSERT INTO inventory_history_records(epoch_id,item_id,quantity,kind,observed_at,session_id)
            SELECT $2,item_id,quantity,'baseline',observed_at,'' FROM seeded`, id, epoch); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE inventory_history_epochs SET initialized=true WHERE id=$1`, epoch); err != nil {
					return err
				}
			}
			// Archive minute points and small rollups indefinitely. Long ranges
			// read hour/day rows, never every raw change or every minute.
			_, err := tx.Exec(ctx, `WITH facts AS (
                SELECT coalesce(sum(i.quantity),0) AS total,count(*) FILTER(WHERE i.quantity>0) AS entries,
                  count(i.item_id) AS checked,count(*) AS candidates,min(i.observed_at) AS oldest,max(i.observed_at) AS latest
                FROM item_definitions d LEFT JOIN network_inventory_profiles p ON p.network_id=d.network_id
                LEFT JOIN plain_catalog_candidates c ON c.network_id=d.network_id AND c.item_id=d.id
                LEFT JOIN inventory i ON i.network_id=d.network_id AND i.item_id=d.id AND (p.network_id IS NULL OR i.observed_at>=p.enabled_at)
                WHERE d.network_id=$1 AND d.kind='item' AND d.exact AND d.nbt=''
                  AND (p.network_id IS NULL AND i.item_id IS NOT NULL OR p.network_id IS NOT NULL AND c.item_id IS NOT NULL)
            ), minute AS (
                INSERT INTO inventory_history_totals(epoch_id,resolution,bucket,sampled_at,total_quantity,minimum,maximum,entries,checked,candidates,online,all_online,oldest_observation,last_observation)
                SELECT $2,60,to_timestamp(floor(extract(epoch FROM clock_timestamp())/60)*60),clock_timestamp(),f.total,f.total,f.total,f.entries,f.checked,f.candidates,
                  coalesce(n.last_seen>now()-$3::double precision*interval '1 second',false),coalesce(n.last_seen>now()-$3::double precision*interval '1 second',false),f.oldest,f.latest
                FROM facts f JOIN networks n ON n.id=$1
                ON CONFLICT(epoch_id,resolution,bucket) DO NOTHING RETURNING *
            ) INSERT INTO inventory_history_totals(epoch_id,resolution,bucket,sampled_at,total_quantity,minimum,maximum,entries,checked,candidates,online,all_online,oldest_observation,last_observation,samples)
            SELECT epoch_id,r,to_timestamp(floor(extract(epoch FROM sampled_at)/r)*r),sampled_at,total_quantity,minimum,maximum,entries,checked,candidates,online,all_online,oldest_observation,last_observation,1
            FROM minute CROSS JOIN unnest(ARRAY[3600,86400]) r
            ON CONFLICT(epoch_id,resolution,bucket) DO UPDATE SET
              sampled_at=excluded.sampled_at,total_quantity=excluded.total_quantity,minimum=least(inventory_history_totals.minimum,excluded.minimum),maximum=greatest(inventory_history_totals.maximum,excluded.maximum),
              entries=excluded.entries,checked=excluded.checked,candidates=excluded.candidates,online=excluded.online,all_online=inventory_history_totals.all_online AND excluded.all_online,
              oldest_observation=excluded.oldest_observation,last_observation=excluded.last_observation,samples=inventory_history_totals.samples+1`, id, epoch, s.options.OfflineAfter.Seconds())
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

type HistoryEpoch struct {
	ID        string    `json:"id"`
	Scope     string    `json:"scope"`
	StartedAt time.Time `json:"started_at"`
}
type HistoryPoint struct {
	At         time.Time  `json:"at"`
	Quantity   string     `json:"quantity"`
	Minimum    string     `json:"minimum"`
	Maximum    string     `json:"maximum"`
	Entries    string     `json:"entries"`
	Checked    string     `json:"checked"`
	Candidates string     `json:"candidates"`
	Online     bool       `json:"online"`
	AllOnline  bool       `json:"all_online"`
	Samples    string     `json:"samples"`
	Oldest     *time.Time `json:"oldest_observation,omitempty"`
	Latest     *time.Time `json:"last_observation,omitempty"`
}
type HistoryRecord struct {
	ID         string    `json:"id"`
	ItemID     string    `json:"item_id"`
	Name       string    `json:"name"`
	Damage     int       `json:"damage"`
	Label      string    `json:"label"`
	Quantity   string    `json:"quantity"`
	Previous   *string   `json:"previous_quantity"`
	Delta      *string   `json:"delta"`
	Kind       string    `json:"kind"`
	RecordedAt time.Time `json:"recorded_at"`
	ObservedAt time.Time `json:"observed_at"`
}
type HistoryQuery struct {
	Epoch           int64
	Item            int64
	From, To        time.Time
	Resolution      int
	Limit           int
	Before          int64
	Direction, Kind string
}
type HistorySeries struct {
	Epochs     []HistoryEpoch `json:"epochs"`
	Epoch      string         `json:"epoch"`
	StartedAt  *time.Time     `json:"started_at"`
	From       time.Time      `json:"from"`
	To         time.Time      `json:"to"`
	Resolution int            `json:"resolution"`
	Points     []HistoryPoint `json:"points"`
	Basis      string         `json:"basis"`
	Item       *HistoryItem   `json:"item,omitempty"`
	Watermark  string         `json:"watermark"`
}
type HistoryItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Damage   int    `json:"damage"`
	Label    string `json:"label"`
	Quantity string `json:"quantity"`
}
type HistoryPage struct {
	Records  []HistoryRecord `json:"records"`
	Next     string          `json:"next"`
	Snapshot string          `json:"snapshot"`
	HasMore  bool            `json:"has_more"`
}

func (s *Store) historyEpochs(ctx context.Context, user, network string, requested int64) ([]HistoryEpoch, int64, *time.Time, error) {
	if err := s.RequireAccess(ctx, user, network, false); err != nil {
		return nil, 0, nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,scope,started_at FROM inventory_history_epochs WHERE network_id=$1 ORDER BY id DESC`, network)
	if err != nil {
		return nil, 0, nil, err
	}
	defer rows.Close()
	epochs := []HistoryEpoch{}
	var selected int64
	var started *time.Time
	for rows.Next() {
		var e HistoryEpoch
		if err = rows.Scan(&e.ID, &e.Scope, &e.StartedAt); err != nil {
			return nil, 0, nil, err
		}
		epochs = append(epochs, e)
		n, _ := strconv.ParseInt(e.ID, 10, 64)
		if requested == n || requested == 0 && selected == 0 {
			selected = n
			t := e.StartedAt
			started = &t
		}
	}
	if err = rows.Err(); err != nil {
		return nil, 0, nil, err
	}
	if requested > 0 && selected == 0 {
		return nil, 0, nil, ErrNotFound
	}
	return epochs, selected, started, nil
}
func normalizeHistoryQuery(q HistoryQuery, started *time.Time) (HistoryQuery, error) {
	if q.Epoch < 0 || q.Item < 0 || q.Before < 0 || q.Limit < 0 || q.Limit > 200 {
		return q, ErrInvalid
	}
	if q.From.IsZero() {
		if started != nil {
			q.From = *started
		} else {
			q.From = time.Now().UTC().Add(-24 * time.Hour)
		}
	}
	if q.To.IsZero() {
		q.To = time.Now().UTC()
	}
	if !q.To.After(q.From) || q.To.Sub(q.From) > 200*365*24*time.Hour {
		return q, ErrInvalid
	}
	if q.Limit == 0 {
		q.Limit = 100
	}
	if q.Direction != "" && q.Direction != "increase" && q.Direction != "decrease" {
		return q, ErrInvalid
	}
	if q.Kind != "" && q.Kind != "change" && q.Kind != "first" && q.Kind != "baseline" {
		return q, ErrInvalid
	}
	return q, nil
}

func normalizeHistorySeriesQuery(q HistoryQuery, started *time.Time) (HistoryQuery, error) {
	q, err := normalizeHistoryQuery(q, started)
	if err != nil {
		return q, err
	}
	if q.Resolution == 0 {
		q.Resolution = 60
		for _, r := range []int{60, 3600, 86400} {
			q.Resolution = r
			if q.To.Sub(q.From).Seconds()/float64(r) <= 2000 {
				break
			}
		}
		// Keep the complete recording range usable after years of retention.
		if q.To.Sub(q.From).Seconds()/float64(q.Resolution) > 2000 {
			days := int((q.To.Sub(q.From) + 24*time.Hour - 1) / (24 * time.Hour))
			q.Resolution = ((days + 1999) / 2000) * 86400
		}
		return q, nil
	}
	if q.Resolution != 60 && q.Resolution != 3600 && q.Resolution != 86400 || q.To.Sub(q.From).Seconds()/float64(q.Resolution) > 2000 {
		return q, ErrInvalid
	}
	return q, nil
}

func (s *Store) InventoryHistorySeries(ctx context.Context, user, network string, q HistoryQuery) (HistorySeries, error) {
	out := HistorySeries{Epochs: []HistoryEpoch{}, Points: []HistoryPoint{}, Basis: "minute_snapshots"}
	epochs, e, started, err := s.historyEpochs(ctx, user, network, q.Epoch)
	if err != nil {
		return out, err
	}
	out.Epochs, out.Epoch, out.StartedAt = epochs, strconv.FormatInt(e, 10), started
	q, err = normalizeHistorySeriesQuery(q, started)
	if err != nil {
		return out, err
	}
	out.From, out.To, out.Resolution = q.From, q.To, q.Resolution
	if e == 0 {
		return out, nil
	}
	if err = s.pool.QueryRow(ctx, `SELECT coalesce(max(id),0)::text FROM inventory_history_records WHERE epoch_id=$1`, e).Scan(&out.Watermark); err != nil {
		return out, err
	}
	if q.Item > 0 {
		var item HistoryItem
		err = s.pool.QueryRow(ctx, `SELECT item_id::text,name,damage,label,quantity::text FROM inventory_history_items WHERE epoch_id=$1 AND item_id=$2`, e, q.Item).Scan(&item.ID, &item.Name, &item.Damage, &item.Label, &item.Quantity)
		if err != nil {
			return out, notFound(err)
		}
		out.Item = &item
		out.Basis = "last_known_quantity"
		rows, err := s.pool.Query(ctx, `WITH boundary AS (
            SELECT quantity,recorded_at FROM inventory_history_records WHERE epoch_id=$1 AND item_id=$2 AND recorded_at<$3 ORDER BY recorded_at DESC,id DESC LIMIT 1
        ), selected AS (
            SELECT DISTINCT ON(floor(extract(epoch FROM recorded_at)/$5)) quantity,recorded_at
            FROM inventory_history_records WHERE epoch_id=$1 AND item_id=$2 AND recorded_at>=$3 AND recorded_at<=$4
            ORDER BY floor(extract(epoch FROM recorded_at)/$5),recorded_at DESC,id DESC
        ) SELECT quantity::text,recorded_at FROM boundary UNION ALL SELECT quantity::text,recorded_at FROM selected ORDER BY recorded_at`, e, q.Item, q.From, q.To, q.Resolution)
		if err != nil {
			return out, err
		}
		defer rows.Close()
		for rows.Next() {
			var p HistoryPoint
			if err = rows.Scan(&p.Quantity, &p.At); err != nil {
				return out, err
			}
			if p.At.Before(q.From) {
				p.At = q.From
			}
			p.Minimum, p.Maximum, p.Online, p.AllOnline, p.Samples = p.Quantity, p.Quantity, true, true, "1"
			out.Points = append(out.Points, p)
		}
		if err = rows.Err(); err != nil {
			return out, err
		}
		if len(out.Points) > 0 && out.Points[len(out.Points)-1].At.Before(q.To) {
			p := out.Points[len(out.Points)-1]
			p.At = q.To
			out.Points = append(out.Points, p)
		}
		return out, nil
	}
	baseResolution := q.Resolution
	if baseResolution > 86400 {
		baseResolution = 86400
	}
	// Whole buckets use rollups; partial edge buckets use the retained minute
	// rows so a past end time cannot include later samples or lose that day.
	rows, err := s.pool.Query(ctx, `WITH eligible AS (
        SELECT *,floor(extract(epoch FROM bucket)/$2) AS slot FROM inventory_history_totals
        WHERE epoch_id=$1 AND sampled_at >= $3 AND sampled_at <= $4 AND (
          resolution=$5 AND bucket >= $3 AND bucket + $5 * interval '1 second' <= $4
          OR resolution=60 AND bucket>=to_timestamp(floor(extract(epoch FROM $3::timestamptz)/60)*60) AND bucket <= $4
            AND (bucket<to_timestamp(ceil(extract(epoch FROM $3::timestamptz)/$5)*$5)
              OR bucket>=to_timestamp(floor(extract(epoch FROM $4::timestamptz)/$5)*$5))
        )
      ), latest AS (
        SELECT DISTINCT ON(slot) * FROM eligible ORDER BY slot,sampled_at DESC
      ), summary AS (
        SELECT slot,min(minimum) AS minimum,max(maximum) AS maximum,bool_and(all_online) AS all_online,sum(samples) AS samples
        FROM eligible GROUP BY slot
      ) SELECT l.sampled_at,l.total_quantity::text,s.minimum::text,s.maximum::text,l.entries::text,l.checked::text,l.candidates::text,
        l.online,s.all_online,s.samples::text,l.oldest_observation,l.last_observation
      FROM latest l JOIN summary s USING(slot) ORDER BY slot LIMIT 2001`, e, q.Resolution, q.From, q.To, baseResolution)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p HistoryPoint
		if err = rows.Scan(&p.At, &p.Quantity, &p.Minimum, &p.Maximum, &p.Entries, &p.Checked, &p.Candidates, &p.Online, &p.AllOnline, &p.Samples, &p.Oldest, &p.Latest); err != nil {
			return out, err
		}
		if !p.At.After(q.To) {
			out.Points = append(out.Points, p)
		}
	}
	return out, rows.Err()
}

func (s *Store) InventoryHistoryRecords(ctx context.Context, user, network string, q HistoryQuery, snapshot int64) (HistoryPage, error) {
	out := HistoryPage{Records: []HistoryRecord{}}
	_, e, started, err := s.historyEpochs(ctx, user, network, q.Epoch)
	if err != nil {
		return out, err
	}
	// Detail pagination is independent of chart resolution.
	q, err = normalizeHistoryQuery(q, started)
	if err != nil {
		return out, err
	}
	if e == 0 {
		return out, nil
	}
	if snapshot < 0 {
		return out, ErrInvalid
	}
	if snapshot == 0 {
		if err = s.pool.QueryRow(ctx, `SELECT coalesce(max(id),0) FROM inventory_history_records WHERE epoch_id=$1`, e).Scan(&snapshot); err != nil {
			return out, err
		}
	}
	out.Snapshot = strconv.FormatInt(snapshot, 10)
	rows, err := s.pool.Query(ctx, `SELECT r.id::text,r.item_id::text,h.name,h.damage,h.label,r.quantity::text,r.previous_quantity::text,(r.quantity::numeric-r.previous_quantity)::text,r.kind,r.recorded_at,r.observed_at
      FROM inventory_history_records r JOIN inventory_history_items h ON h.epoch_id=r.epoch_id AND h.item_id=r.item_id
      WHERE r.epoch_id=$1 AND r.recorded_at>=$2 AND r.recorded_at<=$3 AND r.id<=$4 AND ($5::bigint=0 OR r.id<$5) AND ($6::bigint=0 OR r.item_id=$6)
        AND ($7='' OR r.kind=$7) AND ($8='' OR $8='increase' AND r.previous_quantity IS NOT NULL AND r.quantity>r.previous_quantity OR $8='decrease' AND r.previous_quantity IS NOT NULL AND r.quantity<r.previous_quantity)
      ORDER BY r.id DESC LIMIT $9`, e, q.From, q.To, snapshot, q.Before, q.Item, q.Kind, q.Direction, q.Limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r HistoryRecord
		if err = rows.Scan(&r.ID, &r.ItemID, &r.Name, &r.Damage, &r.Label, &r.Quantity, &r.Previous, &r.Delta, &r.Kind, &r.RecordedAt, &r.ObservedAt); err != nil {
			return out, err
		}
		out.Records = append(out.Records, r)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Records) > q.Limit {
		out.HasMore = true
		out.Records = out.Records[:q.Limit]
		out.Next = out.Records[len(out.Records)-1].ID
	}
	return out, nil
}

func (s *Store) InventoryHistoryItems(ctx context.Context, user, network string, epoch int64, search string) ([]HistoryItem, error) {
	out := []HistoryItem{}
	_, e, _, err := s.historyEpochs(ctx, user, network, epoch)
	if err != nil {
		return out, err
	}
	if len(search) > 128 || !utf8.ValidString(search) {
		return out, ErrInvalid
	}
	if e == 0 {
		return out, nil
	}
	args := []any{e}
	param := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	conditions, err := searchConditions(search, "d.label", "d.search_text", param)
	if err != nil {
		return out, err
	}
	query := `SELECT d.item_id::text,d.name,d.damage,d.label,d.quantity::text FROM inventory_history_items d WHERE d.epoch_id=$1`
	if len(conditions) > 0 {
		query += " AND " + strings.Join(conditions, " AND ")
	}
	query += ` ORDER BY (d.quantity>0) DESC,lower(d.label),d.item_id LIMIT 30`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var i HistoryItem
		if err = rows.Scan(&i.ID, &i.Name, &i.Damage, &i.Label, &i.Quantity); err != nil {
			return out, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
