package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/config"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/discovery"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/metrics"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/proxy"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/server"
)

func main() {
	cfg := config.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	collector := metrics.NewCollector()
	router := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: cfg.MaxConcurrentRequests,
		QueueTimeout:          cfg.QueueTimeout,
	}, collector)

	// Docker discovery setup
	dockerProvider, err := discovery.NewDockerProvider(cfg.DockerPollInterval)
	if err != nil {
		slog.Warn("Docker provider initialization failed (continuing without docker sock)", "error", err)
	} else {
		// Start provider background scanner
		go func() {
			if startErr := dockerProvider.Start(ctx); startErr != nil && ctx.Err() == nil {
				slog.Error("Docker provider execution stopped", "error", startErr)
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
					router.UpdateBackends(targets)
					healthyAndEnabled := 0
					for _, target := range targets {
						if target.Healthy && target.Enabled && target.TargetURL != nil {
							healthyAndEnabled++
						}
					}
					slog.Info("Proxy backends updated",
						"discovered_count", len(targets),
						"active_hosts", len(router.Backends()),
						"healthy_and_enabled", healthyAndEnabled)
				}
			}
		}()
	}

	r := server.SetupRouter(collector, router, dockerProvider, cfg)

	server := &http.Server{
		Addr:         cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("SanProx Edge Gateway running", "addr", cfg.Port, "environment", cfg.Environment)
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
