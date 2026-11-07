package api

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"github.com/CaeliaEve/ae-orbit/internal/telemetry"
)

type routeMetrics struct {
	calls                                               [4]atomic.Uint64
	duration, query, acquire, batch, lockQuery          telemetry.Histogram
	bytes, plainBytes, gzip, clientErrors, serverErrors atomic.Uint64
}

type httpMetrics struct {
	mu     sync.Mutex
	routes map[string]*routeMetrics
}

type routeStatistics struct {
	Route             string             `json:"route"`
	Duration          telemetry.Duration `json:"duration"`
	Query             telemetry.Duration `json:"database_query"`
	Acquire           telemetry.Duration `json:"database_acquire"`
	Batch             telemetry.Duration `json:"database_batch"`
	LockQuery         telemetry.Duration `json:"database_lock_query"`
	QueryCalls        uint64             `json:"database_queries,string"`
	AcquireCalls      uint64             `json:"database_acquires,string"`
	BatchCalls        uint64             `json:"database_batches,string"`
	ResponseBytes     uint64             `json:"response_bytes,string"`
	UncompressedBytes uint64             `json:"uncompressed_bytes,string"`
	GzipResponses     uint64             `json:"gzip_responses,string"`
	ClientErrors      uint64             `json:"client_errors,string"`
	ServerErrors      uint64             `json:"server_errors,string"`
}

func (m *httpMetrics) observe(pattern string, w *measuredResponse) {
	if pattern == "" {
		pattern = "unmatched"
	}
	m.mu.Lock()
	if m.routes == nil {
		m.routes = make(map[string]*routeMetrics)
	}
	route := m.routes[pattern]
	if route == nil {
		route = &routeMetrics{}
		m.routes[pattern] = route
	}
	m.mu.Unlock()
	sample := w.sample.Snapshot()
	for phase, count := range sample.Calls {
		route.calls[phase].Add(count)
	}
	route.duration.Observe(time.Since(w.started), w.failed || w.status >= 400)
	route.query.Observe(sample.Stages[telemetry.Query], false)
	route.acquire.Observe(sample.Stages[telemetry.Acquire], false)
	route.batch.Observe(sample.Stages[telemetry.Batch], false)
	route.lockQuery.Observe(sample.Stages[telemetry.LockQuery], false)
	route.bytes.Add(w.bytes)
	plain := w.bytes
	if sample.UncompressedBytes > 0 {
		plain = uint64(sample.UncompressedBytes)
	}
	route.plainBytes.Add(plain)
	if w.Header().Get("Content-Encoding") == "gzip" {
		route.gzip.Add(1)
	}
	if w.status >= 500 {
		route.serverErrors.Add(1)
	} else if w.status >= 400 {
		route.clientErrors.Add(1)
	}
}

func (m *httpMetrics) snapshot() []routeStatistics {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]routeStatistics, 0, len(m.routes))
	for name, r := range m.routes {
		rows = append(rows, routeStatistics{Route: name, Duration: r.duration.Snapshot(), Query: r.query.Snapshot(), Acquire: r.acquire.Snapshot(), Batch: r.batch.Snapshot(), LockQuery: r.lockQuery.Snapshot(), QueryCalls: r.calls[telemetry.Query].Load(), AcquireCalls: r.calls[telemetry.Acquire].Load(), BatchCalls: r.calls[telemetry.Batch].Load(), ResponseBytes: r.bytes.Load(), UncompressedBytes: r.plainBytes.Load(), GzipResponses: r.gzip.Load(), ClientErrors: r.clientErrors.Load(), ServerErrors: r.serverErrors.Load()})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Route < rows[j].Route })
	return rows
}

type measuredResponse struct {
	http.ResponseWriter
	sample               *telemetry.Request
	started              time.Time
	status               int
	bytes                uint64
	failed, timing, head bool
}

func (w *measuredResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *measuredResponse) WriteHeader(status int) {
	if status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	if w.timing {
		p := w.sample.Snapshot()
		ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
		w.Header().Set("Server-Timing", fmt.Sprintf("app;dur=%.3f, db_query;dur=%.3f, db_acquire;dur=%.3f, db_batch;dur=%.3f, db_lock_query;dur=%.3f", ms(time.Since(w.started)), ms(p.Stages[telemetry.Query]), ms(p.Stages[telemetry.Acquire]), ms(p.Stages[telemetry.Batch]), ms(p.Stages[telemetry.LockQuery])))
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *measuredResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	if !w.head {
		w.bytes += uint64(n)
	}
	w.failed = w.failed || err != nil
	return n, err
}

type streamMetrics struct {
	reads, wakeups, fallback, delivered, resets atomic.Uint64
	readDuration, eventAge                      telemetry.Histogram
}

func (s *Server) readEvents(ctx context.Context, member, network string, after protocol.Quantity) (store.EventPage, error) {
	s.eventMetrics.reads.Add(1)
	started := time.Now()
	page, err := s.db.Events(ctx, member, network, after)
	s.eventMetrics.readDuration.Observe(time.Since(started), err != nil)
	return page, err
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request, m protocol.Member) {
	if err := s.db.RequireAdmin(r.Context(), m.ID); err != nil {
		fail(w, err)
		return
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	pool := s.db.Pool().Stat()
	events := &s.eventMetrics
	respond(w, 200, map[string]any{
		"uptime_seconds": int64(time.Since(s.started).Seconds()), "requests": strconv.FormatUint(s.total.Load(), 10), "active_requests": len(s.requests), "active_streams": len(s.streams), "goroutines": runtime.NumGoroutine(), "heap_bytes": strconv.FormatUint(memory.HeapAlloc, 10), "gc_cycles": memory.NumGC,
		"database_connections": pool.TotalConns(), "database_acquired": pool.AcquiredConns(), "database_acquire_wait_ms": pool.EmptyAcquireWaitTime().Milliseconds(), "database_acquires": strconv.FormatInt(pool.AcquireCount(), 10), "database_empty_acquires": strconv.FormatInt(pool.EmptyAcquireCount(), 10), "database_canceled_acquires": strconv.FormatInt(pool.CanceledAcquireCount(), 10),
		"http": s.performance.snapshot(), "database": s.db.DatabaseStats(), "notifications": s.db.NotificationStats(),
		"events": map[string]any{"reads": strconv.FormatUint(events.reads.Load(), 10), "wakeups": strconv.FormatUint(events.wakeups.Load(), 10), "fallback_checks": strconv.FormatUint(events.fallback.Load(), 10), "delivered": strconv.FormatUint(events.delivered.Load(), 10), "resets": strconv.FormatUint(events.resets.Load(), 10), "read_duration": events.readDuration.Snapshot(), "event_age": events.eventAge.Snapshot()},
	})
}
