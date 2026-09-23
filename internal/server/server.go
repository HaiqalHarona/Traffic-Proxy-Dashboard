package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/config"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/discovery"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/metrics"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/proxy"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/ui"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// SetupRouter configures the Chi router with middleware, static files, SSE telemetry, and catch-all proxy routing.
func SetupRouter(collector *metrics.Collector, proxyRouter *proxy.Router, dockerProvider *discovery.DockerProvider, optionalCfg ...config.Config) *chi.Mux {
	var cfg config.Config
	if len(optionalCfg) > 0 {
		cfg = optionalCfg[0]
	} else {
		cfg = config.Load()
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Embedded UI File Server setup
	subFS, subErr := fs.Sub(ui.Assets, "static")
	if subErr != nil {
		slog.Error("Failed to locate embedded UI assets", "error", subErr)
		os.Exit(1)
	}
	fileServer := http.FileServer(http.FS(subFS))
	r.Handle("/static/*", http.StripPrefix("/static/", fileServer))

	// Read embedded index.html and inject runtime environment
	rawIndex, err := fs.ReadFile(subFS, "index.html")
	if err != nil {
		slog.Error("Failed to read embedded index.html", "error", err)
		os.Exit(1)
	}
	indexContent := bytes.ReplaceAll(rawIndex, []byte("__SANPROX_ENVIRONMENT__"), []byte(cfg.Environment))

	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexContent)
	})

	// Runtime Configuration Endpoint
	r.Get("/api/config", handleConfig(cfg))

	// Development Debugging & Traffic Seeder Endpoints
	r.Post("/api/dev/seed", handleDevSeed(cfg, proxyRouter, dockerProvider))
	r.Post("/api/dev/stress", handleDevStress(cfg, proxyRouter, dockerProvider))
	r.Post("/api/dev/reset-metrics", handleDevResetMetrics(cfg, collector))
	r.Get("/api/dev/debug-state", handleDevDebugState(cfg, collector, proxyRouter, dockerProvider))

	// Server-Sent Events (SSE) Telemetry Stream
	r.Get("/api/events", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		// Immediate handshake flush so client recognizes connected state
		fmt.Fprintf(w, ": connected\n\n")
		flusher.Flush()

		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		sendSnapshot := func() {
			var serviceCount int
			var healthyCount int
			if dockerProvider != nil {
				if services, err := dockerProvider.Services(); err == nil {
					serviceCount = len(services)
					for _, s := range services {
						if s.Healthy {
							healthyCount++
						}
					}
				}
			}

			snapshot := collector.Snapshot(serviceCount, healthyCount)

			statusDotClass := "w-2.5 h-2.5 rounded-full bg-emerald-500 ring-4 ring-emerald-500/20"
			statusTitle := fmt.Sprintf("%d/%d healthy containers", healthyCount, serviceCount)
			if !snapshot.SystemHealthy {
				statusDotClass = "w-2.5 h-2.5 rounded-full bg-rose-500 ring-4 ring-rose-500/20 animate-pulse"
				if serviceCount == 0 {
					statusTitle = "No active backend containers"
				} else {
					statusTitle = fmt.Sprintf("%d/%d containers down", serviceCount-healthyCount, serviceCount)
				}
			}

			statusDotHTML := fmt.Sprintf(
				`<span id="gateway-status-dot" hx-swap-oob="outerHTML" class="%s" title="%s"></span>`,
				statusDotClass, statusTitle,
			)

			// 1. Send HTMX Out-of-Band (OOB) HTML snippet for DOM swaps
			oobHTML := fmt.Sprintf(
				`%s`+
					`<div id="metric-total-requests" hx-swap-oob="outerHTML" class="text-3xl font-bold font-mono text-white mt-2">%d</div>`+
					`<div id="metric-active-concurrency" hx-swap-oob="outerHTML" class="text-3xl font-bold font-mono text-emerald-400 mt-2">%d</div>`+
					`<div id="metric-discovered-services" hx-swap-oob="outerHTML" class="text-3xl font-bold font-mono text-sky-400 mt-2">%d</div>`+
					`<div id="metric-queued-requests" hx-swap-oob="outerHTML" class="text-3xl font-bold font-mono text-amber-400 mt-2">%d</div>`,
				statusDotHTML, snapshot.TotalRequests, snapshot.ActiveConcurrency, snapshot.DiscoveredServices, snapshot.QueuedRequests,
			)
			fmt.Fprintf(w, "event: metrics\ndata: %s\n\n", oobHTML)

			// 2. Send custom telemetry event for Chart.js dataset updates
			jsonPayload, _ := json.Marshal(snapshot)
			fmt.Fprintf(w, "event: telemetry\ndata: %s\n\n", string(jsonPayload))

			flusher.Flush()
		}

		sendSnapshot()

		for {
			select {
			case <-req.Context().Done():
				return
			case <-ticker.C:
				sendSnapshot()
			}
		}
	})

	// Reverse Proxy Catch-all routing
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		proxyRouter.ServeHTTP(w, req)
	})

	return r
}
