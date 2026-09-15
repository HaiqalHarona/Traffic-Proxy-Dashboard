package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/discovery"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/metrics"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/proxy"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/ui"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// SetupRouter configures the Chi router with middleware, static files, SSE telemetry, and catch-all proxy routing.
func SetupRouter(collector *metrics.Collector, proxyRouter *proxy.Router, dockerProvider *discovery.DockerProvider) *chi.Mux {
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
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		http.ServeFileFS(w, req, subFS, "index.html")
	})

	// Server-Sent Events (SSE) Telemetry Stream
	r.Get("/api/events", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-req.Context().Done():
				return
			case <-ticker.C:
				var serviceCount int
				if dockerProvider != nil {
					if services, err := dockerProvider.Services(); err == nil {
						serviceCount = len(services)
					}
				}

				snapshot := collector.Snapshot(serviceCount)

				// 1. Send HTMX Out-of-Band (OOB) HTML snippet for DOM swaps
				oobHTML := fmt.Sprintf(
					`<div id="metric-total-requests" hx-swap-oob="outerHTML" class="text-3xl font-bold mt-2 font-mono text-white">%d</div>`+
						`<div id="metric-active-concurrency" hx-swap-oob="outerHTML" class="text-3xl font-bold mt-2 font-mono text-brand-500">%d</div>`+
						`<div id="metric-discovered-services" hx-swap-oob="outerHTML" class="text-3xl font-bold mt-2 font-mono text-sky-400">%d</div>`+
						`<div id="metric-queued-requests" hx-swap-oob="outerHTML" class="text-3xl font-bold mt-2 font-mono text-amber-400">%d</div>`,
					snapshot.TotalRequests, snapshot.ActiveConcurrency, snapshot.DiscoveredServices, snapshot.QueuedRequests,
				)
				fmt.Fprintf(w, "event: metrics\ndata: %s\n\n", oobHTML)

				// 2. Send custom telemetry event for Chart.js dataset updates
				jsonPayload, _ := json.Marshal(snapshot)
				fmt.Fprintf(w, "event: telemetry\ndata: %s\n\n", string(jsonPayload))

				flusher.Flush()
			}
		}
	})

	// Reverse Proxy Catch-all routing
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		proxyRouter.ServeHTTP(w, req)
	})

	return r
}
