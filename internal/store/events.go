package store

import (
	"context"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

type EventPage struct {
	Events []protocol.Event
	Reset  bool
	LastID protocol.Quantity
}

func (s *Store) Events(ctx context.Context, userID, networkID string, after protocol.Quantity) (EventPage, error) {
	page := EventPage{Events: []protocol.Event{}}
	if err := s.RequireAccess(ctx, userID, networkID, false); err != nil {
		return page, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return page, err
	}
	defer tx.Rollback(context.Background())
	var first protocol.Quantity
	if err = tx.QueryRow(ctx, "SELECT coalesce(min(id),0),coalesce(max(id),0) FROM network_events WHERE network_id=$1", networkID).Scan(&first, &page.LastID); err != nil {
		return page, err
	}
	if after > page.LastID || (first > 0 && after < first-1) {
		page.Reset = true
		return page, tx.Commit(ctx)
	}
	rows, err := tx.Query(ctx, "SELECT id,network_id,kind,revision,created_at FROM network_events WHERE network_id=$1 AND id>$2 ORDER BY id LIMIT 129", networkID, int64(after))
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var e protocol.Event
		if err = rows.Scan(&e.ID, &e.NetworkID, &e.Kind, &e.Revision, &e.CreatedAt); err != nil {
			rows.Close()
			return page, err
		}
		page.Events = append(page.Events, e)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Events) > 128 {
		page.Reset = true
		page.Events = []protocol.Event{}
	}
	return page, tx.Commit(ctx)
}
