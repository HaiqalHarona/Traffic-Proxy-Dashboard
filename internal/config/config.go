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
	Environment           string
	Port                  string
	DockerHost            string
	MaxConcurrentRequests int64
	QueueTimeout          time.Duration
	DockerPollInterval    time.Duration
	LogLevel              slog.Level
}

// IsDevelopment returns true if running under the DEVELOPMENT profile.
func (c Config) IsDevelopment() bool {
	return strings.EqualFold(strings.TrimSpace(c.Environment), "DEVELOPMENT")
}

// Load reads configuration parameters directly from environment variables with zero fallback defaults.
func Load() Config {
	var cfg Config

	cfg.Environment = strings.TrimSpace(os.Getenv("ENVIRONMENT"))

	port := strings.TrimSpace(os.Getenv("PROXY_PORT"))
	if port != "" && !strings.HasPrefix(port, ":") {
		cfg.Port = ":" + port
	} else {
		cfg.Port = port
	}

	cfg.DockerHost = strings.TrimSpace(os.Getenv("DOCKER_HOST"))

	if maxConcStr := os.Getenv("PROXY_MAX_CONCURRENT"); maxConcStr != "" {
		if val, err := strconv.ParseInt(maxConcStr, 10, 64); err == nil {
			cfg.MaxConcurrentRequests = val
		}
	}

	if queueTimeoutStr := os.Getenv("PROXY_QUEUE_TIMEOUT"); queueTimeoutStr != "" {
		if val, err := time.ParseDuration(queueTimeoutStr); err == nil {
			cfg.QueueTimeout = val
		}
	}

	if pollIntervalStr := os.Getenv("DOCKER_POLL_INTERVAL"); pollIntervalStr != "" {
		if val, err := time.ParseDuration(pollIntervalStr); err == nil {
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
		case "INFO":
			cfg.LogLevel = slog.LevelInfo
		}
	}

	return cfg
}
