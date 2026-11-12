package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ControlProductionJob(ctx context.Context, actor, networkID, jobID, action string, in protocol.ProductionJobControlInput) (protocol.ProductionJobSummary, error) {
	var result protocol.ProductionJobSummary
	if !protocol.ProductionJobKeyValid(in.OperationKey) || in.ExpectedControlVersion < 1 || (action != "pause" && action != "resume" && action != "stop") {
		return result, ErrInvalid
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT id FROM networks WHERE id=$1 FOR UPDATE", networkID); err != nil {
			return err
		}
		var owner string
		if err := tx.QueryRow(ctx, "SELECT member_id FROM production_jobs WHERE network_id=$1 AND id=$2", networkID, jobID).Scan(&owner); err != nil {
			return notFound(err)
		}
		members := []string{actor}
		if owner != actor {
			members = append(members, owner)
		}
		sort.Strings(members)
		for _, member := range members {
			if err := lockProductionJobMember(ctx, tx, member); err != nil {
				return err
			}
		}
		admin, err := productionJobAccess(ctx, tx, actor, networkID, true)
		if err != nil {
			return err
		}
		if actor != owner && !admin {
			return ErrForbidden
		}
		var savedActor, savedAction string
		var expected protocol.Quantity
		var encoded []byte
		err = tx.QueryRow(ctx, `SELECT member_id,action,expected_control_version,result FROM production_job_actions WHERE job_id=$1 AND operation_key=$2`, jobID, in.OperationKey).Scan(&savedActor, &savedAction, &expected, &encoded)
		if err == nil {
			if savedActor != actor || savedAction != action || expected != in.ExpectedControlVersion {
				return ErrConflict
			}
			return json.Unmarshal(encoded, &result)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		current, err := scanProductionJob(tx.QueryRow(ctx, "SELECT "+productionJobColumns+" FROM production_jobs j WHERE j.id=$1 FOR UPDATE", jobID))
		if err != nil {
			return err
		}
		if current.SettledAt != nil || current.ControlVersion != in.ExpectedControlVersion {
			return ErrConflict
		}
		next := map[string]string{"pause": "paused", "resume": "active", "stop": "stopped"}[action]
		if (action == "pause" && current.Control != "active") || (action == "resume" && current.Control != "paused") || (action == "stop" && current.Control == "stopped") {
			return ErrConflict
		}
		var operations int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM production_job_actions WHERE job_id=$1", jobID).Scan(&operations); err != nil {
			return err
		}
		if operations >= 1024 || (action != "stop" && operations >= 1023) {
			return ErrCapacity
		}
		if action == "resume" {
			if _, err = productionJobAccess(ctx, tx, owner, networkID, true); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE production_jobs SET control=$2,reason='',control_version=control_version+1,
			owner_authority_epoch=CASE WHEN $2='active' THEN (SELECT epoch FROM production_job_authority_epochs WHERE member_id=$3 AND network_id=$4) ELSE owner_authority_epoch END WHERE id=$1`, jobID, next, owner, networkID)
		if err != nil {
			return err
		}
		if action == "stop" {
			if _, err = tx.Exec(ctx, `UPDATE production_job_entries SET state='canceled',reason='job stopped before admission',terminal_at=now() WHERE job_id=$1 AND state='pending' AND order_id=''`, jobID); err != nil {
				return err
			}
			rows, err := tx.Query(ctx, `WITH changed AS (UPDATE orders o SET state='canceled',reason='job stopped before dispatch',updated_at=now() FROM production_job_entries e WHERE e.job_id=$1 AND o.production_job_entry_id=e.id AND o.state='queued' RETURNING o.id,o.state,o.reason,o.attempt_id)
				INSERT INTO order_events(order_id,state,reason,attempt_id) SELECT id,state,reason,attempt_id FROM changed RETURNING order_id`, jobID)
			if err != nil {
				return err
			}
			rows.Close()
			if err = rows.Err(); err != nil {
				return err
			}
			if err = s.event(ctx, tx, networkID, "orders"); err != nil {
				return err
			}
		}
		markProductionJobChanged(tx, networkID, jobID)
		if err = s.flushProductionJobs(ctx, tx.(*notifyingTx)); err != nil {
			return err
		}
		result, err = scanProductionJob(tx.QueryRow(ctx, "SELECT "+productionJobColumns+" FROM production_jobs j WHERE j.id=$1", jobID))
		if err != nil {
			return err
		}
		encoded, err = json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO production_job_actions(job_id,operation_key,member_id,action,expected_control_version,result) VALUES($1,$2,$3,$4,$5,$6)`, jobID, in.OperationKey, actor, action, int64(in.ExpectedControlVersion), encoded)
		return err
	})
	return result, err
}
