package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"os"

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

	// Reverse Proxy Catch-all routing
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		proxyRouter.ServeHTTP(w, req)
	})

	return r
}

