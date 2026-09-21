package unit_test

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/config"
)

func TestConfig_LoadDefaults(t *testing.T) {
	// Clear any potential existing env vars
	os.Unsetenv("ENVIRONMENT")
	os.Unsetenv("ENV")
	os.Unsetenv("PROXY_PORT")
	os.Unsetenv("PROXY_MAX_CONCURRENT")
	os.Unsetenv("PROXY_QUEUE_TIMEOUT")
	os.Unsetenv("DOCKER_POLL_INTERVAL")
	os.Unsetenv("LOG_LEVEL")

	cfg := config.Load()

	if cfg.Environment != "PRODUCTION" {
		t.Fatalf("Expected default Environment PRODUCTION, got %s", cfg.Environment)
	}
	if cfg.IsDevelopment() {
		t.Fatalf("Expected IsDevelopment() to be false by default")
	}
	if cfg.Port != ":80" {
		t.Fatalf("Expected default Port :80, got %s", cfg.Port)
	}
	if cfg.MaxConcurrentRequests != 5000 {
		t.Fatalf("Expected default MaxConcurrentRequests 5000, got %d", cfg.MaxConcurrentRequests)
	}
	if cfg.QueueTimeout != 3*time.Second {
		t.Fatalf("Expected default QueueTimeout 3s, got %v", cfg.QueueTimeout)
	}
	if cfg.DockerPollInterval != 5*time.Second {
		t.Fatalf("Expected default DockerPollInterval 5s, got %v", cfg.DockerPollInterval)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("Expected default LogLevel LevelInfo, got %v", cfg.LogLevel)
	}
}

func TestConfig_LoadCustomEnv(t *testing.T) {
	t.Setenv("PROXY_PORT", "8080")
	t.Setenv("PROXY_MAX_CONCURRENT", "50")
	t.Setenv("PROXY_QUEUE_TIMEOUT", "500ms")
	t.Setenv("DOCKER_POLL_INTERVAL", "2s")
	t.Setenv("LOG_LEVEL", "DEBUG")

	cfg := config.Load()

	if cfg.Port != ":8080" {
		t.Fatalf("Expected Port :8080, got %s", cfg.Port)
	}
	if cfg.MaxConcurrentRequests != 50 {
		t.Fatalf("Expected MaxConcurrentRequests 50, got %d", cfg.MaxConcurrentRequests)
	}
	if cfg.QueueTimeout != 500*time.Millisecond {
		t.Fatalf("Expected QueueTimeout 500ms, got %v", cfg.QueueTimeout)
	}
	if cfg.DockerPollInterval != 2*time.Second {
		t.Fatalf("Expected DockerPollInterval 2s, got %v", cfg.DockerPollInterval)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("Expected LogLevel LevelDebug, got %v", cfg.LogLevel)
	}
}

func TestConfig_LoadInvalidValuesFallback(t *testing.T) {
	t.Setenv("PROXY_MAX_CONCURRENT", "invalid")
	t.Setenv("PROXY_QUEUE_TIMEOUT", "invalid")
	t.Setenv("DOCKER_POLL_INTERVAL", "-10s")
	t.Setenv("LOG_LEVEL", "UNKNOWN")

	cfg := config.Load()

	if cfg.MaxConcurrentRequests != 5000 {
		t.Fatalf("Expected fallback MaxConcurrentRequests 5000, got %d", cfg.MaxConcurrentRequests)
	}
	if cfg.QueueTimeout != 3*time.Second {
		t.Fatalf("Expected fallback QueueTimeout 3s, got %v", cfg.QueueTimeout)
	}
	if cfg.DockerPollInterval != 5*time.Second {
		t.Fatalf("Expected fallback DockerPollInterval 5s, got %v", cfg.DockerPollInterval)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("Expected fallback LogLevel LevelInfo, got %v", cfg.LogLevel)
	}
}

func TestConfig_EnvironmentVariations(t *testing.T) {
	// Test 1: DEVELOPMENT uppercase
	t.Setenv("ENVIRONMENT", "DEVELOPMENT")
	cfg := config.Load()
	if cfg.Environment != "DEVELOPMENT" || !cfg.IsDevelopment() {
		t.Fatalf("Expected DEVELOPMENT mode, got %s", cfg.Environment)
	}

	// Test 2: development lowercase
	t.Setenv("ENVIRONMENT", "development")
	cfg = config.Load()
	if cfg.Environment != "DEVELOPMENT" || !cfg.IsDevelopment() {
		t.Fatalf("Expected case-insensitive DEVELOPMENT mode, got %s", cfg.Environment)
	}

	// Test 3: Fallback via ENV var
	os.Unsetenv("ENVIRONMENT")
	t.Setenv("ENV", "DEVELOPMENT")
	cfg = config.Load()
	if cfg.Environment != "DEVELOPMENT" || !cfg.IsDevelopment() {
		t.Fatalf("Expected ENV fallback to DEVELOPMENT mode, got %s", cfg.Environment)
	}

	// Test 4: Explicit PRODUCTION
	t.Setenv("ENVIRONMENT", "PRODUCTION")
	cfg = config.Load()
	if cfg.Environment != "PRODUCTION" || cfg.IsDevelopment() {
		t.Fatalf("Expected PRODUCTION mode, got %s", cfg.Environment)
	}
}

