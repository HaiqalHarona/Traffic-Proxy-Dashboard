package unit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/metrics"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/proxy"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/server"
)

func TestSetupRouter_Endpoints(t *testing.T) {
	t.Parallel()

	collector := metrics.NewCollector()
	proxyRouter := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 10,
		QueueTimeout:          1 * time.Second,
	}, collector)

	r := server.SetupRouter(collector, proxyRouter, nil)

	// Test 1: Root route serves index.html
	reqRoot := httptest.NewRequest(http.MethodGet, "/", nil)
	recRoot := httptest.NewRecorder()
	r.ServeHTTP(recRoot, reqRoot)

	if recRoot.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for /, got %d", recRoot.Code)
	}

	// Test 2: Static file route
	reqStatic := httptest.NewRequest(http.MethodGet, "/static/", nil)
	recStatic := httptest.NewRecorder()
	r.ServeHTTP(recStatic, reqStatic)

	if recStatic.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for /static/, got %d", recStatic.Code)
	}

	// Test 3: Catch-all routes to proxy router (returns 502 for unmapped backend)
	reqUnk := httptest.NewRequest(http.MethodGet, "http://unknown.local/api/foo", nil)
	recUnk := httptest.NewRecorder()
	r.ServeHTTP(recUnk, reqUnk)

	if recUnk.Code != http.StatusBadGateway {
		t.Fatalf("Expected status 502 for unmapped proxy route, got %d", recUnk.Code)
	}
}

func TestSetupRouter_SSEEvents(t *testing.T) {
	t.Parallel()

	collector := metrics.NewCollector()
	proxyRouter := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 10,
		QueueTimeout:          1 * time.Second,
	}, collector)

	r := server.SetupRouter(collector, proxyRouter, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Expected text/event-stream content type, got %q", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("Expected no-cache cache control, got %q", rec.Header().Get("Cache-Control"))
	}
}
