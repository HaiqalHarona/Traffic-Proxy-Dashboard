package unit_test

import (
	"net/url"
	"testing"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/proxy"
)

func createTestBackends(count int) []*proxy.Backend {
	backends := make([]*proxy.Backend, count)
	for i := 0; i < count; i++ {
		u, _ := url.Parse("http://127.0.0.1:8080")
		backends[i] = &proxy.Backend{
			URL:     u,
			Healthy: true,
		}
	}
	return backends
}

func TestRoundRobin_Pick(t *testing.T) {
	t.Parallel()

	balancer := proxy.NewBalancer("round-robin")
	backends := createTestBackends(3)

	// Verify cyclic distribution across 3 backends: 0, 1, 2, 0, 1, 2
	expectedIndices := []int{0, 1, 2, 0, 1, 2}
	for i, exp := range expectedIndices {
		picked := balancer.Pick(backends, "127.0.0.1")
		if picked != backends[exp] {
			t.Fatalf("Iteration %d: expected backend %d, got %+v", i, exp, picked)
		}
	}

	// Empty backends check
	if picked := balancer.Pick(nil, "127.0.0.1"); picked != nil {
		t.Fatalf("Expected nil when backends slice is nil, got %+v", picked)
	}
	if picked := balancer.Pick([]*proxy.Backend{}, "127.0.0.1"); picked != nil {
		t.Fatalf("Expected nil when backends slice is empty, got %+v", picked)
	}
}

func TestLeastConn_Pick(t *testing.T) {
	t.Parallel()

	balancer := proxy.NewBalancer("least-conn")
	backends := createTestBackends(3)

	backends[0].ActiveConns.Store(5)
	backends[1].ActiveConns.Store(1)
	backends[2].ActiveConns.Store(3)

	// Backend 1 has the minimum connections (1)
	picked := balancer.Pick(backends, "127.0.0.1")
	if picked != backends[1] {
		t.Fatalf("Expected backend 1 with lowest conns (1), got %+v", picked)
	}

	// Shift load: backend 1 gets 10 connections, backend 2 drops to 0
	backends[1].ActiveConns.Store(10)
	backends[2].ActiveConns.Store(0)

	picked2 := balancer.Pick(backends, "127.0.0.1")
	if picked2 != backends[2] {
		t.Fatalf("Expected backend 2 with lowest conns (0), got %+v", picked2)
	}

	// Empty backends check
	if picked := balancer.Pick(nil, "127.0.0.1"); picked != nil {
		t.Fatalf("Expected nil when backends slice is nil, got %+v", picked)
	}
}

func TestRandom_Pick(t *testing.T) {
	t.Parallel()

	balancer := proxy.NewBalancer("random")
	backends := createTestBackends(3)

	counts := make(map[*proxy.Backend]int)
	totalIterations := 300

	for i := 0; i < totalIterations; i++ {
		picked := balancer.Pick(backends, "127.0.0.1")
		if picked == nil {
			t.Fatalf("Expected non-nil backend on iteration %d", i)
		}
		counts[picked]++
	}

	// Over 300 iterations across 3 backends, every backend must be selected at least once
	for i, b := range backends {
		if counts[b] == 0 {
			t.Fatalf("Expected backend %d to receive traffic, got 0 requests", i)
		}
	}

	// Empty backends check
	if picked := balancer.Pick([]*proxy.Backend{}, "127.0.0.1"); picked != nil {
		t.Fatalf("Expected nil when backends slice is empty, got %+v", picked)
	}
}

func TestIPHash_Pick(t *testing.T) {
	t.Parallel()

	balancer := proxy.NewBalancer("ip-hash")
	backends := createTestBackends(4)

	// Consistent routing for same client IP
	clientA := "192.168.1.100"
	firstPickedA := balancer.Pick(backends, clientA)
	for i := 0; i < 20; i++ {
		picked := balancer.Pick(backends, clientA)
		if picked != firstPickedA {
			t.Fatalf("Expected sticky routing for %s, got different backend on iteration %d", clientA, i)
		}
	}

	// Consistent routing for second client IP
	clientB := "10.0.0.55"
	firstPickedB := balancer.Pick(backends, clientB)
	for i := 0; i < 20; i++ {
		picked := balancer.Pick(backends, clientB)
		if picked != firstPickedB {
			t.Fatalf("Expected sticky routing for %s, got different backend on iteration %d", clientB, i)
		}
	}

	// Empty IP fallback to first backend
	if picked := balancer.Pick(backends, ""); picked != backends[0] {
		t.Fatalf("Expected fallback to backends[0] on empty IP, got %+v", picked)
	}

	// Empty backends check
	if picked := balancer.Pick(nil, clientA); picked != nil {
		t.Fatalf("Expected nil when backends slice is nil, got %+v", picked)
	}
}

func TestNewBalancer_AlgorithmParsing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input        string
		expectedType string
	}{
		{"round-robin", "*proxy.RoundRobin"},
		{"roundrobin", "*proxy.RoundRobin"},
		{"rr", "*proxy.RoundRobin"},
		{"least-conn", "*proxy.LeastConn"},
		{"leastconn", "*proxy.LeastConn"},
		{"lc", "*proxy.LeastConn"},
		{"random", "*proxy.Random"},
		{"rand", "*proxy.Random"},
		{"ip-hash", "*proxy.IPHash"},
		{"iphash", "*proxy.IPHash"},
		{"hash", "*proxy.IPHash"},
		{"unknown", "*proxy.RoundRobin"},
		{"", "*proxy.RoundRobin"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			b := proxy.NewBalancer(tc.input)
			switch tc.expectedType {
			case "*proxy.RoundRobin":
				if _, ok := b.(*proxy.RoundRobin); !ok {
					t.Fatalf("Input %q: expected *proxy.RoundRobin, got %T", tc.input, b)
				}
			case "*proxy.LeastConn":
				if _, ok := b.(*proxy.LeastConn); !ok {
					t.Fatalf("Input %q: expected *proxy.LeastConn, got %T", tc.input, b)
				}
			case "*proxy.Random":
				if _, ok := b.(*proxy.Random); !ok {
					t.Fatalf("Input %q: expected *proxy.Random, got %T", tc.input, b)
				}
			case "*proxy.IPHash":
				if _, ok := b.(*proxy.IPHash); !ok {
					t.Fatalf("Input %q: expected *proxy.IPHash, got %T", tc.input, b)
				}
			}
		})
	}
}
