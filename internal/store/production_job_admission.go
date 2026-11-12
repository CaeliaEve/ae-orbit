package store

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

// The caller already holds the network lock. Lock every owner before any job.
func (s *Store) fenceProductionJobs(ctx context.Context, tx pgx.Tx, networkID string) error {
	rows, err := tx.Query(ctx, `SELECT DISTINCT member_id FROM production_jobs WHERE network_id=$1 AND control='active' AND settled_at IS NULL ORDER BY member_id`, networkID)
	if err != nil {
		return err
	}
	var owners []string
	for rows.Next() {
		var owner string
		if err = rows.Scan(&owner); err != nil {
			rows.Close()
			return err
		}
		owners = append(owners, owner)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	sort.Strings(owners)
	for _, owner := range owners {
		if err = lockProductionJobMember(ctx, tx, owner); err != nil {
			return err
		}
	}
	for _, owner := range owners {
		_, accessErr := productionJobAccess(ctx, tx, owner, networkID, true)
		if accessErr != nil && !errors.Is(accessErr, ErrForbidden) {
			return accessErr
		}
		rows, err = tx.Query(ctx, `UPDATE production_jobs j SET control='paused',reason='owner_permission_lost',control_version=control_version+1
			WHERE j.network_id=$1 AND j.member_id=$2 AND j.control='active' AND j.settled_at IS NULL
			AND ($3 OR j.owner_authority_epoch<>(SELECT epoch FROM production_job_authority_epochs WHERE member_id=$2 AND network_id=$1)) RETURNING id`, networkID, owner, accessErr != nil)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			markProductionJobChanged(tx, networkID, id)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) admitProductionJobs(ctx context.Context, tx pgx.Tx, networkID string, caps protocol.Capabilities) error {
	if err := s.fenceProductionJobs(ctx, tx, networkID); err != nil {
		return err
	}
	// A network-wide capability loss blocks, rather than rejects, saved intent.
	if !caps.Craft || !caps.ExactQuantities || caps.MaxCraftQuantity < 1 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT j.id,j.member_id,j.created_at,j.ttl_seconds
		FROM production_jobs j JOIN networks n ON n.id=j.network_id
		WHERE j.network_id=$1 AND j.control='active' AND j.settled_at IS NULL
		ORDER BY CASE WHEN n.jobs_admission_cursor_created_at IS NULL OR (j.created_at,j.id)>(n.jobs_admission_cursor_created_at,n.jobs_admission_cursor_id) THEN 0 ELSE 1 END,j.created_at,j.id LIMIT 64`, networkID)
	if err != nil {
		return err
	}
	type candidate struct {
		id, owner string
		created   time.Time
		ttl       int
	}
	var jobs []candidate
	for rows.Next() {
		var j candidate
		if err = rows.Scan(&j.id, &j.owner, &j.created, &j.ttl); err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if len(jobs) == 0 {
		return nil
	}
	// The ordered candidate list wraps the persistent cursor once, never revisiting a job.
	selections := 0
	for examined := 0; examined < len(jobs) && examined < 64 && selections < 16; examined++ {
		var queued, linked int
		if err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE production_job_entry_id IS NOT NULL)
			FROM orders WHERE network_id=$1 AND state='queued'`, networkID).Scan(&queued, &linked); err != nil {
			return err
		}
		if queued >= s.options.MaxQueuedOrders || linked >= min(32, s.options.MaxQueuedOrders) {
			break
		}
		j := jobs[examined]
		var entryID string
		var item, quantity protocol.Quantity
		err = tx.QueryRow(ctx, `SELECT id,item_id,quantity FROM production_job_entries WHERE job_id=$1 AND state='pending' AND order_id='' ORDER BY ordinal LIMIT 1 FOR UPDATE`, j.id).Scan(&entryID, &item, &quantity)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		selections++
		if _, err = tx.Exec(ctx, `UPDATE networks SET jobs_admission_cursor_created_at=$2,jobs_admission_cursor_id=$3 WHERE id=$1`, networkID, j.created, j.id); err != nil {
			return err
		}
		// Consult current orders: reports/expiry in this poll have not projected yet.
		var outstanding int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM production_job_entries e LEFT JOIN orders o ON o.production_job_entry_id=e.id
			WHERE e.job_id=$1 AND coalesce(o.state,e.state) IN ('queued','assigned','computing','running','needs_review')`, j.id).Scan(&outstanding); err != nil {
			return err
		}
		if outstanding >= 8 {
			continue
		}
		canonical, identityErr := canonicalItemID(ctx, tx, networkID, item)
		if identityErr != nil && !errors.Is(identityErr, ErrNotFound) {
			return identityErr
		}
		var craftable, exact bool
		reason := ""
		if identityErr != nil {
			reason = "crafting identity missing"
		}
		if identityErr == nil {
			err = tx.QueryRow(ctx, `SELECT i.craftable,d.exact FROM inventory i JOIN item_definitions d ON d.network_id=i.network_id AND d.id=i.item_id WHERE i.network_id=$1 AND i.item_id=$2`, networkID, int64(canonical)).Scan(&craftable, &exact)
			if errors.Is(err, pgx.ErrNoRows) {
				reason = "crafting identity missing"
			} else if err != nil {
				return err
			} else if !exact {
				reason = "crafting identity is not exact"
			} else if !craftable {
				reason = "item is no longer craftable"
			}
		}
		if reason == "" && quantity > caps.MaxCraftQuantity {
			reason = "quantity exceeds current crafting limit"
		}
		if reason != "" {
			if _, err = tx.Exec(ctx, `UPDATE production_job_entries SET state='rejected',reason=$2,terminal_at=now() WHERE id=$1`, entryID, reason); err != nil {
				return err
			}
			markProductionJobChanged(tx, networkID, j.id)
			continue
		}
		order, err := s.enqueueOrder(ctx, tx, j.owner, networkID, protocol.OrderInput{ItemID: canonical, Quantity: quantity, TTLSeconds: j.ttl, IdempotencyKey: "production-order:" + newID(), BatchID: j.id}, entryID)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE production_job_entries SET state='queued',order_id=$2 WHERE id=$1 AND order_id=''`, entryID, order.ID); err != nil {
			return err
		}
	}
	return nil
}
