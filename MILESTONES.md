# TrafficProxy Edge Gateway — Milestone Overview & Feature Roadmap

A comprehensive, technical implementation roadmap detailing current system status, gap analysis, and phased development milestones for TrafficProxy Edge Gateway.

---

## Current Architecture Baseline

All baseline architecture components have been systematically audited against the codebase and unit test suite:

- [x] **Core Proxy Engine**: Single-host reverse proxy (`net/http/httputil.ReverseProxy`) with persistent connection pooling.
  - *Evidence*: [`internal/proxy/proxy.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/internal/proxy/proxy.go#L57-L65); tested in [`test/unit/proxy_test.go:TestRouter_ServeHTTP_Routing`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/test/unit/proxy_test.go).
- [x] **Concurrency Throttling**: Global concurrency gating via `golang.org/x/sync/semaphore` with a queue acquisition timeout returning 503 on saturation.
  - *Evidence*: [`internal/proxy/proxy.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/internal/proxy/proxy.go#L91-L106); tested in [`test/unit/proxy_test.go:TestRouter_QueueTimeout`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/test/unit/proxy_test.go).
- [x] **Service Discovery**: Docker Engine API polling over `/var/run/docker.sock` targeting `traffic-proxy.*` container labels.
  - *Evidence*: [`internal/discovery/discovery.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/internal/discovery/discovery.go#L36-L175); tested in [`test/unit/discovery_test.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/test/unit/discovery_test.go).
- [x] **Telemetry State Engine**: Global lock-free power-of-two ring buffer (1024 slots) and atomic counters tracking `TotalRequests`, `ActiveConcurrency`, `QueuedRequests`, and `DiscoveredServices`.
  - *Evidence*: [`internal/metrics/metrics.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/internal/metrics/metrics.go#L12-L108); tested in [`test/unit/metrics_test.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/test/unit/metrics_test.go).
- [x] **Embedded UI Dashboard**: Single static executable binary serving static assets from embedded filesystem (`//go:embed`).
  - *Evidence*: [`ui/embed.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/ui/embed.go), [`ui/static/index.html`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/ui/static/index.html); tested in [`test/unit/server_test.go:TestSetupRouter_Endpoints`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/test/unit/server_test.go).
- [x] **Live SSE Telemetry Streaming (`/api/events`)**: Server-Sent Events (SSE) streaming live DOM updates to HTMX and JSON to Chart.js.
  - *Evidence*: [`internal/server/server.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/internal/server/server.go), [`ui/static/index.html`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/ui/static/index.html); tested in [`test/unit/server_test.go:TestSetupRouter_SSEEvents`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/test/unit/server_test.go).
- [x] **Developer Sandbox & Interactive Controls**: Environment profiling (`DEVELOPMENT` vs `PRODUCTION`), dynamic Docker service target sampling in synthetic seeder (`POST /api/dev/seed`), queue saturation stress testing (`POST /api/dev/stress`), atomic metrics reset (`POST /api/dev/reset-metrics`), and live runtime state inspection (`GET /api/dev/debug-state`) protected by production access guards (403 Forbidden).
  - *Evidence*: [`internal/server/dev.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/internal/server/dev.go), [`internal/server/server.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/internal/server/server.go), [`ui/static/index.html`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/ui/static/index.html); tested in [`test/unit/server_test.go:TestSetupRouter_DevEndpoints_DevelopmentMode`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/test/unit/server_test.go), [`TestSetupRouter_DevEndpoints_ProductionForbidden`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/test/unit/server_test.go), [`TestSetupRouter_DevSeed_DynamicDockerSampling`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/test/unit/server_test.go).
- [x] **Testing & CI**: Decoupled unit test suite (`test/unit/`), `.golangci.yml` linting rules, and multi-stage scratch container compilation via GitHub Actions (`.github/workflows/ci.yml`).
  - *Evidence*: [`test/unit/`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/test/unit/), [`.golangci.yml`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/.golangci.yml), [`.github/workflows/ci.yml`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/.github/workflows/ci.yml), [`Dockerfile`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/Dockerfile).

---

## Milestone 1: Multi-Backend Load Balancing & Advanced Routing

**Objective**: Transition from single-host mapping to a distributed reverse proxy supporting multi-replica services and fine-grained URL routing.

### 1.1 Multi-Replica Load Balancing
- [x] **Feature**: Support multiple container backends sharing the same `traffic-proxy.rule` hostname.
- **Implementation Evidence**:
  - Replaced single-host `map[string]*httputil.ReverseProxy` with `BackendPool` in [`internal/proxy/proxy.go`](file:///home/ninonakano/Desktop/Traffic-Proxy-Dashboard/internal/proxy/proxy.go).
  - Implemented 4 load balancing algorithms in [`internal/proxy/balancer.go`](file:///home/ninonakano/Desktop/Traffic-Proxy-Dashboard/internal/proxy/balancer.go):
    - `round-robin` (default): Atomic cyclic index selection across healthy replicas.
    - `least-conn`: Selects backend with lowest atomic in-flight connection counter.
    - `ip-hash`: FNV-1a hash of client IP for sticky session routing.
    - `random`: Uniform pseudo-random distribution.
  - Added label `traffic-proxy.balance` parsing in `NewBalancer`.
  - Upgraded [`internal/discovery/discovery.go`](file:///home/ninonakano/Desktop/Traffic-Proxy-Dashboard/internal/discovery/discovery.go) to catalogue all containers, probe reachability via TCP, and flag `Reachable`/`Enabled`/`DiscoveryError`.
  - Updated [`cmd/proxy/main.go`](file:///home/ninonakano/Desktop/Traffic-Proxy-Dashboard/cmd/proxy/main.go) to pass raw target slices to `router.UpdateBackends(targets)`.
  - Comprehensive unit testing: [`test/unit/balancer_test.go`](file:///home/ninonakano/Desktop/Traffic-Proxy-Dashboard/test/unit/balancer_test.go) and [`test/unit/proxy_test.go`](file:///home/ninonakano/Desktop/Traffic-Proxy-Dashboard/test/unit/proxy_test.go).
- **Target Files**: `internal/proxy/balancer.go`, `internal/proxy/proxy.go`, `internal/discovery/discovery.go`, `cmd/proxy/main.go`.
- **Status**: **Completed**.

### 1.2 Path-Based Routing & Path Rewriting
- [ ] **Feature**: Route requests based on URL path prefixes and rewrite paths before forwarding.
- **Implementation Plan**:
  - Add container labels:
    - `traffic-proxy.path=/api/v1/*`
    - `traffic-proxy.strip-prefix=/api/v1`
    - `traffic-proxy.priority=100` (for overlapping route resolution)
  - Implement a Trie-based or Radix-tree path matcher in `internal/proxy`.
  - Rewrite `req.URL.Path` and preserve original path in `X-Forwarded-Prefix`.
- **Target Files**: `internal/proxy/route_tree.go`, `internal/proxy/proxy.go`.
- **Status**: **Pending** (Currently only hostname-based routing is evaluated).

### 1.3 WebSocket & HTTP Upgrade Support
- [ ] **Feature**: Transparent proxying of WebSocket (`ws://` and `wss://`) connections and HTTP/2 cleartext (`h2c`).
- **Implementation Plan**:
  - Verify `httputil.ReverseProxy` properly negotiates `Connection: Upgrade` headers.
  - Bypass standard short-lived request timeouts for active persistent WebSocket tunnels.
- **Target Files**: `internal/proxy/proxy.go`.
- **Status**: **Pending** (No dedicated WebSocket connection tracking or timeout bypass logic).

---

## Milestone 2: TLS Termination, ACME & Security Engine

**Objective**: Implement production HTTPS termination on port 443 with automatic certificate issuance and client security controls.

### 2.1 Automated TLS & ACME (Let's Encrypt)
- [ ] **Feature**: Automatic zero-configuration SSL/TLS certificate generation and renewal.
- **Implementation Plan**:
  - Integrate `golang.org/x/crypto/acme/autocert` in `internal/server/server.go`.
  - Add support for HTTP-01 ACME challenge verification.
  - Cache certificates in a persistent volume directory (e.g., `/certs`).
- **Target Files**: `internal/server/tls.go`, `internal/server/server.go`, `docker-compose.yml`.
- **Status**: **Pending** (No ACME client or autocert package integrated).

### 2.2 Custom Certificate & Wildcard TLS Support
- [ ] **Feature**: Load user-supplied certificates for offline, private homelab, or custom domain setups.
- **Implementation Plan**:
  - Support mounting certificate keypairs via flags/environment variables (`TLS_CERT_FILE`, `TLS_KEY_FILE`).
  - Dynamic SNI certificate resolution matching inbound TLS `ClientHello` against configured domains.
- **Target Files**: `internal/server/tls.go`.
- **Status**: **Pending** (HTTPS listener and certificate loading not yet present).

### 2.3 HTTP-to-HTTPS Redirection
- [ ] **Feature**: Automated redirect of all plain HTTP traffic on port 80 to secure port 443.
- **Implementation Plan**:
  - Add auxiliary listener on `:80` that issues `301 Moved Permanently` redirects to `https://<host><request_uri>`, while allowing ACME challenge traffic on `/.well-known/acme-challenge/*`.
- **Target Files**: `internal/server/server.go`.
- **Status**: **Pending** (Single HTTP server listener currently configured).

### 2.4 Client Rate Limiting & Access Control
- [ ] **Feature**: Guard backends against denial-of-service and brute force.
- **Implementation Plan**:
  - Token-bucket rate limiter (`golang.org/x/time/rate`) keyed by client IP.
  - Container label overrides:
    - `traffic-proxy.ratelimit.requests=100` (requests per second)
    - `traffic-proxy.ratelimit.burst=20`
    - `traffic-proxy.allowlist=192.168.1.0/24,10.0.0.0/8` (CIDR network restriction)
- **Target Files**: `internal/proxy/ratelimit.go`, `internal/proxy/proxy.go`.
- **Status**: **Pending** (Rate limiting package not yet integrated).

### 2.5 Security Headers Middleware
- [ ] **Feature**: Enforce modern browser security standards.
- **Implementation Plan**:
  - Chi middleware injecting `Strict-Transport-Security` (HSTS), `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, and `Referrer-Policy: strict-origin-when-cross-origin`.
- **Target Files**: `internal/server/middleware.go`.
- **Status**: **Pending** (Only `middleware.Logger` and `middleware.Recoverer` currently active).

---

## Milestone 3: Active Health Checking, Circuit Breaking & Resiliency

**Objective**: Ensure zero downtime by proactively evicting degraded backends and halting cascading upstream failures.

### 3.1 Active Out-of-Band Health Checking
- [ ] **Feature**: Continuously probe backends to detect hangs, socket errors, and 5xx failures before user requests hit them.
- **Implementation Plan**:
  - Support container labels:
    - `traffic-proxy.healthcheck.path=/healthz`
    - `traffic-proxy.healthcheck.interval=10s`
    - `traffic-proxy.healthcheck.timeout=2s`
    - `traffic-proxy.healthcheck.unhealthy-threshold=3`
    - `traffic-proxy.healthcheck.healthy-threshold=2`
  - Periodic background goroutine executing HTTP GET probes against container endpoints.
  - Mark `ServiceTarget.Healthy = false` and atomically exclude failed endpoints from the load balancer pool.
- **Target Files**: `internal/discovery/health.go`, `internal/proxy/balancer.go`.
- **Status**: **Pending** (Discovery currently verifies Docker container state `c.State == "running"`, but lacks out-of-band HTTP probes).

### 3.2 Circuit Breaker Pattern
- [ ] **Feature**: Fast-fail requests when an upstream service demonstrates an error rate exceeding safe operating thresholds.
- **Implementation Plan**:
  - State machine: `Closed` (normal traffic) -> `Open` (fast-fail with `503` without contacting backend) -> `Half-Open` (test probe requests).
  - Trigger transition when error rate exceeds 50% over a 10-second sliding window.
- **Target Files**: `internal/proxy/circuit_breaker.go`.
- **Status**: **Pending** (Circuit breaker state machine not yet implemented).

### 3.3 Safe Request Retries
- [ ] **Feature**: Automatically retry failed idempotent HTTP requests on alternative healthy replicas.
- **Implementation Plan**:
  - If upstream returns `502 Bad Gateway`, connection refused, or socket timeout on `GET`/`HEAD` requests, retry once on an alternative backend in the pool.
- **Target Files**: `internal/proxy/proxy.go`.
- **Status**: **Pending** (Reverse proxy forwards requests without automatic retry fallback).

---

## Milestone 4: Event-Driven Discovery & Pluggable Providers

**Objective**: Replace polling with real-time daemon events and expand discovery beyond single-daemon Docker.

### 4.1 Real-Time Docker Events Stream
- [ ] **Feature**: Immediate route synchronization (< 50ms latency) upon container startup, shutdown, or crash.
- **Implementation Plan**:
  - Replace `time.NewTicker(p.pollInterval)` in `DockerProvider` with a persistent event stream listener using `cli.Events(ctx, types.EventsOptions{})`.
  - Filter events on `type=container` and actions `start`, `die`, `destroy`, `update`.
  - Trigger targeted service table reloads on receipt of lifecycle events.
- **Target Files**: `internal/discovery/docker.go`.
- **Status**: **Pending** (Currently using ticker-based polling via `p.pollInterval` in `internal/discovery/discovery.go`).

### 4.2 File-Based / Static Configuration Provider
- [ ] **Feature**: Route traffic to external bare-metal servers, virtual machines, or non-containerized services.
- **Implementation Plan**:
  - Implement `FileProvider` adhering to the `discovery.Provider` interface.
  - Parse a YAML/JSON configuration file (e.g. `/etc/traffic-proxy/routes.yaml`).
  - Watch for file modifications using `fsnotify/fsnotify` for live reloading without gateway restarts.
- **Target Files**: `internal/discovery/file.go`.
- **Status**: **Pending** (`Provider` interface is defined in `internal/discovery/discovery.go`, but only `DockerProvider` is implemented).

### 4.3 Cluster Discovery (Docker Swarm / Kubernetes)
- [ ] **Feature**: Auto-discover services in multi-node clusters.
- **Implementation Plan**:
  - `SwarmProvider`: Inspect Docker Swarm Services (`cli.ServiceList`) targeting overlay network VIPs.
  - `KubernetesProvider`: Track Kubernetes `Endpoints` or `EndpointSlices` via `client-go`.
- **Target Files**: `internal/discovery/swarm.go`, `internal/discovery/kubernetes.go`.
- **Status**: **Pending** (Cluster provider implementations not yet created).

---

## Milestone 5: Granular Observability & Enhanced HTMX Dashboard

**Objective**: Expand telemetry from high-level global counters to per-route performance diagnostics and an interactive control plane.

### 5.1 Per-Route Metrics & Latency Histograms
- [ ] **Feature**: Real-time observability into which specific backends are consuming resources or experiencing errors.
- **Implementation Plan**:
  - Track metrics partitioned by `HostRule`:
    - Total requests & request rate (RPS).
    - Status code counters: `2xx`, `3xx`, `4xx`, `5xx`.
    - Latency distribution: min, max, p50, p95, p99.
    - Bandwidth: Bytes sent and received.
- **Target Files**: `internal/metrics/route_metrics.go`, `internal/metrics/metrics.go`.
- **Status**: **Pending** (Currently tracking global aggregate counters in `internal/metrics/metrics.go`).

### 5.2 Prometheus Metrics Exposition (`/metrics`)
- [ ] **Feature**: Standard OpenMetrics / Prometheus scrape endpoint for Grafana integration.
- **Implementation Plan**:
  - Mount `/metrics` endpoint in `internal/server/server.go`.
  - Export standard Prometheus counters, gauges, and histograms without external heavy agent dependencies.
- **Target Files**: `internal/metrics/prometheus.go`, `internal/server/server.go`.
- **Status**: **Pending** (`/metrics` route not yet mounted).

### 5.3 Interactive HTMX Dashboard & Live Real-Time Telemetry
- [ ] **Feature**: Real-time Server-Sent Events (SSE) telemetry stream (`/api/events`) and interactive management console.
- **Sub-Features & Implementation Progress**:
  - [x] **Live Telemetry Stream (`/api/events`)**: Chi router SSE endpoint emitting dual streams (`event: metrics` for HTMX OOB DOM swaps and `event: telemetry` for dynamic Chart.js updates with 25-point sliding window).
    - *Evidence*: [`internal/server/server.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/internal/server/server.go#L67-L147), [`ui/static/index.html`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/ui/static/index.html#L133-L176), [`test/unit/server_test.go:TestSetupRouter_SSEEvents`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/test/unit/server_test.go#L56-L98).
  - [x] **Dynamic Environment Profile & Status Dot**: Runtime environment injection (`__SANPROX_ENVIRONMENT__`), status badge pill, and live gateway connection indicator (`#gateway-status-dot`) with automatic offline recovery handling on `htmx:sseError`.
    - *Evidence*: [`ui/static/index.html`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/ui/static/index.html#L48-L52), [`internal/server/server.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/internal/server/server.go#L44-L55), [`test/unit/server_test.go:TestSetupRouter_BrandingAndConfig`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/test/unit/server_test.go#L100-L148).
  - [x] **Interactive Dev Console & Synthetic Seeder**: In-dashboard quick actions and dedicated Dev Tools tab supporting synthetic traffic seeding with dynamic Docker service sampling (`POST /api/dev/seed`), queue saturation testing (`POST /api/dev/stress`), atomic counter reset (`POST /api/dev/reset-metrics`), and live runtime state inspection (`GET /api/dev/debug-state`).
    - *Evidence*: [`internal/server/dev.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/internal/server/dev.go), [`ui/static/index.html`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/ui/static/index.html#L97-L131), [`test/unit/server_test.go:TestSetupRouter_DevSeed_DynamicDockerSampling`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/test/unit/server_test.go#L247-L290).
  - [x] **Routing Discovery Table & Compose Generator**: Active virtual host rule inspection table with filter search and one-click Docker Compose YAML generator snippet.
    - *Evidence*: [`ui/static/index.html`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/ui/static/index.html#L227-L338).
  - [ ] **Maintenance Mode / Drain toggle**: Stop routing new traffic to a specific backend without killing the container.
  - [ ] **Live Slider for Runtime Limits**: Dynamic concurrency limit and queue timeout adjustments on the fly.
  - [ ] **Live Application Log Stream**: Real-time daemon log stream over SSE with client-side log level filtering.
- **Target Files**: `ui/static/index.html`, `internal/server/server.go`, `internal/server/dev.go`, `cmd/trafficgen/main.go`.
- **Status**: **In Progress (Live SSE Streaming, Dev Tools Console, and Dynamic Docker Seeder Complete; Drain & Live Logs Pending)**.
  - *Current State*: The real-time `/api/events` Server-Sent Events stream is implemented in [`internal/server/server.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/internal/server/server.go) and verified by [`test/unit/server_test.go:TestSetupRouter_SSEEvents`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/test/unit/server_test.go). [`ui/static/index.html`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/ui/static/index.html) receives live dual streams via HTMX SSE extension and dynamic Chart.js rolling updates. [`internal/server/dev.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/internal/server/dev.go) provides full developer and mock control endpoints with dynamic Docker service sampling. [`cmd/trafficgen/main.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/cmd/trafficgen/main.go) and [`scripts/seed-traffic.sh`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/scripts/seed-traffic.sh) support live streaming verification. Interactive backend drain controls and log filtering remain pending.

---

## Milestone 6: Configuration Management, E2E Testing & Hardening

**Objective**: Provide dynamic production configuration and ensure strict reliability under benchmark load.

### 6.1 Centralized Dynamic Configuration
- [x] **Feature**: Unified configuration via environment variables, profile detection, and local start scripts.
  - *Evidence*: [`internal/config/config.go`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/internal/config/config.go#L12-L92), [`.env.example`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/.env.example), [`.env`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/.env), [`start.ps1`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/start.ps1), [`start.sh`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/start.sh); tested in [`test/unit/config_test.go:TestConfig_EnvironmentVariations`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/test/unit/config_test.go).
  - Supported: `ENVIRONMENT` (`DEVELOPMENT`/`PRODUCTION`), `PROXY_PORT`, `PROXY_MAX_CONCURRENT`, `PROXY_QUEUE_TIMEOUT`, `DOCKER_POLL_INTERVAL`, `LOG_LEVEL`.
  - *Status*: **Implemented (Core Environment Configuration & Profile Guards)**.

### 6.2 End-to-End (E2E) Integration Test Suite
- [ ] **Feature**: Automated integration verification running real Docker daemon containers.
- **Implementation Plan**:
  - Test suites using `testcontainers-go` to spin up mock backend containers, apply labels, send traffic through TrafficProxy, and assert load balancing, failover, and queue rejections.
- **Target Files**: `test/integration/e2e_test.go`.
- **Status**: **Pending** (Current test suite is unit-level in `test/unit/`).

### 6.3 Performance Benchmarking & Load Testing
- [ ] **Feature**: Validate throughput (> 20,000 req/sec) and low latency under 5,000+ concurrent connections.
- **Implementation Plan**:
  - Automated benchmark scripts using `vegeta` or `k6` committed in `test/benchmark/`.
  - Profiling hooks (`net/http/pprof`) enabled via debug flag for CPU and heap allocation analysis.
  - Note: Synthetic traffic generator tool [`cmd/trafficgen`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/cmd/trafficgen) and [`scripts/seed-traffic.sh`](file:///C:/Users/johan/Desktop/Traffic-Proxy-Dashboard/scripts/seed-traffic.sh) are implemented and functional, but formal benchmarking harness and pprof profiling remain pending.
- **Target Files**: `test/benchmark/load_test.sh`, `internal/server/pprof.go`.
- **Status**: **Pending**.

---

## Milestone Execution Matrix

| Milestone | Key Features | Status | Priority | Estimated Complexity | Core Packages |
| :--- | :--- | :---: | :--- | :--- | :--- |
| **Baseline Architecture** | Single-host proxy, Semaphore queue, Docker discovery, Embedded UI, Dev Controls | **Completed** | Foundation | - | `internal/*`, `ui/` |
| **M1: Load Balancing & Routing** | Multi-replica pools, Round-Robin, Path matching, WebSocket | **In Progress (1.1 Done)** | **P0 (Immediate)** | Medium | `internal/proxy` |
| **M2: TLS & Security** | Let's Encrypt ACME, Custom certs, HTTPS redirect, Rate limiting | **Pending** | **P0 (Immediate)** | High | `internal/server`, `internal/proxy` |
| **M3: Health Checks & Resiliency**| Active HTTP probes, Circuit Breakers, Safe retries | **Pending** | **P1 (High)** | Medium | `internal/discovery`, `internal/proxy` |
| **M4: Event-Driven Discovery** | Real-time Docker events, File/Static provider, Swarm/K8s | **Pending** | **P1 (High)** | Medium | `internal/discovery` |
| **M5: Observability & Dashboard** | Live SSE stream (Done), Dev Controls & Seeder (Done), Per-route metrics, Prometheus `/metrics`, Drain controls | **In Progress** | **P2 (Medium)** | Medium | `internal/metrics`, `ui/`, `internal/server` |
| **M6: Config, E2E & Hardening** | Environment config & profiles (Done), `testcontainers-go` E2E, Load benchmarks | **In Progress** | **P2 (Medium)** | Low | `internal/config`, `test/` |
