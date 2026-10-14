package unit_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/metrics"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/proxy"
)

func TestProxy_PathRouting_And_PrefixStripping(t *testing.T) {
	t.Parallel()

	var receivedAPIPath atomic.Value
	var receivedAPIPrefix atomic.Value
	apiBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAPIPath.Store(r.URL.Path)
		receivedAPIPrefix.Store(r.Header.Get("X-Forwarded-Prefix"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("api response"))
	}))
	defer apiBackend.Close()

	var receivedWebPath atomic.Value
	var receivedWebPrefix atomic.Value
	webBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedWebPath.Store(r.URL.Path)
		receivedWebPrefix.Store(r.Header.Get("X-Forwarded-Prefix"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("web response"))
	}))
	defer webBackend.Close()

	collector := metrics.NewCollector()
	router := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 50,
		QueueTimeout:          1 * time.Second,
	}, collector)

	apiURL, _ := url.Parse(apiBackend.URL)
	webURL, _ := url.Parse(webBackend.URL)

	// Route 1: /api with StripPrefix = true
	router.RegisterRouteBackend("app.local", "/api", true, 0, apiURL)
	// Route 2: /web with StripPrefix = false
	router.RegisterRouteBackend("app.local", "/web", false, 0, webURL)

	// 1. Test /api/v1/users -> should strip /api -> backend receives /v1/users, prefix /api
	req1 := httptest.NewRequest(http.MethodGet, "http://app.local/api/v1/users", nil)
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for /api/v1/users, got %d", rec1.Code)
	}
	if gotPath, ok := receivedAPIPath.Load().(string); !ok || gotPath != "/v1/users" {
		t.Fatalf("Expected stripped path /v1/users at backend, got %q", gotPath)
	}
	if gotPrefix, ok := receivedAPIPrefix.Load().(string); !ok || gotPrefix != "/api" {
		t.Fatalf("Expected X-Forwarded-Prefix /api at backend, got %q", gotPrefix)
	}

	// 2. Test exact /api -> should strip /api -> backend receives / (default non-empty root)
	req2 := httptest.NewRequest(http.MethodGet, "http://app.local/api", nil)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for /api, got %d", rec2.Code)
	}
	if gotPath, ok := receivedAPIPath.Load().(string); !ok || gotPath != "/" {
		t.Fatalf("Expected stripped path / at backend for exact prefix, got %q", gotPath)
	}

	// 3. Test /web/dashboard -> StripPrefix = false -> backend receives /web/dashboard
	req3 := httptest.NewRequest(http.MethodGet, "http://app.local/web/dashboard", nil)
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for /web/dashboard, got %d", rec3.Code)
	}
	if gotPath, ok := receivedWebPath.Load().(string); !ok || gotPath != "/web/dashboard" {
		t.Fatalf("Expected preserved path /web/dashboard at backend, got %q", gotPath)
	}
	if gotPrefix, ok := receivedWebPrefix.Load().(string); !ok || gotPrefix != "/web" {
		t.Fatalf("Expected X-Forwarded-Prefix /web at backend, got %q", gotPrefix)
	}

	// 4. Test unmapped path: /other -> strict zero-fallback -> HTTP 404 Not Found
	req4 := httptest.NewRequest(http.MethodGet, "http://app.local/other", nil)
	rec4 := httptest.NewRecorder()
	router.ServeHTTP(rec4, req4)

	if rec4.Code != http.StatusNotFound {
		t.Fatalf("Expected strict zero-fallback 404 for unmapped path /other, got %d", rec4.Code)
	}

	// 5. Test segment boundary mismatch: /apidocs -> should NOT match /api -> HTTP 404 Not Found
	req5 := httptest.NewRequest(http.MethodGet, "http://app.local/apidocs", nil)
	rec5 := httptest.NewRecorder()
	router.ServeHTTP(rec5, req5)

	if rec5.Code != http.StatusNotFound {
		t.Fatalf("Expected strict zero-fallback 404 for segment boundary mismatch /apidocs, got %d", rec5.Code)
	}
}
