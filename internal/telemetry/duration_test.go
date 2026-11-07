package telemetry_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/telemetry"
)

func TestHistogramIncludesBoundariesErrorsOverflowAndConcurrentSamples(t *testing.T) {
	var h telemetry.Histogram
	h.Observe(500*time.Microsecond, false)
	h.Observe(25*time.Millisecond, true)
	h.Observe(20*time.Second, false)
	first := h.Snapshot()
	if first.Count != 3 || first.Errors != 1 || first.TotalMillis != 20025.5 || first.MaxMillis != 20000 || first.Buckets[0].Count != 1 || first.Buckets[3].Count != 2 || first.Buckets[len(first.Buckets)-1].Count != 2 {
		t.Fatalf("incorrect histogram including overflow: %+v", first)
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				h.Observe(2*time.Millisecond, false)
				_ = h.Snapshot()
			}
		}()
	}
	workers.Wait()
	last := h.Snapshot()
	if last.Count != 803 || last.TotalMillis != 21625.5 || last.Buckets[1].Count != 801 {
		t.Fatalf("concurrent observations were lost: %+v", last)
	}
}

func TestRequestMeasurementsKeepContextsIndependentAndCountZeroDurations(t *testing.T) {
	a, first := telemetry.WithRequest(context.Background())
	b, second := telemetry.WithRequest(context.Background())
	telemetry.Add(a, telemetry.Acquire, 0)
	telemetry.Add(a, telemetry.Query, 3*time.Millisecond)
	telemetry.Add(b, telemetry.Query, time.Millisecond)
	telemetry.UncompressedBytes(a, 1024)
	x, y := first.Snapshot(), second.Snapshot()
	if x.Calls[telemetry.Acquire] != 1 || x.Stages[telemetry.Query] != 3*time.Millisecond || x.UncompressedBytes != 1024 || y.Stages[telemetry.Query] != time.Millisecond || y.UncompressedBytes != 0 {
		t.Fatalf("request measurements leaked or lost zero-duration calls: %+v %+v", x, y)
	}
}
