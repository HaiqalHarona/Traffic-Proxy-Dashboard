package unit_test

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/config"
)

func TestConfig_Load_ZeroFallbacks(t *testing.T) {
	// Clear all config environment variables
	os.Unsetenv("ENVIRONMENT")
	os.Unsetenv("ENV")
	os.Unsetenv("PROXY_PORT")
	os.Unsetenv("DOCKER_HOST")
	os.Unsetenv("PROXY_MAX_CONCURRENT")
	os.Unsetenv("PROXY_QUEUE_TIMEOUT")
	os.Unsetenv("DOCKER_POLL_INTERVAL")
	os.Unsetenv("LOG_LEVEL")

	cfg := config.Load()

	if cfg.Environment != "" {
		t.Fatalf("Expected empty Environment with zero fallbacks, got %q", cfg.Environment)
	}
	if cfg.IsDevelopment() {
		t.Fatalf("Expected IsDevelopment() false when ENVIRONMENT is empty")
	}
	if cfg.Port != "" {
		t.Fatalf("Expected empty Port with zero fallbacks, got %q", cfg.Port)
	}
	if cfg.DockerHost != "" {
		t.Fatalf("Expected empty DockerHost with zero fallbacks, got %q", cfg.DockerHost)
	}
	if cfg.MaxConcurrentRequests != 0 {
		t.Fatalf("Expected 0 MaxConcurrentRequests with zero fallbacks, got %d", cfg.MaxConcurrentRequests)
	}
	if cfg.QueueTimeout != 0 {
		t.Fatalf("Expected 0 QueueTimeout with zero fallbacks, got %v", cfg.QueueTimeout)
	}
	if cfg.DockerPollInterval != 0 {
		t.Fatalf("Expected 0 DockerPollInterval with zero fallbacks, got %v", cfg.DockerPollInterval)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("Expected LevelInfo (0) zero value for LogLevel, got %v", cfg.LogLevel)
	}
}

func TestConfig_Load_StrictEnvironment(t *testing.T) {
	t.Setenv("ENVIRONMENT", "DEVELOPMENT")
	t.Setenv("PROXY_PORT", "8080")
	t.Setenv("DOCKER_HOST", "unix:///custom/docker.sock")
	t.Setenv("PROXY_MAX_CONCURRENT", "50")
	t.Setenv("PROXY_QUEUE_TIMEOUT", "500ms")
	t.Setenv("DOCKER_POLL_INTERVAL", "2s")
	t.Setenv("LOG_LEVEL", "DEBUG")

	cfg := config.Load()

	if cfg.Environment != "DEVELOPMENT" {
		t.Fatalf("Expected Environment DEVELOPMENT, got %s", cfg.Environment)
	}
	if !cfg.IsDevelopment() {
		t.Fatalf("Expected IsDevelopment() true")
	}
	if cfg.Port != ":8080" {
		t.Fatalf("Expected Port :8080, got %s", cfg.Port)
	}
	if cfg.DockerHost != "unix:///custom/docker.sock" {
		t.Fatalf("Expected DockerHost unix:///custom/docker.sock, got %s", cfg.DockerHost)
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

func TestConfig_Load_InvalidValuesZero(t *testing.T) {
	t.Setenv("PROXY_MAX_CONCURRENT", "invalid")
	t.Setenv("PROXY_QUEUE_TIMEOUT", "invalid")
	t.Setenv("DOCKER_POLL_INTERVAL", "not-a-duration")
	t.Setenv("LOG_LEVEL", "UNKNOWN")

	cfg := config.Load()

	if cfg.MaxConcurrentRequests != 0 {
		t.Fatalf("Expected 0 MaxConcurrentRequests for invalid input with no fallback, got %d", cfg.MaxConcurrentRequests)
	}
	if cfg.QueueTimeout != 0 {
		t.Fatalf("Expected 0 QueueTimeout for invalid input with no fallback, got %v", cfg.QueueTimeout)
	}
	if cfg.DockerPollInterval != 0 {
		t.Fatalf("Expected 0 DockerPollInterval for invalid input with no fallback, got %v", cfg.DockerPollInterval)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("Expected 0 (LevelInfo) for unknown LogLevel, got %v", cfg.LogLevel)
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
	if !cfg.IsDevelopment() {
		t.Fatalf("Expected case-insensitive DEVELOPMENT mode")
	}

	// Test 3: No fallback via ENV var
	os.Unsetenv("ENVIRONMENT")
	t.Setenv("ENV", "DEVELOPMENT")
	cfg = config.Load()
	if cfg.Environment != "" || cfg.IsDevelopment() {
		t.Fatalf("Expected no ENV fallback when ENVIRONMENT is unset, got %q", cfg.Environment)
	}

	// Test 4: Explicit PRODUCTION
	t.Setenv("ENVIRONMENT", "PRODUCTION")
	cfg = config.Load()
	if cfg.Environment != "PRODUCTION" || cfg.IsDevelopment() {
		t.Fatalf("Expected PRODUCTION mode, got %s", cfg.Environment)
	}
}
