package server

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/config"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/discovery"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/metrics"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/proxy"
)

// ConfigResponse returns gateway environment and runtime configuration for the UI.
type ConfigResponse struct {
	AppName       string `json:"app_name"`
	Environment   string `json:"environment"`
	IsDevelopment bool   `json:"is_development"`
	Version       string `json:"version"`
	Port          string `json:"port"`
	DockerSocket  string `json:"docker_socket"`
}

// SeedResult records the summary of a synthetic traffic run.
type SeedResult struct {
	Status        string   `json:"status"`
	TotalSent     uint64   `json:"total_sent"`
	Status200     uint64   `json:"status_200"`
	Status502     uint64   `json:"status_502"`
	Status503     uint64   `json:"status_503"`
	StatusOther   uint64   `json:"status_other"`
	DurationMs    int64    `json:"duration_ms"`
	ThroughputRPS string   `json:"throughput_rps"`
	SampledHosts  []string `json:"sampled_hosts,omitempty"`
}

func handleConfig(cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		resp := ConfigResponse{
			AppName:       "SanProx",
			Environment:   cfg.Environment,
			IsDevelopment: cfg.IsDevelopment(),
			Version:       "v1.0.0",
			Port:          cfg.Port,
			DockerSocket:  cfg.DockerHost,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleDevSeed(cfg config.Config, proxyRouter *proxy.Router, dockerProvider *discovery.DockerProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if !cfg.IsDevelopment() {
			http.Error(w, `{"error":"Forbidden: Dev features disabled in PRODUCTION"}`, http.StatusForbidden)
			return
		}

		count := 50
		concurrency := 10

		if cStr := req.URL.Query().Get("count"); cStr != "" {
			if val, err := strconv.Atoi(cStr); err == nil && val > 0 && val <= 5000 {
				count = val
			}
		}
		if concStr := req.URL.Query().Get("concurrency"); concStr != "" {
			if val, err := strconv.Atoi(concStr); err == nil && val > 0 && val <= 100 {
				concurrency = val
			}
		}

		res := executeTrafficSeed(proxyRouter, dockerProvider, count, concurrency)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}
}

func handleDevStress(cfg config.Config, proxyRouter *proxy.Router, dockerProvider *discovery.DockerProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if !cfg.IsDevelopment() {
			http.Error(w, `{"error":"Forbidden: Dev features disabled in PRODUCTION"}`, http.StatusForbidden)
			return
		}

		count := 100
		concurrency := 30
		holdMs := 150

		if cStr := req.URL.Query().Get("count"); cStr != "" {
			if val, err := strconv.Atoi(cStr); err == nil && val > 0 {
				count = val
			}
		}
		if concStr := req.URL.Query().Get("concurrency"); concStr != "" {
			if val, err := strconv.Atoi(concStr); err == nil && val > 0 {
				concurrency = val
			}
		}
		if hStr := req.URL.Query().Get("hold_ms"); hStr != "" {
			if val, err := strconv.Atoi(hStr); err == nil && val > 0 {
				holdMs = val
			}
		}

		res := executeStressTraffic(proxyRouter, dockerProvider, count, concurrency, holdMs)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}
}

func handleDevResetMetrics(cfg config.Config, collector *metrics.Collector) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if !cfg.IsDevelopment() {
			http.Error(w, `{"error":"Forbidden: Dev features disabled in PRODUCTION"}`, http.StatusForbidden)
			return
		}

		collector.Reset()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"message": "Telemetry metrics reset to 0",
		})
	}
}

func handleDevDebugState(cfg config.Config, collector *metrics.Collector, proxyRouter *proxy.Router, dockerProvider *discovery.DockerProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if !cfg.IsDevelopment() {
			http.Error(w, `{"error":"Forbidden: Dev features disabled in PRODUCTION"}`, http.StatusForbidden)
			return
		}

		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)

		var services []discovery.ServiceTarget
		if dockerProvider != nil {
			if s, err := dockerProvider.Services(); err == nil {
				services = s
			}
		}

		routes := []string{}
		if proxyRouter != nil {
			routes = proxyRouter.Backends()
		}

		data := map[string]interface{}{
			"environment":         cfg.Environment,
			"is_development":      cfg.IsDevelopment(),
			"goroutines":          runtime.NumGoroutine(),
			"mem_alloc_kb":        mem.Alloc / 1024,
			"total_requests":      collector.TotalRequests.Load(),
			"active_concurrency":  collector.ActiveConcurrency.Load(),
			"queued_requests":     collector.QueuedRequests.Load(),
			"registered_routes":   routes,
			"discovered_services": len(services),
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(data)
	}
}

func executeTrafficSeed(proxyRouter *proxy.Router, dockerProvider *discovery.DockerProvider, count int, concurrency int) SeedResult {
	if count <= 0 {
		count = 50
	}
	if concurrency <= 0 {
		concurrency = 5
	}
	if concurrency > count {
		concurrency = count
	}

	var s SeedResult
	var totalSent, status200, status502, status503, statusOther atomic.Uint64

	// Dynamically sample hosts from active services discovered via Docker
	var hosts []string
	if dockerProvider != nil {
		if services, err := dockerProvider.Services(); err == nil {
			for _, svc := range services {
				if svc.HostRule != "" && svc.Healthy {
					hosts = append(hosts, svc.HostRule)
				}
			}
			// If none healthy, fallback to any discovered rule
			if len(hosts) == 0 {
				for _, svc := range services {
					if svc.HostRule != "" {
						hosts = append(hosts, svc.HostRule)
					}
				}
			}
		}
	}

	// Fallback to proxy router registered backends
	if len(hosts) == 0 && proxyRouter != nil {
		hosts = proxyRouter.Backends()
	}

	// Fallback mock hosts if no discovered or registered backends are present
	if len(hosts) == 0 {
		hosts = []string{"app.local", "app.local", "app.local", "slow.local", "unknown.local"}
	}

	s.SampledHosts = hosts
	reqsPerWorker := count / concurrency
	remainder := count % concurrency

	var wg sync.WaitGroup
	start := time.Now()

	for i := 0; i < concurrency; i++ {
		workerReqs := reqsPerWorker
		if i == 0 {
			workerReqs += remainder
		}

		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < n; j++ {
				targetHost := hosts[rand.Intn(len(hosts))]
				req := httptest.NewRequest(http.MethodGet, "http://"+targetHost+"/", nil)
				req.Host = targetHost
				rec := httptest.NewRecorder()

				if proxyRouter != nil {
					proxyRouter.ServeHTTP(rec, req)
				}
				totalSent.Add(1)

				switch rec.Code {
				case http.StatusOK:
					status200.Add(1)
				case http.StatusBadGateway:
					status502.Add(1)
				case http.StatusServiceUnavailable:
					status503.Add(1)
				default:
					statusOther.Add(1)
				}

				time.Sleep(time.Duration(2+rand.Intn(6)) * time.Millisecond)
			}
		}(workerReqs)
	}

	wg.Wait()
	duration := time.Since(start)

	s.Status = "success"
	s.TotalSent = totalSent.Load()
	s.Status200 = status200.Load()
	s.Status502 = status502.Load()
	s.Status503 = status503.Load()
	s.StatusOther = statusOther.Load()
	s.DurationMs = duration.Milliseconds()

	if duration.Seconds() > 0 {
		s.ThroughputRPS = fmt.Sprintf("%.1f", float64(s.TotalSent)/duration.Seconds())
	} else {
		s.ThroughputRPS = "0.0"
	}

	return s
}

func executeStressTraffic(proxyRouter *proxy.Router, dockerProvider *discovery.DockerProvider, count int, concurrency int, holdMs int) SeedResult {
	if count <= 0 {
		count = 100
	}
	if concurrency <= 0 {
		concurrency = 25
	}
	if holdMs <= 0 {
		holdMs = 100
	}

	var s SeedResult
	var totalSent, status200, status502, status503, statusOther atomic.Uint64

	stressHost := "slow.local"
	if dockerProvider != nil {
		if services, err := dockerProvider.Services(); err == nil {
			for _, svc := range services {
				if strings.Contains(svc.HostRule, "slow") {
					stressHost = svc.HostRule
					break
				}
			}
		}
	}
	s.SampledHosts = []string{stressHost}

	reqsPerWorker := count / concurrency
	remainder := count % concurrency

	var wg sync.WaitGroup
	start := time.Now()

	for i := 0; i < concurrency; i++ {
		workerReqs := reqsPerWorker
		if i == 0 {
			workerReqs += remainder
		}

		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < n; j++ {
				// Targeting slow.local causes queuing delay
				req := httptest.NewRequest(http.MethodGet, "http://slow.local/", nil)
				req.Host = "slow.local"
				rec := httptest.NewRecorder()

				if proxyRouter != nil {
					proxyRouter.ServeHTTP(rec, req)
				}
				totalSent.Add(1)

				switch rec.Code {
				case http.StatusOK:
					status200.Add(1)
				case http.StatusBadGateway:
					status502.Add(1)
				case http.StatusServiceUnavailable:
					status503.Add(1)
				default:
					statusOther.Add(1)
				}

				time.Sleep(time.Duration(holdMs) * time.Millisecond)
			}
		}(workerReqs)
	}

	wg.Wait()
	duration := time.Since(start)

	s.Status = "success"
	s.TotalSent = totalSent.Load()
	s.Status200 = status200.Load()
	s.Status502 = status502.Load()
	s.Status503 = status503.Load()
	s.StatusOther = statusOther.Load()
	s.DurationMs = duration.Milliseconds()

	if duration.Seconds() > 0 {
		s.ThroughputRPS = fmt.Sprintf("%.1f", float64(s.TotalSent)/duration.Seconds())
	} else {
		s.ThroughputRPS = "0.0"
	}

	return s
}
