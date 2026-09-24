package unit_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/discovery"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/metrics"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/proxy"
)

func TestRouter_NormalizeHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"example.com", "example.com"},
		{"EXAMPLE.COM", "example.com"},
		{"example.com:8080", "example.com"},
		{"LOCALHOST:80", "localhost"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := proxy.NormalizeHost(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeHost(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestRouter_DefaultConfig(t *testing.T) {
	t.Parallel()

	collector := metrics.NewCollector()
	router := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 0,
		QueueTimeout:          0,
	}, collector)

	if router == nil {
		t.Fatal("Expected non-nil Router when initialized with default config")
	}
}

func TestRouter_ServeHTTP_Routing(t *testing.T) {
	t.Parallel()

	collector := metrics.NewCollector()
	router := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 10,
		QueueTimeout:          1 * time.Second,
	}, collector)

	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK from backend"))
	}))
	defer backendServer.Close()

	backendURL, _ := url.Parse(backendServer.URL)
	router.RegisterBackend("app.local", backendURL)

	// Test 1: Registered host rule
	req := httptest.NewRequest(http.MethodGet, "http://app.local:8080/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", rec.Code)
	}

	// Test 2: Unregistered host rule
	reqUnk := httptest.NewRequest(http.MethodGet, "http://unknown.local/", nil)
	recUnk := httptest.NewRecorder()
	router.ServeHTTP(recUnk, reqUnk)

	if recUnk.Code != http.StatusBadGateway {
		t.Fatalf("Expected status 502, got %d", recUnk.Code)
	}
}

func TestRouter_UpdateBackends(t *testing.T) {
	t.Parallel()

	collector := metrics.NewCollector()
	router := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 10,
		QueueTimeout:          1 * time.Second,
	}, collector)

	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("dynamic backend OK"))
	}))
	defer backendServer.Close()

	backendURL, _ := url.Parse(backendServer.URL)
	targets := []discovery.ServiceTarget{
		{
			HostRule:  "dynamic.local",
			TargetURL: backendURL,
			Healthy:   true,
			Enabled:   true,
		},
	}

	router.UpdateBackends(targets)

	req := httptest.NewRequest(http.MethodGet, "http://dynamic.local/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200 after UpdateBackends, got %d", rec.Code)
	}
}

func TestRouter_MultiReplica_RegisterBackend(t *testing.T) {
	t.Parallel()

	collector := metrics.NewCollector()
	router := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 10,
		QueueTimeout:          1 * time.Second,
	}, collector)

	var counts [3]atomic.Uint64
	servers := make([]*httptest.Server, 3)
	for i := 0; i < 3; i++ {
		idx := i
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counts[idx].Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fmt.Sprintf("backend %d", idx)))
		}))
		defer servers[i].Close()

		backendURL, _ := url.Parse(servers[i].URL)
		router.RegisterBackend("web.local", backendURL)
	}

	// Send 9 requests to web.local — RoundRobin should distribute 3 to each
	for i := 0; i < 9; i++ {
		req := httptest.NewRequest(http.MethodGet, "http://web.local/", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Request %d failed with status %d", i, rec.Code)
		}
	}

	for i := 0; i < 3; i++ {
		if c := counts[i].Load(); c != 3 {
			t.Fatalf("Backend %d received %d requests, expected exactly 3", i, c)
		}
	}
}

func TestRouter_UpdateBackends_MultiReplica_Algorithms(t *testing.T) {
	t.Parallel()

	collector := metrics.NewCollector()
	router := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 20,
		QueueTimeout:          1 * time.Second,
	}, collector)

	var counts [3]atomic.Uint64
	targets := make([]discovery.ServiceTarget, 3)
	servers := make([]*httptest.Server, 3)

	for i := 0; i < 3; i++ {
		idx := i
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counts[idx].Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		defer servers[i].Close()

		backendURL, _ := url.Parse(servers[i].URL)
		targets[i] = discovery.ServiceTarget{
			ID:        fmt.Sprintf("target-%d", i),
			HostRule:  "multi.local",
			TargetURL: backendURL,
			Labels: map[string]string{
				"traffic-proxy.balance": "round-robin",
			},
			Healthy: true,
			Enabled: true,
		}
	}

	router.UpdateBackends(targets)

	// Round-robin: 6 requests -> 2 each
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest(http.MethodGet, "http://multi.local/", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Request %d failed: status %d", i, rec.Code)
		}
	}

	for i := 0; i < 3; i++ {
		if c := counts[i].Load(); c != 2 {
			t.Fatalf("Backend %d received %d requests, expected 2", i, c)
		}
	}

	// Now switch to IPHash
	for i := range targets {
		targets[i].Labels["traffic-proxy.balance"] = "ip-hash"
	}
	router.UpdateBackends(targets)

	// Requests with identical client IP should always hit the same backend
	var ipHashPickedBackend int = -1
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "http://multi.local/", nil)
		req.RemoteAddr = "192.168.100.50:12345"
		rec := httptest.NewRecorder()

		prevCounts := [3]uint64{counts[0].Load(), counts[1].Load(), counts[2].Load()}
		router.ServeHTTP(rec, req)

		var hitIdx int = -1
		for j := 0; j < 3; j++ {
			if counts[j].Load() > prevCounts[j] {
				hitIdx = j
				break
			}
		}
		if hitIdx == -1 {
			t.Fatalf("Request %d did not register on any backend", i)
		}
		if ipHashPickedBackend == -1 {
			ipHashPickedBackend = hitIdx
		} else if hitIdx != ipHashPickedBackend {
			t.Fatalf("IPHash routed to backend %d, but expected sticky backend %d", hitIdx, ipHashPickedBackend)
		}
	}
}

func TestRouter_UnhealthyBackendsExcluded(t *testing.T) {
	t.Parallel()

	collector := metrics.NewCollector()
	router := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 10,
		QueueTimeout:          1 * time.Second,
	}, collector)

	var healthyHit atomic.Uint64
	var unhealthyHit atomic.Uint64

	healthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		healthyHit.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer healthyServer.Close()

	unhealthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unhealthyHit.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer unhealthyServer.Close()

	hURL, _ := url.Parse(healthyServer.URL)
	uURL, _ := url.Parse(unhealthyServer.URL)

	targets := []discovery.ServiceTarget{
		{
			HostRule:  "mixed.local",
			TargetURL: hURL,
			Healthy:   true,
			Enabled:   true,
		},
		{
			HostRule:  "mixed.local",
			TargetURL: uURL,
			Healthy:   false, // Excluded from pool
			Enabled:   true,
		},
	}

	router.UpdateBackends(targets)

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "http://mixed.local/", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", rec.Code)
		}
	}

	if healthyHit.Load() != 5 {
		t.Fatalf("Expected 5 hits to healthy backend, got %d", healthyHit.Load())
	}
	if unhealthyHit.Load() != 0 {
		t.Fatalf("Expected 0 hits to unhealthy backend, got %d", unhealthyHit.Load())
	}
}

func TestRouter_QueueTimeout(t *testing.T) {
	t.Parallel()

	collector := metrics.NewCollector()
	router := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 1,
		QueueTimeout:          50 * time.Millisecond,
	}, collector)

	blockBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer blockBackend.Close()

	backendURL, _ := url.Parse(blockBackend.URL)
	router.RegisterBackend("block.local", backendURL)

	started := make(chan struct{})
	done := make(chan struct{})

	// Fire first request to hold semaphore
	go func() {
		close(started)
		req1 := httptest.NewRequest(http.MethodGet, "http://block.local/", nil)
		rec1 := httptest.NewRecorder()
		router.ServeHTTP(rec1, req1)
		close(done)
	}()

	<-started
	time.Sleep(10 * time.Millisecond)

	// Second request should exceed queue timeout and return 503
	req2 := httptest.NewRequest(http.MethodGet, "http://block.local/", nil)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusServiceUnavailable {
		t.Fatalf("Expected status 503 Service Unavailable, got %d", rec2.Code)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("First request timed out unexpectedly")
	}
}
