package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

func (s *Store) productionJobRead(ctx context.Context, userID, networkID string, read func(pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = productionJobAccess(ctx, tx, userID, networkID, false); err != nil {
		return err
	}
	if err = read(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) productionJobLimits() map[string]int {
	return map[string]int{
		"entries_per_job": 500, "manifest_bytes": 256 * 1024,
		"unsettled_jobs_per_network": 64, "unsettled_jobs_per_owner": 16,
		"detailed_jobs_per_network": 512, "creation_receipts_per_network": 16384,
		"source_entry_receipts_per_network": 250000, "outstanding_entries_per_job": 8,
		"queued_job_orders_per_network": min(32, s.options.MaxQueuedOrders),
		"entry_selections_per_poll":     16, "job_candidates_per_poll": 64,
		"retained_changes_per_network": 2048, "changes_per_response": 100,
		"control_operations_per_job": 1024, "history_per_response": 100,
		"settled_detail_retention_days": 30,
		"queued_orders_per_network":     s.options.MaxQueuedOrders,
		"active_window":                 s.options.ActiveWindow, "group_planning": s.options.GroupPlanning,
		"response_bytes": s.options.Limits.ResponseBytes,
	}
}

func (s *Store) ProductionJobs(ctx context.Context, userID, networkID string) (protocol.ProductionJobSnapshot, error) {
	page := protocol.ProductionJobSnapshot{Jobs: []protocol.ProductionJobSummary{}, Limits: s.productionJobLimits()}
	err := s.productionJobRead(ctx, userID, networkID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT jobs_revision,
			(SELECT count(*) FROM production_jobs WHERE network_id=$1 AND settled_at IS NULL),
			(SELECT count(*) FROM production_jobs WHERE network_id=$1 AND settled_at IS NOT NULL),
			(SELECT count(*) FROM production_jobs WHERE network_id=$1),
			(SELECT count(*) FROM production_job_receipts WHERE network_id=$1),
			(SELECT count(*) FROM production_job_entry_receipts WHERE network_id=$1)
			FROM networks WHERE id=$1`, networkID).Scan(&page.Watermark, &page.UnsettledCount, &page.SettledCount, &page.DetailedCount, &page.ReceiptCount, &page.EntryReceiptCount); err != nil {
			return notFound(err)
		}
		rows, err := tx.Query(ctx, "SELECT "+productionJobColumns+" FROM production_jobs j WHERE j.network_id=$1 AND j.settled_at IS NULL ORDER BY j.created_at,j.id", networkID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			job, err := scanProductionJob(rows)
			if err != nil {
				return err
			}
			page.Jobs = append(page.Jobs, job)
		}
		return rows.Err()
	})
	return page, err
}

type productionJobHistoryCursor struct {
	Network string    `json:"network"`
	Settled time.Time `json:"settled"`
	ID      string    `json:"id"`
}

func (s *Store) ProductionJobHistory(ctx context.Context, userID, networkID, cursor string, limit int) (protocol.ProductionJobHistoryPage, error) {
	page := protocol.ProductionJobHistoryPage{Jobs: []protocol.ProductionJobSummary{}}
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 100 {
		return page, ErrInvalid
	}
	var c productionJobHistoryCursor
	if cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(data) > 1024 || json.Unmarshal(data, &c) != nil || c.Network != networkID || c.ID == "" || c.Settled.IsZero() {
			return page, ErrInvalid
		}
	}
	err := s.productionJobRead(ctx, userID, networkID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "SELECT "+productionJobColumns+` FROM production_jobs j WHERE j.network_id=$1 AND j.settled_at IS NOT NULL
			AND ($2='' OR (j.settled_at,j.id)<($3,$2)) ORDER BY j.settled_at DESC,j.id DESC LIMIT $4`,
			networkID, c.ID, c.Settled, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			job, err := scanProductionJob(rows)
			if err != nil {
				return err
			}
			page.Jobs = append(page.Jobs, job)
		}
		return rows.Err()
	})
	if err != nil {
		return page, err
	}
	if len(page.Jobs) > limit {
		page.HasMore = true
		page.Jobs = page.Jobs[:limit]
		last := page.Jobs[len(page.Jobs)-1]
		data, _ := json.Marshal(productionJobHistoryCursor{networkID, *last.SettledAt, last.ID})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return page, nil
}

func (s *Store) ProductionJobChanges(ctx context.Context, userID, networkID string, after protocol.Quantity) (protocol.ProductionJobChangePage, error) {
	page := protocol.ProductionJobChangePage{Changes: []protocol.ProductionJobChange{}, NextAfter: after}
	if after < 0 {
		return page, ErrInvalid
	}
	err := s.productionJobRead(ctx, userID, networkID, func(tx pgx.Tx) error {
		var first protocol.Quantity
		if err := tx.QueryRow(ctx, `SELECT n.jobs_revision,coalesce((SELECT min(revision) FROM production_job_changes WHERE network_id=n.id),n.jobs_revision+1)
			FROM networks n WHERE n.id=$1`, networkID).Scan(&page.Watermark, &first); err != nil {
			return notFound(err)
		}
		if after > page.Watermark || after < first-1 {
			page.Reset = true
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT revision,job_id,version,summary,removed FROM production_job_changes WHERE network_id=$1 AND revision>$2 ORDER BY revision LIMIT 101`, networkID, int64(after))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var change protocol.ProductionJobChange
			var summary []byte
			if err = rows.Scan(&change.Revision, &change.JobID, &change.Version, &summary, &change.Removed); err != nil {
				return err
			}
			if summary != nil {
				if err = json.Unmarshal(summary, &change.Summary); err != nil {
					return err
				}
			}
			page.Changes = append(page.Changes, change)
		}
		return rows.Err()
	})
	if len(page.Changes) > 100 {
		page.HasMore = true
		page.Changes = page.Changes[:100]
	}
	if len(page.Changes) > 0 {
		page.NextAfter = page.Changes[len(page.Changes)-1].Revision
	}
	return page, err
}
