package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

const (
	maxProductionJobsNetwork     = 64
	maxProductionJobsOwner       = 16
	maxProductionJobDetails      = 512
	maxProductionJobReceipts     = 16384
	maxProductionEntryReceipts   = 250000
	productionJobChangeRetention = 2048
)

var ErrEntryAlreadyAccepted = errors.New("entry_already_accepted")

type ProductionJobEntryConflictError struct {
	JobIDs []string `json:"job_ids"`
}

func (e *ProductionJobEntryConflictError) Error() string { return ErrEntryAlreadyAccepted.Error() }
func (e *ProductionJobEntryConflictError) Unwrap() error { return ErrEntryAlreadyAccepted }

const productionJobColumns = `j.id,j.network_id,j.member_id,j.name,j.manifest_digest,j.control,j.reason,j.result,j.version,j.control_version,j.total,j.counts,j.created_at,j.updated_at,j.settled_at`

func scanProductionJob(row scanner) (protocol.ProductionJobSummary, error) {
	var result protocol.ProductionJobSummary
	var counts []byte
	err := row.Scan(&result.ID, &result.NetworkID, &result.OwnerID, &result.Name, &result.ManifestDigest, &result.Control,
		&result.Reason, &result.Result, &result.Version, &result.ControlVersion, &result.Total, &counts,
		&result.CreatedAt, &result.UpdatedAt, &result.SettledAt)
	if err != nil {
		return result, notFound(err)
	}
	err = json.Unmarshal(counts, &result.Counts)
	return result, err
}

// Call with the network lock held, and before acquiring any job row locks.
func lockProductionJobMember(ctx context.Context, tx pgx.Tx, memberID string) error {
	var id string
	return notFound(tx.QueryRow(ctx, "SELECT id FROM members WHERE id=$1 FOR SHARE", memberID).Scan(&id))
}

func productionJobAccess(ctx context.Context, tx pgx.Tx, memberID, networkID string, craft bool) (bool, error) {
	var admin, allowed bool
	err := tx.QueryRow(ctx, `SELECT coalesce(bool_or(m.is_admin),false),
		coalesce(bool_or(NOT m.revoked AND (m.is_admin OR (g.member_id IS NOT NULL AND (NOT $3 OR g.can_craft)))),false)
		FROM members m JOIN networks n ON n.id=$2
		LEFT JOIN network_grants g ON g.member_id=m.id AND g.network_id=n.id WHERE m.id=$1`,
		memberID, networkID, craft).Scan(&admin, &allowed)
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, ErrForbidden
	}
	return admin, nil
}

func productionJobDetail(ctx context.Context, tx pgx.Tx, networkID, jobID string) (protocol.ProductionJobDetail, error) {
	result := protocol.ProductionJobDetail{Entries: []protocol.ProductionJobEntry{}}
	var err error
	result.Summary, err = scanProductionJob(tx.QueryRow(ctx, "SELECT "+productionJobColumns+" FROM production_jobs j WHERE j.network_id=$1 AND j.id=$2", networkID, jobID))
	if err != nil {
		return result, err
	}
	rows, err := tx.Query(ctx, `SELECT id,entry_key,ordinal,requested_item_id,item_id,quantity,state,reason,order_id,attempt_id,terminal_at
		FROM production_job_entries WHERE job_id=$1 ORDER BY ordinal LIMIT 500`, jobID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry protocol.ProductionJobEntry
		if err = rows.Scan(&entry.ID, &entry.EntryKey, &entry.Ordinal, &entry.RequestedItemID, &entry.ItemID,
			&entry.Quantity, &entry.State, &entry.Reason, &entry.OrderID, &entry.AttemptID, &entry.TerminalAt); err != nil {
			return result, err
		}
		result.Entries = append(result.Entries, entry)
	}
	return result, rows.Err()
}

func productionJobAcceptance(ctx context.Context, tx pgx.Tx, memberID, networkID, key string) (protocol.ProductionJobAcceptance, error) {
	var result protocol.ProductionJobAcceptance
	err := tx.QueryRow(ctx, `SELECT job_id,manifest_digest FROM production_job_receipts
		WHERE member_id=$1 AND network_id=$2 AND request_key=$3`, memberID, networkID, key).Scan(&result.Receipt.JobID, &result.Receipt.ManifestDigest)
	if err != nil {
		return result, notFound(err)
	}
	detail, err := productionJobDetail(ctx, tx, networkID, result.Receipt.JobID)
	if errors.Is(err, ErrNotFound) {
		result.Receipt.Archived = true
	} else if err != nil {
		return result, err
	} else {
		result.Job = &detail
	}
	result.Replayed = true
	return result, nil
}

func (s *Store) CreateProductionJob(ctx context.Context, userID, networkID string, input protocol.ProductionJobInput) (protocol.ProductionJobAcceptance, error) {
	var result protocol.ProductionJobAcceptance
	in, err := input.Normalize()
	if err != nil {
		return result, errors.Join(ErrInvalid, err)
	}
	digest := in.ManifestDigest()
	err = s.transaction(ctx, func(tx pgx.Tx) error {
		var capsJSON []byte
		if err := tx.QueryRow(ctx, "SELECT capabilities FROM networks WHERE id=$1 FOR UPDATE", networkID).Scan(&capsJSON); err != nil {
			return notFound(err)
		}
		if err := lockProductionJobMember(ctx, tx, userID); err != nil {
			return err
		}
		if _, err := productionJobAccess(ctx, tx, userID, networkID, true); err != nil {
			return err
		}
		existing, err := productionJobAcceptance(ctx, tx, userID, networkID, in.RequestKey)
		if err == nil {
			if existing.Receipt.ManifestDigest != digest {
				return ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		var receipts, entryReceipts, details, liveNetwork, liveOwner int
		if err = tx.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM production_job_receipts WHERE network_id=$1),
			(SELECT count(*) FROM production_job_entry_receipts WHERE network_id=$1),
			count(*),count(*) FILTER(WHERE settled_at IS NULL),
			count(*) FILTER(WHERE settled_at IS NULL AND member_id=$2)
			FROM production_jobs WHERE network_id=$1`, networkID, userID).Scan(&receipts, &entryReceipts, &details, &liveNetwork, &liveOwner); err != nil {
			return err
		}
		keys := make([]string, len(in.Entries))
		for i, entry := range in.Entries {
			keys[i] = entry.EntryKey
		}
		// Legacy orders cannot supply job identities; leave these collisions ambiguous.
		var legacySource bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders
			WHERE member_id=$1 AND network_id=$2 AND idempotency_key=ANY($3::text[]))`, userID, networkID, keys).Scan(&legacySource); err != nil {
			return err
		}
		if legacySource {
			return ErrConflict
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT job_id FROM production_job_entry_receipts
			WHERE member_id=$1 AND network_id=$2 AND entry_key=ANY($3::text[]) ORDER BY job_id`, userID, networkID, keys)
		if err != nil {
			return err
		}
		conflict := &ProductionJobEntryConflictError{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			conflict.JobIDs = append(conflict.JobIDs, id)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		if len(conflict.JobIDs) > 0 {
			return conflict
		}
		if receipts >= maxProductionJobReceipts || entryReceipts+len(in.Entries) > maxProductionEntryReceipts ||
			details >= maxProductionJobDetails || liveNetwork >= maxProductionJobsNetwork || liveOwner >= maxProductionJobsOwner {
			return ErrCapacity
		}
		var caps protocol.Capabilities
		if err = json.Unmarshal(capsJSON, &caps); err != nil {
			return err
		}
		if !caps.Craft || !caps.ExactQuantities || caps.MaxCraftQuantity < 1 {
			return ErrCapability
		}
		canonicalIDs := make([]protocol.Quantity, len(in.Entries))
		for i, entry := range in.Entries {
			canonical, err := canonicalItemID(ctx, tx, networkID, entry.ItemID)
			if err != nil {
				return err
			}
			var eligible bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM inventory i JOIN item_definitions d
				ON d.network_id=i.network_id AND d.id=i.item_id WHERE i.network_id=$1 AND i.item_id=$2 AND i.craftable AND d.exact)`,
				networkID, int64(canonical)).Scan(&eligible); err != nil {
				return err
			}
			if !eligible || entry.Quantity > caps.MaxCraftQuantity {
				return ErrCapability
			}
			canonicalIDs[i] = canonical
		}
		if _, err = tx.Exec(ctx, `INSERT INTO production_job_authority_epochs(member_id,network_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, userID, networkID); err != nil {
			return err
		}
		jobID := newID()
		if _, err = tx.Exec(ctx, `INSERT INTO production_jobs(id,network_id,member_id,name,manifest_digest,ttl_seconds,owner_authority_epoch,total)
			SELECT $1,$2,$3,$4,$5,$6,epoch,$7 FROM production_job_authority_epochs WHERE member_id=$3 AND network_id=$2`,
			jobID, networkID, userID, in.Name, digest, in.TTLSeconds, len(in.Entries)); err != nil {
			return err
		}
		batch := &pgx.Batch{}
		for i, entry := range in.Entries {
			entryID := newID()
			batch.Queue(`INSERT INTO production_job_entries(id,job_id,network_id,entry_key,ordinal,requested_item_id,item_id,quantity)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, entryID, jobID, networkID, entry.EntryKey, i, int64(entry.ItemID), int64(canonicalIDs[i]), int64(entry.Quantity))
			batch.Queue(`INSERT INTO production_job_entry_receipts(member_id,network_id,entry_key,job_id,entry_id) VALUES($1,$2,$3,$4,$5)`, userID, networkID, entry.EntryKey, jobID, entryID)
		}
		if err = tx.SendBatch(ctx, batch).Close(); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO production_job_receipts(member_id,network_id,request_key,manifest_digest,job_id) VALUES($1,$2,$3,$4,$5)`, userID, networkID, in.RequestKey, digest, jobID); err != nil {
			return err
		}
		markProductionJobChanged(tx, networkID, jobID)
		if err = s.flushProductionJobs(ctx, tx.(*notifyingTx)); err != nil {
			return err
		}
		result, err = productionJobAcceptance(ctx, tx, userID, networkID, in.RequestKey)
		result.Replayed = false
		return err
	})
	return result, err
}

func (s *Store) ProductionJobByKey(ctx context.Context, userID, networkID, ownerID, key string) (protocol.ProductionJobAcceptance, error) {
	var result protocol.ProductionJobAcceptance
	if !protocol.ProductionJobKeyValid(key) {
		return result, ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(context.Background())
	admin, err := productionJobAccess(ctx, tx, userID, networkID, false)
	if err != nil {
		return result, err
	}
	if ownerID == "" {
		ownerID = userID
	}
	if ownerID != userID && !admin {
		return result, ErrForbidden
	}
	result, err = productionJobAcceptance(ctx, tx, ownerID, networkID, key)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (s *Store) ProductionJob(ctx context.Context, userID, networkID, jobID string) (protocol.ProductionJobDetail, error) {
	var result protocol.ProductionJobDetail
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(context.Background())
	if _, err = productionJobAccess(ctx, tx, userID, networkID, false); err != nil {
		return result, err
	}
	result, err = productionJobDetail(ctx, tx, networkID, jobID)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
