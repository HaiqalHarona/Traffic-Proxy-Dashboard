# TrafficProxy Edge Gateway — Milestone Overview & Feature Roadmap

A comprehensive, technical implementation roadmap detailing current system status, gap analysis, and phased development milestones for TrafficProxy Edge Gateway.

---

## Current Architecture Baseline

- **Core Proxy Engine**: Single-host reverse proxy (`net/http/httputil.ReverseProxy`) with persistent connection pooling.
- **Concurrency Throttling**: Global concurrency gating via `golang.org/x/sync/semaphore` with a 3-second queue acquisition timeout.
- **Service Discovery**: Docker Engine API polling over `/var/run/docker.sock` every 5 seconds targeting `traffic-proxy.*` container labels.
- **Telemetry**: Global lock-free ring buffer (1024 slots) tracking `TotalRequests`, `ActiveConcurrency`, `QueuedRequests`, and `DiscoveredServices`.
- **UI & Telemetry Delivery**: Server-Sent Events (SSE) streaming DOM updates to an embedded HTMX dashboard and JSON to Chart.js.
- **Testing & CI**: Decoupled unit test suite (`test/unit/`), `.golangci.yml` v2 schema enforcement, and multi-stage scratch container compilation via GitHub Actions (`.github/workflows/ci.yml`).

---

## Milestone 1: Multi-Backend Load Balancing & Advanced Routing

**Objective**: Transition from single-host mapping to a distributed reverse proxy supporting multi-replica services and fine-grained URL routing.

### 1.1 Multi-Replica Load Balancing
- **Feature**: Support multiple container backends sharing the same `traffic-proxy.rule` hostname.
- **Implementation**:
  - Replace `map[string]*httputil.ReverseProxy` in `internal/proxy/proxy.go` with a `BackendPool` structure.
  - Implement load balancing algorithms selectable via label `traffic-proxy.balance`:
    - `round-robin` (default): Atomic cyclic index selection.
    - `least-conn`: Forward to the upstream with the lowest active semaphore counter.
    - `ip-hash`: Consistent hashing on `req.RemoteAddr` for session stickiness.
    - `random`: Uniform pseudo-random distribution.
- **Target Files**: `internal/proxy/balancer.go`, `internal/proxy/proxy.go`.

### 1.2 Path-Based Routing & Path Rewriting
- **Feature**: Route requests based on URL path prefixes and rewrite paths before forwarding.
- **Implementation**:
  - Add container labels:
    - `traffic-proxy.path=/api/v1/*`
    - `traffic-proxy.strip-prefix=/api/v1`
    - `traffic-proxy.priority=100` (for overlapping route resolution)
  - Implement a Trie-based or Radix-tree path matcher in `internal/proxy`.
  - Rewrite `req.URL.Path` and preserve original path in `X-Forwarded-Prefix`.
- **Target Files**: `internal/proxy/route_tree.go`, `internal/proxy/proxy.go`.

### 1.3 WebSocket & HTTP Upgrade Support
- **Feature**: Transparent proxying of WebSocket (`ws://` and `wss://`) connections and HTTP/2 cleartext (`h2c`).
- **Implementation**:
  - Verify `httputil.ReverseProxy` properly negotiates `Connection: Upgrade` headers.
  - Bypass standard short-lived request timeouts for active persistent WebSocket tunnels.
- **Target Files**: `internal/proxy/proxy.go`.

---

## Milestone 2: TLS Termination, ACME & Security Engine

**Objective**: Implement production HTTPS termination on port 443 with automatic certificate issuance and client security controls.

### 2.1 Automated TLS & ACME (Let's Encrypt)
- **Feature**: Automatic zero-configuration SSL/TLS certificate generation and renewal.
- **Implementation**:
  - Integrate `golang.org/x/crypto/acme/autocert` in `internal/server/server.go`.
  - Add support for HTTP-01 ACME challenge verification.
  - Cache certificates in a persistent volume directory (e.g., `/certs`).
- **Target Files**: `internal/server/tls.go`, `internal/server/server.go`, `docker-compose.yml`.

### 2.2 Custom Certificate & Wildcard TLS Support
- **Feature**: Load user-supplied certificates for offline, private homelab, or custom domain setups.
- **Implementation**:
  - Support mounting certificate keypairs via flags/environment variables (`TLS_CERT_FILE`, `TLS_KEY_FILE`).
  - Dynamic SNI certificate resolution matching inbound TLS `ClientHello` against configured domains.
- **Target Files**: `internal/server/tls.go`.

### 2.3 HTTP-to-HTTPS Redirection
- **Feature**: Automated redirect of all plain HTTP traffic on port 80 to secure port 443.
- **Implementation**:
  - Add auxiliary listener on `:80` that issues `301 Moved Permanently` redirects to `https://<host><request_uri>`, while allowing ACME challenge traffic on `/.well-known/acme-challenge/*`.
- **Target Files**: `internal/server/server.go`.

### 2.4 Client Rate Limiting & Access Control
- **Feature**: Guard backends against denial-of-service and brute force.
- **Implementation**:
  - Token-bucket rate limiter (`golang.org/x/time/rate`) keyed by client IP.
  - Container label overrides:
    - `traffic-proxy.ratelimit.requests=100` (requests per second)
    - `traffic-proxy.ratelimit.burst=20`
    - `traffic-proxy.allowlist=192.168.1.0/24,10.0.0.0/8` (CIDR network restriction)
- **Target Files**: `internal/proxy/ratelimit.go`, `internal/proxy/proxy.go`.

### 2.5 Security Headers Middleware
- **Feature**: Enforce modern browser security standards.
- **Implementation**:
  - Chi middleware injecting `Strict-Transport-Security` (HSTS), `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, and `Referrer-Policy: strict-origin-when-cross-origin`.
- **Target Files**: `internal/server/middleware.go`.

---

## Milestone 3: Active Health Checking, Circuit Breaking & Resiliency

**Objective**: Ensure zero downtime by proactively evicting degraded backends and halting cascading upstream failures.

### 3.1 Active Out-of-Band Health Checking
- **Feature**: Continuously probe backends to detect hangs, socket errors, and 5xx failures before user requests hit them.
- **Implementation**:
  - Support container labels:
    - `traffic-proxy.healthcheck.path=/healthz`
    - `traffic-proxy.healthcheck.interval=10s`
    - `traffic-proxy.healthcheck.timeout=2s`
    - `traffic-proxy.healthcheck.unhealthy-threshold=3`
    - `traffic-proxy.healthcheck.healthy-threshold=2`
  - Periodic background goroutine executing HTTP GET probes against container endpoints.
  - Mark `ServiceTarget.Healthy = false` and atomically exclude failed endpoints from the load balancer pool.
- **Target Files**: `internal/discovery/health.go`, `internal/proxy/balancer.go`.

### 3.2 Circuit Breaker Pattern
- **Feature**: Fast-fail requests when an upstream service demonstrates an error rate exceeding safe operating thresholds.
- **Implementation**:
  - State machine: `Closed` (normal traffic) -> `Open` (fast-fail with `503` without contacting backend) -> `Half-Open` (test probe requests).
  - Trigger transition when error rate exceeds 50% over a 10-second sliding window.
- **Target Files**: `internal/proxy/circuit_breaker.go`.

### 3.3 Safe Request Retries
- **Feature**: Automatically retry failed idempotent HTTP requests on alternative healthy replicas.
- **Implementation**:
  - If upstream returns `502 Bad Gateway`, connection refused, or socket timeout on `GET`/`HEAD` requests, retry once on an alternative backend in the pool.
- **Target Files**: `internal/proxy/proxy.go`.

---

## Milestone 4: Event-Driven Discovery & Pluggable Providers

**Objective**: Replace 5-second polling with real-time daemon events and expand discovery beyond single-daemon Docker.

### 4.1 Real-Time Docker Events Stream
- **Feature**: Immediate route synchronization (< 50ms latency) upon container startup, shutdown, or crash.
- **Implementation**:
  - Replace `time.NewTicker(5 * time.Second)` in `DockerProvider` with a persistent event stream listener using `cli.Events(ctx, types.EventsOptions{})`.
  - Filter events on `type=container` and actions `start`, `die`, `destroy`, `update`.
  - Trigger targeted service table reloads on receipt of lifecycle events.
- **Target Files**: `internal/discovery/docker.go`.

### 4.2 File-Based / Static Configuration Provider
- **Feature**: Route traffic to external bare-metal servers, virtual machines, or non-containerized services.
- **Implementation**:
  - Implement `FileProvider` adhering to the `discovery.Provider` interface.
  - Parse a YAML/JSON configuration file (e.g. `/etc/traffic-proxy/routes.yaml`).
  - Watch for file modifications using `fsnotify/fsnotify` for live reloading without gateway restarts.
- **Target Files**: `internal/discovery/file.go`.

### 4.3 Cluster Discovery (Docker Swarm / Kubernetes)
- **Feature**: Auto-discover services in multi-node clusters.
- **Implementation**:
  - `SwarmProvider`: Inspect Docker Swarm Services (`cli.ServiceList`) targeting overlay network VIPs.
  - `KubernetesProvider`: Track Kubernetes `Endpoints` or `EndpointSlices` via `client-go`.
- **Target Files**: `internal/discovery/swarm.go`, `internal/discovery/kubernetes.go`.

---

## Milestone 5: Granular Observability & Enhanced HTMX Dashboard

**Objective**: Expand telemetry from high-level global counters to per-route performance diagnostics and an interactive control plane.

### 5.1 Per-Route Metrics & Latency Histograms
- **Feature**: Real-time observability into which specific backends are consuming resources or experiencing errors.
- **Implementation**:
  - Track metrics partitioned by `HostRule`:
    - Total requests & request rate (RPS).
    - Status code counters: `2xx`, `3xx`, `4xx`, `5xx`.
    - Latency distribution: min, max, p50, p95, p99.
    - Bandwidth: Bytes sent and received.
- **Target Files**: `internal/metrics/route_metrics.go`, `internal/metrics/metrics.go`.

### 5.2 Prometheus Metrics Exposition (`/metrics`)
- **Feature**: Standard OpenMetrics / Prometheus scrape endpoint for Grafana integration.
- **Implementation**:
  - Mount `/metrics` endpoint in `internal/server/server.go`.
  - Export standard Prometheus counters, gauges, and histograms without external heavy agent dependencies.
- **Target Files**: `internal/metrics/prometheus.go`, `internal/server/server.go`.

### 5.3 Interactive HTMX Dashboard Enhancements
- **Feature**: Comprehensive real-time management console.
- **Implementation**:
  - **Service Inventory Table**:
    - Live list of all discovered backends, internal IP addresses, target ports, health status, and active connections.
    - Status pills: Green (healthy), Red (unhealthy/failing), Yellow (draining).
  - **Interactive Controls**:
    - Maintenance Mode / Drain toggle: Stop routing new traffic to a specific backend without killing the container.
    - Live slider to adjust global concurrency limit and queue timeouts on the fly.
  - **Live Log Stream**:
    - SSE-streamed structured JSON log feed with client-side level filtering (`INFO`, `WARN`, `ERROR`).
- **Target Files**: `ui/static/index.html`, `internal/server/server.go`.

---

## Milestone 6: Configuration Management, E2E Testing & Hardening

**Objective**: Provide dynamic production configuration and ensure strict reliability under benchmark load.

### 6.1 Centralized Dynamic Configuration
- **Feature**: Unified configuration via environment variables and configuration files.
- **Supported Variables**:
  - `TRAFFIC_PROXY_HTTP_PORT` (default: `80`)
  - `TRAFFIC_PROXY_HTTPS_PORT` (default: `443`)
  - `TRAFFIC_PROXY_MAX_CONCURRENCY` (default: `5000`)
  - `TRAFFIC_PROXY_QUEUE_TIMEOUT` (default: `3s`)
  - `TRAFFIC_PROXY_LOG_LEVEL` (`DEBUG`, `INFO`, `WARN`, `ERROR`)
  - `TRAFFIC_PROXY_CERT_DIR` (default: `/certs`)
- **Target Files**: `internal/config/config.go`, `cmd/proxy/main.go`.

### 6.2 End-to-End (E2E) Integration Test Suite
- **Feature**: Automated integration verification running real Docker daemon containers.
- **Implementation**:
  - Test suites using `testcontainers-go` to spin up mock backend containers, apply labels, send traffic through TrafficProxy, and assert load balancing, failover, and queue rejections.
- **Target Files**: `test/integration/e2e_test.go`.

### 6.3 Performance Benchmarking & Load Testing
- **Feature**: Validate throughput (> 20,000 req/sec) and low latency under 5,000+ concurrent connections.
- **Implementation**:
  - Automated benchmark scripts using `vegeta` or `k6` committed in `test/benchmark/`.
  - Profiling hooks (`net/http/pprof`) enabled via debug flag for CPU and heap allocation analysis.
- **Target Files**: `test/benchmark/load_test.sh`, `internal/server/pprof.go`.

---

## Milestone Execution Matrix

| Milestone | Key Features | Priority | Estimated Complexity | Core Packages |
| :--- | :--- | :--- | :--- | :--- |
| **M1: Load Balancing & Routing** | Multi-replica pools, Round-Robin, Path matching, WebSocket | **P0 (Immediate)** | Medium | `internal/proxy` |
| **M2: TLS & Security** | Let's Encrypt ACME, Custom certs, HTTPS redirect, Rate limiting | **P0 (Immediate)** | High | `internal/server`, `internal/proxy` |
| **M3: Health Checks & Resiliency**| Active HTTP probes, Circuit Breakers, Safe retries | **P1 (High)** | Medium | `internal/discovery`, `internal/proxy` |
| **M4: Event-Driven Discovery** | Real-time Docker events, File/Static provider, Swarm/K8s | **P1 (High)** | Medium | `internal/discovery` |
| **M5: Observability & Dashboard** | Per-route metrics, Prometheus `/metrics`, Interactive UI | **P2 (Medium)** | Medium | `internal/metrics`, `ui/` |
| **M6: Config, E2E & Hardening** | Environment config, `testcontainers-go` E2E, Load benchmarks | **P2 (Medium)** | Low | `internal/config`, `test/` |
