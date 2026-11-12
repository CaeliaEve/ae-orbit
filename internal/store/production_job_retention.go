package store

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"
)

// One maintenance pass archives at most 64 detailed jobs across all networks.
func (s *Store) archiveProductionJobs(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT network_id,id FROM production_jobs WHERE settled_at<now()-interval '30 days' ORDER BY settled_at,id LIMIT 64`)
	if err != nil {
		return err
	}
	jobs := map[string][]string{}
	for rows.Next() {
		var networkID, jobID string
		if err = rows.Scan(&networkID, &jobID); err != nil {
			rows.Close()
			return err
		}
		jobs[networkID] = append(jobs[networkID], jobID)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	networks := make([]string, 0, len(jobs))
	for networkID := range jobs {
		networks = append(networks, networkID)
	}
	sort.Strings(networks)
	for _, networkID := range networks {
		if err = s.transaction(ctx, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "SELECT id FROM networks WHERE id=$1 FOR UPDATE", networkID); err != nil {
				return err
			}
			var removed bool
			for _, jobID := range jobs[networkID] {
				var version int64
				err := tx.QueryRow(ctx, `SELECT version FROM production_jobs WHERE network_id=$1 AND id=$2 AND settled_at<now()-interval '30 days' FOR UPDATE`, networkID, jobID).Scan(&version)
				if err == pgx.ErrNoRows {
					continue
				}
				if err != nil {
					return err
				}
				if _, err = tx.Exec(ctx, `WITH revision AS (UPDATE networks SET jobs_revision=jobs_revision+1 WHERE id=$1 RETURNING jobs_revision)
					INSERT INTO production_job_changes(network_id,revision,job_id,version,removed) SELECT $1,jobs_revision,$2,$3,true FROM revision`, networkID, jobID, version+1); err != nil {
					return err
				}
				if _, err = tx.Exec(ctx, "DELETE FROM production_jobs WHERE network_id=$1 AND id=$2", networkID, jobID); err != nil {
					return err
				}
				removed = true
			}
			if !removed {
				return nil
			}
			if _, err := tx.Exec(ctx, `DELETE FROM production_job_changes WHERE network_id=$1 AND revision <=
				(SELECT jobs_revision-$2 FROM networks WHERE id=$1)`, networkID, productionJobChangeRetention); err != nil {
				return err
			}
			return s.event(ctx, tx, networkID, "jobs")
		}); err != nil {
			return err
		}
	}
	return nil
}
