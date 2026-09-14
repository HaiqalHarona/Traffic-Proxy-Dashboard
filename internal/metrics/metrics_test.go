package metrics

import (
	"sync"
	"testing"
)

func TestTelemetryRingBuffer_PushAndLatest(t *testing.T) {
	t.Parallel()

	rb := NewTelemetryRingBuffer(3) // capacity 8

	s1 := MetricSnapshot{TotalRequests: 1}
	s2 := MetricSnapshot{TotalRequests: 2}

	rb.Push(s1)
	if rb.Latest().TotalRequests != 1 {
		t.Fatalf("Expected TotalRequests 1, got %d", rb.Latest().TotalRequests)
	}

	rb.Push(s2)
	if rb.Latest().TotalRequests != 2 {
		t.Fatalf("Expected TotalRequests 2, got %d", rb.Latest().TotalRequests)
	}
}

func TestCollector_Concurrency(t *testing.T) {
	t.Parallel()

	c := NewCollector()
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
}
