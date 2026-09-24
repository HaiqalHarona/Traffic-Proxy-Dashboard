package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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

	// Determine static asset filesystem (live disk in DEVELOPMENT if present, embedded fallback)
	var staticFS fs.FS
	localStaticDir := ""
	if cfg.IsDevelopment() {
		localStaticDir = findLocalStaticDir()
	}

	if localStaticDir != "" {
		slog.Info("Serving UI assets live from disk (hot reload enabled)", "path", localStaticDir)
		staticFS = os.DirFS(localStaticDir)
	} else {
		subFS, subErr := fs.Sub(ui.Assets, "static")
		if subErr != nil {
			slog.Error("Failed to locate embedded UI assets", "error", subErr)
			os.Exit(1)
		}
		staticFS = subFS
	}

	fileServer := http.FileServer(http.FS(staticFS))
	if cfg.IsDevelopment() && localStaticDir != "" {
		r.Handle("/static/*", http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			fileServer.ServeHTTP(w, req)
		})))
	} else {
		r.Handle("/static/*", http.StripPrefix("/static/", fileServer))
	}

	renderIndex := func() ([]byte, error) {
		raw, err := fs.ReadFile(staticFS, "index.html")
		if err != nil {
			return nil, err
		}
		rendered := bytes.ReplaceAll(raw, []byte("__SANPROX_ENVIRONMENT__"), []byte(cfg.Environment))
		rendered = bytes.ReplaceAll(rendered, []byte("__SANPROX_PORT__"), []byte(cfg.Port))
		rendered = bytes.ReplaceAll(rendered, []byte("__SANPROX_DOCKER_SOCKET__"), []byte(cfg.DockerHost))
		return rendered, nil
	}

	cachedIndex, err := renderIndex()
	if err != nil && (localStaticDir == "" || !cfg.IsDevelopment()) {
		slog.Error("Failed to render index.html", "error", err)
		os.Exit(1)
	}

	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if cfg.IsDevelopment() && localStaticDir != "" {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			content, renderErr := renderIndex()
			if renderErr != nil {
				http.Error(w, "Failed to load index.html from disk", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(content)
			return
		}
		_, _ = w.Write(cachedIndex)
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
				if cfg.IsDevelopment() {
					statusDotClass = "w-2.5 h-2.5 rounded-full bg-emerald-500 ring-4 ring-emerald-500/20"
					if serviceCount == 0 {
						statusTitle = "Development gateway active (0 containers)"
					} else {
						statusTitle = fmt.Sprintf("Development gateway active (%d/%d containers)", healthyCount, serviceCount)
					}
				} else {
					statusDotClass = "w-2.5 h-2.5 rounded-full bg-rose-500 ring-4 ring-rose-500/20 animate-pulse"
					if serviceCount == 0 {
						statusTitle = "No active backend containers"
					} else {
						statusTitle = fmt.Sprintf("%d/%d containers down", serviceCount-healthyCount, serviceCount)
					}
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

// findLocalStaticDir inspects candidate relative paths to locate local UI static assets during development.
func findLocalStaticDir() string {
	candidates := []string{"ui/static", "./ui/static", "../ui/static", "../../ui/static"}
	for _, c := range candidates {
		info, err := os.Stat(c)
		if err == nil && info.IsDir() {
			if _, err := os.Stat(filepath.Join(c, "index.html")); err == nil {
				return c
			}
		}
	}
	return ""
}
