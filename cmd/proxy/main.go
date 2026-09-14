package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/discovery"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/metrics"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/proxy"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/ui"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	collector := metrics.NewCollector()
	router := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 5000,
		QueueTimeout:          3 * time.Second,
	}, collector)

	// Docker discovery setup
	dockerProvider, err := discovery.NewDockerProvider(5 * time.Second)
	if err != nil {
		slog.Warn("Docker provider initialization failed (continuing without docker sock)", "error", err)
	} else {
		// Start provider background scanner
		go func() {
			if err := dockerProvider.Start(ctx); err != nil && ctx.Err() == nil {
				slog.Error("Docker provider execution stopped", "error", err)
			}
		}()

		// Subscribe to discovered service targets and update proxy routing table dynamically
		go func() {
			sub := dockerProvider.Subscribe()
			for {
				select {
				case <-ctx.Done():
					return
				case targets := <-sub:
					routes := make(map[string]*url.URL, len(targets))
					for _, target := range targets {
						if target.Healthy && target.TargetURL != nil {
							routes[target.HostRule] = target.TargetURL
						}
					}
					router.UpdateBackends(routes)
					slog.Info("Proxy backends updated", "discovered_count", len(targets), "active_routes", len(routes))
				}
			}
		}()
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Embedded UI File Server setup
	subFS, err := fs.Sub(ui.Assets, "static")
	if err != nil {
		slog.Error("Failed to locate embedded UI assets", "error", err)
		os.Exit(1)
	}
	fileServer := http.FileServer(http.FS(subFS))
	r.Handle("/static/*", http.StripPrefix("/static/", fileServer))
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = "/index.html"
		fileServer.ServeHTTP(w, r)
	})

	// Server-Sent Events (SSE) Telemetry Stream
	r.Get("/api/events", func(w http.ResponseWriter, r *http.Request) {
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
			case <-r.Context().Done():
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
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		router.ServeHTTP(w, r)
	})

	server := &http.Server{
		Addr:         ":80",
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("TrafficProxy Edge Gateway running", "addr", ":80")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("Shutting down gateway gracefully...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}
	slog.Info("Gateway stopped cleanly")
}
