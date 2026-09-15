package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config encapsulates gateway runtime parameters loaded from environment variables.
type Config struct {
	Port                  string
	MaxConcurrentRequests int64
	QueueTimeout          time.Duration
	DockerPollInterval    time.Duration
	LogLevel              slog.Level
}

// Load reads configuration parameters from environment variables with sensible defaults.
func Load() Config {
	cfg := Config{
		Port:                  ":80",
		MaxConcurrentRequests: 5000,
		QueueTimeout:          3 * time.Second,
		DockerPollInterval:    5 * time.Second,
		LogLevel:              slog.LevelInfo,
	}

	if port := os.Getenv("PROXY_PORT"); port != "" {
		if !strings.HasPrefix(port, ":") {
			cfg.Port = ":" + port
		} else {
			cfg.Port = port
		}
	}

	if maxConcStr := os.Getenv("PROXY_MAX_CONCURRENT"); maxConcStr != "" {
		if val, err := strconv.ParseInt(maxConcStr, 10, 64); err == nil && val > 0 {
			cfg.MaxConcurrentRequests = val
		}
	}

	if queueTimeoutStr := os.Getenv("PROXY_QUEUE_TIMEOUT"); queueTimeoutStr != "" {
		if val, err := time.ParseDuration(queueTimeoutStr); err == nil && val > 0 {
			cfg.QueueTimeout = val
		}
	}

	if pollIntervalStr := os.Getenv("DOCKER_POLL_INTERVAL"); pollIntervalStr != "" {
		if val, err := time.ParseDuration(pollIntervalStr); err == nil && val > 0 {
			cfg.DockerPollInterval = val
		}
	}

	if logLevelStr := os.Getenv("LOG_LEVEL"); logLevelStr != "" {
		switch strings.ToUpper(strings.TrimSpace(logLevelStr)) {
		case "DEBUG":
			cfg.LogLevel = slog.LevelDebug
		case "WARN", "WARNING":
			cfg.LogLevel = slog.LevelWarn
		case "ERROR":
			cfg.LogLevel = slog.LevelError
		default:
			cfg.LogLevel = slog.LevelInfo
		}
	}

	return cfg
}
