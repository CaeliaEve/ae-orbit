package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrations embed.FS

func (s *Store) Migrate(ctx context.Context) error {
	return s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(614238427)"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_versions(version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
			return err
		}
		files, err := fs.Glob(migrations, "migrations/*.sql")
		if err != nil {
			return err
		}
		sort.Strings(files)
		for _, name := range files {
			var exists bool
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_versions WHERE version=$1)", name).Scan(&exists); err != nil {
				return err
			}
			if exists {
				continue
			}
			body, err := migrations.ReadFile(name)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, string(body)); err != nil {
				return fmt.Errorf("migration %s: %w", strings.TrimPrefix(name, "migrations/"), err)
			}
			if name == "migrations/006_nbt_identity.sql" {
				if err = migrateNBTIdentity(ctx, tx); err != nil {
					return err
				}
			}
			if _, err = tx.Exec(ctx, "INSERT INTO schema_versions(version) VALUES($1)", name); err != nil {
				return err
			}
		}
		return nil
	})
}
