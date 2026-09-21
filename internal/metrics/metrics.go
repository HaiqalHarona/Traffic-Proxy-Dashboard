package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// MetricSnapshot represents a telemetry snapshot sent over SSE.
type MetricSnapshot struct {
	Timestamp          int64  `json:"timestamp"`
	TotalRequests      uint64 `json:"total_requests"`
	ActiveConcurrency  int64  `json:"active_concurrency"`
	QueuedRequests     int64  `json:"queued_requests"`
	DiscoveredServices int    `json:"discovered_services"`
	HealthyServices    int    `json:"healthy_services"`
	SystemHealthy      bool   `json:"system_healthy"`
}

// TelemetryRingBuffer provides thread-safe ring buffer storage for telemetry snapshots.
type TelemetryRingBuffer struct {
	data     []MetricSnapshot
	mu       sync.RWMutex
	capacity uint64
	mask     uint64
	writeIdx uint64
}

func NewTelemetryRingBuffer(sizeExponent uint8) *TelemetryRingBuffer {
	capacity := uint64(1 << sizeExponent)
	return &TelemetryRingBuffer{
		capacity: capacity,
		mask:     capacity - 1,
		writeIdx: 0,
		data:     make([]MetricSnapshot, capacity),
	}
}

func (rb *TelemetryRingBuffer) Push(snapshot MetricSnapshot) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	slot := rb.writeIdx & rb.mask
	rb.data[slot] = snapshot
	rb.writeIdx++
}

func (rb *TelemetryRingBuffer) Latest() MetricSnapshot {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	if rb.writeIdx == 0 {
		return MetricSnapshot{}
	}
	slot := (rb.writeIdx - 1) & rb.mask
	return rb.data[slot]
}

// Collector tracks global proxy telemetry using atomic primitives for counters.
type Collector struct {
	RingBuffer        *TelemetryRingBuffer
	TotalRequests     atomic.Uint64
	ActiveConcurrency atomic.Int64
	QueuedRequests    atomic.Int64
}

func NewCollector() *Collector {
	return &Collector{
		RingBuffer: NewTelemetryRingBuffer(10), // Capacity: 1024
	}
}

func (c *Collector) Snapshot(discoveredCount int, optionalHealthy ...int) MetricSnapshot {
	healthyCount := discoveredCount
	if len(optionalHealthy) > 0 {
		healthyCount = optionalHealthy[0]
	}
	systemHealthy := (discoveredCount > 0 && healthyCount == discoveredCount)

	s := MetricSnapshot{
		Timestamp:          time.Now().UnixMilli(),
		TotalRequests:      c.TotalRequests.Load(),
		ActiveConcurrency:  c.ActiveConcurrency.Load(),
		QueuedRequests:     c.QueuedRequests.Load(),
		DiscoveredServices: discoveredCount,
		HealthyServices:    healthyCount,
		SystemHealthy:      systemHealthy,
	}
	c.RingBuffer.Push(s)
	return s
}

// Reset atomically resets all metric counters back to zero.
func (c *Collector) Reset() {
	c.TotalRequests.Store(0)
	c.ActiveConcurrency.Store(0)
	c.QueuedRequests.Store(0)
}

