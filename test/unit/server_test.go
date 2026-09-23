package unit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/config"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/discovery"
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

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
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

	body := rec.Body.String()
	if !strings.Contains(body, ": connected") {
		t.Fatalf("Expected : connected handshake in SSE stream, got %q", body)
	}
	if !strings.Contains(body, "event: metrics") {
		t.Fatalf("Expected event: metrics in SSE stream, got %q", body)
	}
	if !strings.Contains(body, "gateway-status-dot") {
		t.Fatalf("Expected gateway-status-dot OOB swap in SSE stream, got %q", body)
	}
	if !strings.Contains(body, "event: telemetry") {
		t.Fatalf("Expected event: telemetry in SSE stream, got %q", body)
	}
	if !strings.Contains(body, "system_healthy") {
		t.Fatalf("Expected system_healthy in SSE telemetry JSON, got %q", body)
	}
}

func TestSetupRouter_BrandingAndConfig(t *testing.T) {
	t.Parallel()

	collector := metrics.NewCollector()
	proxyRouter := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 10,
		QueueTimeout:          1 * time.Second,
	}, collector)

	cfg := config.Config{
		Environment: "DEVELOPMENT",
	}

	r := server.SetupRouter(collector, proxyRouter, nil, cfg)

	// Check / serves SanProx branding and injected environment
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "SanProx") {
		t.Fatalf("Expected 'SanProx' branding in root HTML response")
	}
	if strings.Contains(body, "TrafficProxy") {
		t.Fatalf("Found leftover 'TrafficProxy' in root HTML response")
	}
	if !strings.Contains(body, `id="gateway-status-dot"`) {
		t.Fatalf("Expected gateway-status-dot in root HTML response")
	}
	if !strings.Contains(body, "DEVELOPMENT") {
		t.Fatalf("Expected injected 'DEVELOPMENT' in root HTML response")
	}

	// Check /api/config
	reqCfg := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	recCfg := httptest.NewRecorder()
	r.ServeHTTP(recCfg, reqCfg)

	if recCfg.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from /api/config, got %d", recCfg.Code)
	}
	if !strings.Contains(recCfg.Body.String(), `"app_name":"SanProx"`) {
		t.Fatalf("Expected app_name SanProx in /api/config response, got %s", recCfg.Body.String())
	}
	if !strings.Contains(recCfg.Body.String(), `"is_development":true`) {
		t.Fatalf("Expected is_development true in /api/config response, got %s", recCfg.Body.String())
	}
}

func TestSetupRouter_DevEndpoints_DevelopmentMode(t *testing.T) {
	t.Parallel()

	collector := metrics.NewCollector()
	proxyRouter := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 10,
		QueueTimeout:          1 * time.Second,
	}, collector)

	cfg := config.Config{
		Environment: "DEVELOPMENT",
	}

	r := server.SetupRouter(collector, proxyRouter, nil, cfg)

	// 1. Test POST /api/dev/seed
	reqSeed := httptest.NewRequest(http.MethodPost, "/api/dev/seed?count=10&concurrency=2", nil)
	recSeed := httptest.NewRecorder()
	r.ServeHTTP(recSeed, reqSeed)

	if recSeed.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from /api/dev/seed, got %d: %s", recSeed.Code, recSeed.Body.String())
	}
	if !strings.Contains(recSeed.Body.String(), `"status":"success"`) {
		t.Fatalf("Expected success in seed response, got %s", recSeed.Body.String())
	}

	// 2. Test POST /api/dev/stress
	reqStress := httptest.NewRequest(http.MethodPost, "/api/dev/stress?count=4&concurrency=2&hold_ms=5", nil)
	recStress := httptest.NewRecorder()
	r.ServeHTTP(recStress, reqStress)

	if recStress.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from /api/dev/stress, got %d", recStress.Code)
	}

	// 3. Test GET /api/dev/debug-state
	reqDebug := httptest.NewRequest(http.MethodGet, "/api/dev/debug-state", nil)
	recDebug := httptest.NewRecorder()
	r.ServeHTTP(recDebug, reqDebug)

	if recDebug.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from /api/dev/debug-state, got %d", recDebug.Code)
	}
	if !strings.Contains(recDebug.Body.String(), `"is_development":true`) {
		t.Fatalf("Expected is_development in debug-state response")
	}

	// 4. Test POST /api/dev/reset-metrics
	reqReset := httptest.NewRequest(http.MethodPost, "/api/dev/reset-metrics", nil)
	recReset := httptest.NewRecorder()
	r.ServeHTTP(recReset, reqReset)

	if recReset.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from /api/dev/reset-metrics, got %d", recReset.Code)
	}
	if collector.TotalRequests.Load() != 0 {
		t.Fatalf("Expected TotalRequests to be 0 after reset, got %d", collector.TotalRequests.Load())
	}
}

func TestSetupRouter_DevEndpoints_ProductionForbidden(t *testing.T) {
	t.Parallel()

	collector := metrics.NewCollector()
	proxyRouter := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 10,
		QueueTimeout:          1 * time.Second,
	}, collector)

	cfg := config.Config{
		Environment: "PRODUCTION",
	}

	r := server.SetupRouter(collector, proxyRouter, nil, cfg)

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/dev/seed"},
		{http.MethodPost, "/api/dev/stress"},
		{http.MethodPost, "/api/dev/reset-metrics"},
		{http.MethodGet, "/api/dev/debug-state"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("Expected 403 Forbidden for %s %s in PRODUCTION, got %d", ep.method, ep.path, rec.Code)
		}
	}
}

func TestSetupRouter_DevSeed_DynamicDockerSampling(t *testing.T) {
	t.Parallel()

	collector := metrics.NewCollector()
	proxyRouter := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 10,
		QueueTimeout:          1 * time.Second,
	}, collector)

	cfg := config.Config{
		Environment: "DEVELOPMENT",
	}

	dockerProvider := discovery.NewDockerProviderWithClient(nil, 5*time.Second)
	dockerProvider.SetServices([]discovery.ServiceTarget{
		{
			HostRule: "dynamic-whoami.local",
			Healthy:  true,
		},
		{
			HostRule: "dynamic-api.local",
			Healthy:  true,
		},
	})

	r := server.SetupRouter(collector, proxyRouter, dockerProvider, cfg)

	reqSeed := httptest.NewRequest(http.MethodPost, "/api/dev/seed?count=10&concurrency=2", nil)
	recSeed := httptest.NewRecorder()
	r.ServeHTTP(recSeed, reqSeed)

	if recSeed.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from /api/dev/seed, got %d: %s", recSeed.Code, recSeed.Body.String())
	}

	body := recSeed.Body.String()
	if !strings.Contains(body, "dynamic-whoami.local") || !strings.Contains(body, "dynamic-api.local") {
		t.Fatalf("Expected dynamic docker services in sampled_hosts, got: %s", body)
	}
}
