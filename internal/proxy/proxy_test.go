package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/metrics"
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
			got := normalizeHost(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeHost(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestRouter_ServeHTTP_Routing(t *testing.T) {
	t.Parallel()

	collector := metrics.NewCollector()
	router := NewRouter(Config{
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
