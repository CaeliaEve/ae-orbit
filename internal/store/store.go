// Package store owns durable state and all transitions that require a transaction.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not_found")
	ErrInvalid      = errors.New("invalid_request")
	ErrConflict     = errors.New("conflict")
	ErrSequence     = errors.New("sequence_conflict")
	ErrStaleSession = errors.New("stale_session")
	ErrViewChanged  = errors.New("view_changed")
	ErrCapacity     = errors.New("capacity_exceeded")
	ErrCapability   = errors.New("capability_unavailable")
)

type Options struct {
	MaxConnections  int32
	Limits          protocol.Limits
	MaxQueuedOrders int
	ActiveWindow    int
	GroupPlanning   int
	ScanItems       int
	OfflineAfter    time.Duration
	RecheckAfter    time.Duration
	EventRetention  int
}

func DefaultOptions() Options {
	return Options{MaxConnections: 16, Limits: protocol.Limits{BatchBytes: 32768, BatchItems: 128, ResponseBytes: 32768, Commands: 4, CPUs: 64, PollMillis: 1000}, MaxQueuedOrders: 256, ActiveWindow: 4, GroupPlanning: 1, ScanItems: 32, OfflineAfter: 90 * time.Second, RecheckAfter: 5 * time.Minute, EventRetention: 1024}
}

type Store struct {
	pool      *pgxpool.Pool
	options   Options
	cursorKey [32]byte
}
type Device struct {
	NetworkID string
	TokenHash [32]byte
}

func Open(ctx context.Context, dsn string, options Options) (*Store, error) {
	if options.MaxConnections < 1 || options.Limits.BatchItems < 1 || options.Limits.BatchBytes < 2048 || options.Limits.ResponseBytes < 2048 || options.Limits.Commands < 1 || options.ActiveWindow < 1 || options.GroupPlanning < 1 || options.EventRetention < 1 || options.OfflineAfter <= 0 || options.RecheckAfter <= 0 {
		return nil, fmt.Errorf("invalid store budgets")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = options.MaxConnections
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	cfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "15000"
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, err
	}
	s := &Store{pool: p, options: options}
	if _, err = rand.Read(s.cursorKey[:]); err != nil {
		p.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close()              { s.pool.Close() }
func (s *Store) Pool() *pgxpool.Pool { return s.pool }
func (s *Store) Options() Options    { return s.options }

func (s *Store) transaction(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func newToken(prefix string) string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b[:])
}
func hashToken(token string) [32]byte { return sha256.Sum256([]byte(token)) }
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s *Store) event(ctx context.Context, tx pgx.Tx, networkID, kind string) error {
	_, err := tx.Exec(ctx, `INSERT INTO network_events(network_id,kind,revision) SELECT id,$2,revision FROM networks WHERE id=$1`, networkID, kind)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM network_events WHERE network_id=$1 AND id < (SELECT id FROM network_events WHERE network_id=$1 ORDER BY id DESC OFFSET $2 LIMIT 1)`, networkID, s.options.EventRetention-1)
	return err
}
