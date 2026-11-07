package store

import (
	"context"
	"strings"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/telemetry"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type databaseMetrics struct {
	query, acquire, batch, lockQuery, transaction, commit telemetry.Histogram
}

type DatabaseStatistics struct {
	Query       telemetry.Duration `json:"query"`
	Acquire     telemetry.Duration `json:"acquire"`
	Batch       telemetry.Duration `json:"batch"`
	LockQuery   telemetry.Duration `json:"lock_query"`
	Transaction telemetry.Duration `json:"transaction"`
	Commit      telemetry.Duration `json:"commit"`
}

func (s *Store) DatabaseStats() DatabaseStatistics {
	m := &s.performance
	return DatabaseStatistics{Query: m.query.Snapshot(), Acquire: m.acquire.Snapshot(), Batch: m.batch.Snapshot(), LockQuery: m.lockQuery.Snapshot(), Transaction: m.transaction.Snapshot(), Commit: m.commit.Snapshot()}
}

type databaseTracer struct{ metrics *databaseMetrics }
type queryKey struct{}
type acquireKey struct{}
type batchKey struct{}
type querySpan struct {
	started time.Time
	locking bool
}

func (t *databaseTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	// Only classify our explicit locking statements. No SQL or arguments are
	// retained in the metrics or the returned context.
	return context.WithValue(ctx, queryKey{}, querySpan{started: time.Now(), locking: strings.Contains(data.SQL, "FOR UPDATE") || strings.Contains(data.SQL, "FOR SHARE")})
}

func (t *databaseTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span := ctx.Value(queryKey{}).(querySpan)
	elapsed := time.Since(span.started)
	t.metrics.query.Observe(elapsed, data.Err != nil)
	telemetry.Add(ctx, telemetry.Query, elapsed)
	if span.locking {
		t.metrics.lockQuery.Observe(elapsed, data.Err != nil)
		telemetry.Add(ctx, telemetry.LockQuery, elapsed)
	}
}

func (t *databaseTracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	return context.WithValue(ctx, acquireKey{}, time.Now())
}

func (t *databaseTracer) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	elapsed := time.Since(ctx.Value(acquireKey{}).(time.Time))
	t.metrics.acquire.Observe(elapsed, data.Err != nil)
	telemetry.Add(ctx, telemetry.Acquire, elapsed)
}

func (t *databaseTracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	return context.WithValue(ctx, batchKey{}, time.Now())
}

// The complete batch is timed once; individual result reads would overlap it.
func (t *databaseTracer) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {}

func (t *databaseTracer) TraceBatchEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchEndData) {
	elapsed := time.Since(ctx.Value(batchKey{}).(time.Time))
	t.metrics.batch.Observe(elapsed, data.Err != nil)
	telemetry.Add(ctx, telemetry.Batch, elapsed)
}
