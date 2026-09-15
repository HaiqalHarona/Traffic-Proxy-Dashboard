package unit_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

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
	routes := map[string]*url.URL{
		"dynamic.local": backendURL,
	}

	router.UpdateBackends(routes)

	req := httptest.NewRequest(http.MethodGet, "http://dynamic.local/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200 after UpdateBackends, got %d", rec.Code)
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
