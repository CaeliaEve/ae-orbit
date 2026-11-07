// Package telemetry provides bounded, in-process performance measurements.
package telemetry

import (
	"context"
	"sync"
	"time"
)

var bounds = [...]time.Duration{time.Millisecond, 5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 5 * time.Second, 15 * time.Second}

type Bucket struct {
	LEMillis float64 `json:"le_ms"`
	Count    uint64  `json:"count,string"`
}

type Duration struct {
	Count       uint64   `json:"count,string"`
	Errors      uint64   `json:"errors,string"`
	TotalMillis float64  `json:"total_ms"`
	MaxMillis   float64  `json:"max_ms"`
	Buckets     []Bucket `json:"buckets"`
}

type Histogram struct {
	mu                   sync.Mutex
	count, errors, total uint64
	max                  time.Duration
	buckets              [len(bounds)]uint64
}

func (h *Histogram) Observe(elapsed time.Duration, failed bool) {
	if elapsed < 0 {
		elapsed = 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.count++
	h.total += uint64(elapsed)
	if failed {
		h.errors++
	}
	if elapsed > h.max {
		h.max = elapsed
	}
	for i, bound := range bounds {
		if elapsed <= bound {
			h.buckets[i]++
			break
		}
	}
}

func (h *Histogram) Snapshot() Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := Duration{Count: h.count, Errors: h.errors, TotalMillis: float64(h.total) / float64(time.Millisecond), MaxMillis: float64(h.max) / float64(time.Millisecond), Buckets: make([]Bucket, len(bounds))}
	var count uint64
	for i, bound := range bounds {
		count += h.buckets[i]
		d.Buckets[i] = Bucket{LEMillis: float64(bound) / float64(time.Millisecond), Count: count}
	}
	return d
}

type Phase uint8

const (
	Query Phase = iota
	Acquire
	Batch
	LockQuery
	phaseCount
)

type Request struct {
	mu                sync.Mutex
	stages            [phaseCount]time.Duration
	calls             [phaseCount]uint64
	uncompressedBytes int
}

type RequestSnapshot struct {
	Stages            [phaseCount]time.Duration
	Calls             [phaseCount]uint64
	UncompressedBytes int
}

type requestKey struct{}

func WithRequest(ctx context.Context) (context.Context, *Request) {
	r := &Request{}
	return context.WithValue(ctx, requestKey{}, r), r
}

func Add(ctx context.Context, phase Phase, elapsed time.Duration) {
	if r, ok := ctx.Value(requestKey{}).(*Request); ok {
		r.mu.Lock()
		r.stages[phase] += elapsed
		r.calls[phase]++
		r.mu.Unlock()
	}
}

func UncompressedBytes(ctx context.Context, size int) {
	if r, ok := ctx.Value(requestKey{}).(*Request); ok {
		r.mu.Lock()
		r.uncompressedBytes = size
		r.mu.Unlock()
	}
}

func (r *Request) Snapshot() RequestSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RequestSnapshot{Stages: r.stages, Calls: r.calls, UncompressedBytes: r.uncompressedBytes}
}
