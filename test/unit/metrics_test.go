package unit_test

import (
	"sync"
	"testing"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/metrics"
)

func TestTelemetryRingBuffer_PushAndLatest(t *testing.T) {
	t.Parallel()

	rb := metrics.NewTelemetryRingBuffer(3) // capacity 8

	// Empty ring buffer
	empty := rb.Latest()
	if empty.TotalRequests != 0 {
		t.Fatalf("Expected empty snapshot with 0 TotalRequests, got %d", empty.TotalRequests)
	}

	s1 := metrics.MetricSnapshot{TotalRequests: 1}
	s2 := metrics.MetricSnapshot{TotalRequests: 2}

	rb.Push(s1)
	if rb.Latest().TotalRequests != 1 {
		t.Fatalf("Expected TotalRequests 1, got %d", rb.Latest().TotalRequests)
	}

	rb.Push(s2)
	if rb.Latest().TotalRequests != 2 {
		t.Fatalf("Expected TotalRequests 2, got %d", rb.Latest().TotalRequests)
	}
}

func TestTelemetryRingBuffer_WrapAround(t *testing.T) {
	t.Parallel()

	rb := metrics.NewTelemetryRingBuffer(2) // capacity 4

	for i := uint64(1); i <= 10; i++ {
		rb.Push(metrics.MetricSnapshot{TotalRequests: i})
	}

	latest := rb.Latest()
	if latest.TotalRequests != 10 {
		t.Fatalf("Expected TotalRequests 10 after wrap-around, got %d", latest.TotalRequests)
	}
}

func TestCollector_Concurrency(t *testing.T) {
	t.Parallel()

	c := metrics.NewCollector()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.TotalRequests.Add(1)
			c.ActiveConcurrency.Add(1)
			_ = c.Snapshot(5)
			c.ActiveConcurrency.Add(-1)
		}()
	}

	wg.Wait()

	if c.TotalRequests.Load() != 100 {
		t.Fatalf("Expected TotalRequests 100, got %d", c.TotalRequests.Load())
	}

	snap := c.Snapshot(10)
	if snap.DiscoveredServices != 10 {
		t.Fatalf("Expected DiscoveredServices 10, got %d", snap.DiscoveredServices)
	}
}
