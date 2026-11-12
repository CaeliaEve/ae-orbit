package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

func markProductionJobChanged(tx pgx.Tx, networkID, jobID string) {
	if tracked, ok := tx.(*notifyingTx); ok {
		if tracked.productionJobs == nil {
			tracked.productionJobs = make(map[string]map[string]struct{})
		}
		if tracked.productionJobs[networkID] == nil {
			tracked.productionJobs[networkID] = make(map[string]struct{})
		}
		tracked.productionJobs[networkID][jobID] = struct{}{}
	}
}

// This only tracks identity; projection and job-row locks wait until precommit.
func markProductionOrderChanged(ctx context.Context, tx pgx.Tx, orderID string) error {
	var networkID, jobID string
	err := tx.QueryRow(ctx, `SELECT e.network_id,e.job_id FROM orders o JOIN production_job_entries e
		ON e.id=o.production_job_entry_id WHERE o.id=$1`, orderID).Scan(&networkID, &jobID)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	markProductionJobChanged(tx, networkID, jobID)
	return nil
}

func (s *Store) flushProductionJobs(ctx context.Context, tx *notifyingTx) error {
	if len(tx.productionJobs) == 0 {
		return nil
	}
	networks := make([]string, 0, len(tx.productionJobs))
	for networkID := range tx.productionJobs {
		networks = append(networks, networkID)
	}
	sort.Strings(networks)
	for _, networkID := range networks {
		// Writers already hold this lock; taking it here also protects future hooks.
		if _, err := tx.Exec(ctx, "SELECT id FROM networks WHERE id=$1 FOR UPDATE", networkID); err != nil {
			return err
		}
		ids := make([]string, 0, len(tx.productionJobs[networkID]))
		for jobID := range tx.productionJobs[networkID] {
			ids = append(ids, jobID)
		}
		sort.Strings(ids)
		for _, jobID := range ids {
			if err := s.flushProductionJob(ctx, tx, networkID, jobID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM production_job_changes WHERE network_id=$1 AND revision <=
			(SELECT jobs_revision-$2 FROM networks WHERE id=$1)`, networkID, productionJobChangeRetention); err != nil {
			return err
		}
		if err := s.event(ctx, tx, networkID, "jobs"); err != nil {
			return err
		}
	}
	tx.productionJobs = nil
	return nil
}

func (s *Store) flushProductionJob(ctx context.Context, tx pgx.Tx, networkID, jobID string) error {
	var control string
	if err := tx.QueryRow(ctx, "SELECT control FROM production_jobs WHERE network_id=$1 AND id=$2 FOR UPDATE", networkID, jobID).Scan(&control); err != nil {
		return notFound(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE production_job_entries e SET state=o.state,reason=o.reason,attempt_id=o.attempt_id,order_id=o.id,
		terminal_at=CASE WHEN o.state IN ('done','failed','canceled','expired') THEN coalesce(e.terminal_at,o.updated_at) ELSE NULL END
		FROM orders o WHERE e.job_id=$1 AND o.production_job_entry_id=e.id AND (e.order_id='' OR e.order_id=o.id)`, jobID); err != nil {
		return err
	}
	var counts protocol.ProductionJobCounts
	rows, err := tx.Query(ctx, `SELECT state,count(*) FROM production_job_entries WHERE job_id=$1 GROUP BY state`, jobID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var state string
		var count protocol.Quantity
		if err = rows.Scan(&state, &count); err != nil {
			rows.Close()
			return err
		}
		switch state {
		case "pending":
			counts.Pending = count
		case "rejected":
			counts.Rejected = count
		case "queued":
			counts.Queued = count
		case "assigned":
			counts.Assigned = count
		case "computing":
			counts.Computing = count
		case "running":
			counts.Running = count
		case "needs_review":
			counts.NeedsReview = count
		case "done":
			counts.Done = count
		case "failed":
			counts.Failed = count
		case "canceled":
			counts.Canceled = count
		case "expired":
			counts.Expired = count
		default:
			rows.Close()
			return fmt.Errorf("unknown production entry state %q", state)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	settled, result := productionJobSettlement(counts, control)
	encoded, err := json.Marshal(counts)
	if err != nil {
		return err
	}
	summary, err := scanProductionJob(tx.QueryRow(ctx, `UPDATE production_jobs j SET counts=$3,version=version+1,updated_at=now(),
		result=$4,settled_at=CASE WHEN $5 THEN coalesce(settled_at,now()) ELSE NULL END
		WHERE j.network_id=$1 AND j.id=$2 RETURNING `+productionJobColumns, networkID, jobID, encoded, result, settled))
	if err != nil {
		return err
	}
	snapshot, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH revision AS (UPDATE networks SET jobs_revision=jobs_revision+1 WHERE id=$1 RETURNING jobs_revision)
		INSERT INTO production_job_changes(network_id,revision,job_id,version,summary)
		SELECT $1,jobs_revision,$2,$3,$4 FROM revision`, networkID, jobID, int64(summary.Version), snapshot)
	return err
}

func productionJobSettlement(counts protocol.ProductionJobCounts, control string) (bool, string) {
	if counts.Pending+counts.Queued+counts.Assigned+counts.Computing+counts.Running+counts.NeedsReview > 0 {
		return false, ""
	}
	if counts.Rejected+counts.Failed+counts.Expired > 0 || (counts.Canceled > 0 && control != "stopped") {
		return true, "finished_with_issues"
	}
	if counts.Canceled > 0 {
		return true, "canceled"
	}
	return true, "completed"
}
