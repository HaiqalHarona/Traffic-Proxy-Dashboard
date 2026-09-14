---
name: proxy-architecture
description: Architecture specifications, concurrency control patterns, Docker discovery mechanisms, and telemetry conventions for TrafficProxy Edge Gateway.
---

# TrafficProxy Architecture & Development Guide

Use when adding features, modifying routing or discovery logic, or refactoring TrafficProxy Edge Gateway.

## Architectural Components

### 1. Reverse Proxy & Concurrency Throttling (`internal/proxy`)
- Uses `golang.org/x/sync/semaphore` to cap concurrent requests (`MaxConcurrentRequests: 5000`).
- Context timeout on queue wait (`QueueTimeout: 3s`). Returns HTTP `503 Service Unavailable` on timeout.
- Normalizes incoming `Host` headers (stripping port, lowercase).
- Returns HTTP `502 Bad Gateway` if no backend rule matches.
- Uses `net/http/httputil.ReverseProxy` with customized persistent transport pooling.

### 2. Service Discovery (`internal/discovery`)
- Abstract `Provider` interface: `Name()`, `Start(ctx)`, `Services()`, `Subscribe()`.
- `DockerProvider` polls `/var/run/docker.sock` every 5 seconds.
- Filter labels:
  - `traffic-proxy.enable=true`
  - `traffic-proxy.rule=<host>`
  - `traffic-proxy.port=<int>` (optional; falls back to exposed port or 80)
- Dynamically updates active backends via channel subscription.

### 3. Telemetry Engine (`internal/metrics`)
- Atomic counters: `TotalRequests` (`atomic.Uint64`), `ActiveConcurrency` (`atomic.Int64`), `QueuedRequests` (`atomic.Int64`).
- Lock-free, power-of-two capacity ring buffer (`TelemetryRingBuffer`) storing `MetricSnapshot` records.

### 4. Embedded Frontend & SSE (`cmd/proxy`, `ui/`)
- Embeds static assets using Go `//go:embed static/*`.
- Mounts Chi router with recovery and structured JSON logging (`slog`).
- Emits dual SSE streams on `/api/events` every second:
  1. `event: metrics`: Out-of-band HTML fragment swapping DOM counters for HTMX.
  2. `event: telemetry`: JSON payload driving Chart.js time-series graphs.

### 5. Packaging & Deployment
- Multi-stage Docker build targeting `scratch` (< 20MB).
- Non-root user `65534:65534` (`nobody`).
- Read-only root filesystem with read-only Docker socket mount (`/var/run/docker.sock:ro`).
