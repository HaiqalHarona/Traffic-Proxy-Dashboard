# Developer Guide

Comprehensive architectural, structural, and technical reference for all directories and files in TrafficProxy Edge Gateway.

---

## Table of Contents

- [Project Overview](#project-overview)
- [Directory & File Reference](#directory--file-reference)
  - [Root Files](#root-files)
  - [cmd/](#cmd)
  - [internal/](#internal)
    - [internal/config/](#internalconfig)
    - [internal/discovery/](#internaldiscovery)
    - [internal/metrics/](#internalmetrics)
    - [internal/proxy/](#internalproxy)
    - [internal/server/](#internalserver)
  - [ui/](#ui)
  - [test/](#test)
  - [.github/](#github)
  - [doc/](#doc)
  - [.agents/](#agents)
- [End-to-End Operational Workflow](#end-to-end-operational-workflow)
  - [1. Comprehensive System Architecture Diagram](#1-comprehensive-system-architecture-diagram)
  - [2. Go File Correlation by Runtime Phase](#2-go-file-correlation-by-runtime-phase)
  - [3. Go File Correlation Summary Matrix](#3-go-file-correlation-summary-matrix)
- [Traffic Monitoring & Control Flow](#traffic-monitoring--control-flow)
- [Go (Golang) Primer for Beginners](#go-golang-primer-for-beginners)
  - [1. Go Syntax Demystified in Plain English](#1-go-syntax-demystified-in-plain-english)
  - [2. What Each File's Core Function Does](#2-what-each-files-core-function-does)
  - [3. Why We Need Semaphores, Queues, and Concurrency Limits](#3-why-we-need-semaphores-queues-and-concurrency-limits)
  - [4. Why Discovery Needs Fallbacks](#4-why-discovery-needs-fallbacks)
  - [5. Essential Go CLI Commands](#5-essential-go-cli-commands)
- [Building & Testing Locally](#building--testing-locally)

---

## Project Overview

TrafficProxy is a container-aware HTTP reverse proxy and edge gateway. It automatically discovers downstream services by polling the Docker Engine API, actively controls incoming request concurrency via weighted semaphores, and streams live metrics to an embedded HTMX web dashboard using Server-Sent Events (SSE).

---

## Directory & File Reference

### Root Files

| File / Directory | Purpose |
| :--- | :--- |
| `Dockerfile` | Multi-stage Docker build recipe compiling a statically linked binary and deploying it into a scratch image. |
| `docker-compose.yml` | Container orchestration specification mounting `/var/run/docker.sock` in read-only mode with least-privilege security flags. |
| `go.mod` | Go module declaration (`github.com/HaiqalHarona/Traffic-Proxy-Dashboard`), specifying Go version 1.22+ and third-party dependencies. |
| `go.sum` | Checksums for reproducible Go module dependencies and builds. |
| `.golangci.yml` | Linter configuration for `golangci-lint` detailing active linters, options, and run timeouts. |
| `.dockerignore` | Build context exclude file ensuring minimal layer footprints during image compilation. |
| `.gitignore` | Git version control exclude specifications for binaries, test coverages, and IDE configurations. |
| `skills-lock.json` | Lockfile recording external tool and agent skill configurations. |
| `README.md` | Primary project documentation, providing quickstart, architecture summary, and configuration guidelines. |

#### Detailed Root File Descriptions

- **`Dockerfile`**:
  - **Builder Stage (`golang:1.22-alpine`)**: Installs build prerequisites (`git`, `ca-certificates`, `tzdata`). Copies `go.mod` and downloads dependencies before copying project sources. Compiles a static Linux binary (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64`) with stripped symbols (`-ldflags="-s -w -extldflags '-static'"`).
  - **Runtime Stage (`scratch`)**: Minimal rootless production container (< 20MB). Imports CA certificates and timezone databases from the builder. Copies the compiled `/app/traffic-proxy` binary to `/traffic-proxy`. Configures unprivileged user `65534:65534` (`nobody`), exposes ports `80` and `443`, and specifies `/traffic-proxy` as the entrypoint.

- **`docker-compose.yml`**:
  - Defines the `traffic-proxy` service container.
  - Mounts the host Docker socket at `/var/run/docker.sock:ro`.
  - Sets `read_only: true` and `no-new-privileges:true` for kernel-level security isolation.
  - Configures an automated HTTP healthcheck (`wget -q --spider http://localhost:80/`) running every 15 seconds.

- **`go.mod`**:
  - Declares core dependencies including `github.com/go-chi/chi/v5` (HTTP router and middleware), `github.com/docker/docker` (Docker Engine client API), and `golang.org/x/sync/semaphore` (weighted semaphore implementation).

- **`.golangci.yml`**:
  - Configures `golangci-lint` (v1 schema compatible with v1.64.8). Enables linters (`errcheck`, `gosimple`, `govet` with `enable-all: true`, `ineffassign`, `staticcheck`, `unused`, `gofmt`, `misspell`). Sets execution timeout to 5 minutes.

- **`.dockerignore`**:
  - Excludes `.git`, `.gitignore`, `README.md`, `Dockerfile`, `docker-compose.yml`, `learning_proposal.md`, and `*.log` from the Docker build context.

- **`.gitignore`**:
  - Ignores build artifacts (`*.exe`, `*.so`, `*.test`), test coverage files (`coverage.*`, `*.out`), workspace files (`go.work`), environment files (`.env`), and IDE configurations (`.idea/`, `.vscode/`).

---

### cmd/

Application entrypoints and runnable binary mains.

#### `cmd/proxy/`
- **`cmd/proxy/main.go`**:
  - **Runtime Initialization**: Sets up structured JSON logging (`log/slog`) with configured log level, loads runtime configuration via `config.Load()`, initializes root signal handling for `SIGINT`/`SIGTERM` via `signal.NotifyContext`, and creates core `metrics.Collector` and `proxy.Router` instances.
  - **Discovery Initialization**: Instantiates `discovery.DockerProvider` targeting `/var/run/docker.sock` with polling interval from configuration and kicks off background polling.
  - **Dynamic Route Subscription**: Subscribes to discovery events and synchronizes the proxy routing table dynamically via `router.UpdateBackends(routes)`.
  - **Server Execution & Graceful Teardown**: Delegates router configuration to `server.SetupRouter`, runs HTTP server on configured port (`cfg.Port`), and manages graceful shutdown with a 10-second timeout deadline.

#### `cmd/trafficgen/`
- **`cmd/trafficgen/main.go`**:
  - Synthetic traffic generator simulating diverse routing loads (valid backends, slow delayed backends, unmapped 502 targets, and SSE stream subscribers).
  - Reports request throughput (req/s), HTTP response code distributions, and connection errors.

---

### internal/

Internal libraries and packages private to TrafficProxy.

#### internal/config/
Environment variable configuration loader.

- **`internal/config/config.go`**:
  - **`Config`**: Defines gateway runtime parameters (`Port`, `MaxConcurrentRequests`, `QueueTimeout`, `DockerPollInterval`, `LogLevel`).
  - **`Load()`**: Reads environment overrides (`PROXY_PORT`, `PROXY_MAX_CONCURRENT`, `PROXY_QUEUE_TIMEOUT`, `DOCKER_POLL_INTERVAL`, `LOG_LEVEL`) with fallback defaults.

#### internal/discovery/
Container auto-discovery and backend synchronization.

- **`internal/discovery/discovery.go`**:
  - **`ServiceTarget`**: Struct capturing discovered container metadata (`ID`, `Name`, `Host`, `Port`, `HostRule`, `TargetURL`, `Labels`, `Healthy`, `CreatedAt`).
  - **`Provider` Interface**: Generic abstraction exposing `Name() string`, `Start(ctx context.Context) error`, `Services() ([]ServiceTarget, error)`, and `Subscribe() <-chan []ServiceTarget`. Designed to support Swarm, Nomad, Kubernetes, or gossip backends.
  - **`DockerProvider`**: Implements `Provider` using `github.com/docker/docker/client`.
  - **`Scan(ctx)`**: Queries `/var/run/docker.sock` via `ContainerList`. Filters containers possessing the label `traffic-proxy.enable=true` and an active `traffic-proxy.rule`. Resolves container network IP addresses across attached networks and port definitions (`traffic-proxy.port` -> exposed port -> fallback `80`). Updates internal synchronized slice and publishes targets to the subscriber channel.
  - **`NewDockerProviderWithClient` & `SetServices`**: Test helpers enabling dependency injection of custom HTTP mock Docker clients.

#### internal/metrics/
Thread-safe metrics collection and telemetry storage.

- **`internal/metrics/metrics.go`**:
  - **`MetricSnapshot`**: JSON-serializable telemetry payload holding `Timestamp`, `TotalRequests`, `ActiveConcurrency`, `QueuedRequests`, and `DiscoveredServices`.
  - **`TelemetryRingBuffer`**: Thread-safe ring buffer utilizing bitwise power-of-two capacity masking (`writeIdx & mask`) for high-throughput slot placement without pointer reshuffling.
  - **`Collector`**: Central metrics aggregator utilizing atomic primitives (`sync/atomic.Uint64` for total request counter, `sync/atomic.Int64` for active concurrency and queue gauges).
  - **`Snapshot(discoveredCount int)`**: Takes an atomic reading of all metrics, appends the snapshot into the ring buffer, and returns the snapshot instance.

#### internal/proxy/
Traffic management, concurrency throttling, and reverse proxy routing.

- **`internal/proxy/proxy.go`**:
  - **`Config`**: Defines `MaxConcurrentRequests` (default: 1000) and `QueueTimeout` (default: 5s).
  - **`Router`**: Primary traffic routing engine containing a weighted semaphore (`*semaphore.Weighted`), metrics collector reference, read-write mutex, and a dynamic map of `*httputil.ReverseProxy` keyed by normalized hostname.
  - **`RegisterBackend(hostRule, targetURL)` & `UpdateBackends(routes)`**: Registers and hot-reloads backend proxy instances. Configures an optimized HTTP transport with persistent connection pooling (`MaxIdleConns: 100`, `MaxIdleConnsPerHost: 10`, `IdleConnTimeout: 90s`).
  - **`ServeHTTP(w, req)`**:
    1. Increments `TotalRequests` counter.
    2. Enqueues the request into the semaphore with a timeout context (`QueueTimeout`).
    3. Increments and decrements `QueuedRequests`.
    4. If the queue times out before acquiring a slot, replies with HTTP `503 Service Unavailable`.
    5. Acquires semaphore slot and increments `ActiveConcurrency`. Releases slot upon completion.
    6. Normalizes `req.Host` (stripping port and converting to lowercase).
    7. Looks up reverse proxy backend. If not found, returns HTTP `502 Bad Gateway`.
    8. Forwards request via `httputil.ReverseProxy.ServeHTTP`.
  - **`NormalizeHost(host)`**: Exported helper utility extracting hostnames from host/port pairs and returning lowercase strings.

#### internal/server/
HTTP routing multiplexer and telemetry event streams.

- **`internal/server/server.go`**:
  - Configures Chi router with `middleware.Logger` and `middleware.Recoverer`.
  - Mounts the embedded UI file server (`ui.Assets`) under `/static/*` and serves `/index.html` at root `/`.
  - Implements the Server-Sent Events (SSE) telemetry stream on `/api/events`, emitting dual real-time events (`event: metrics` for HTMX OOB updates and `event: telemetry` for Chart.js).
  - Configures catch-all `r.NotFound` handler routing unmatched traffic to `proxyRouter.ServeHTTP`.

---

### ui/

Static dashboard assets embedded into the Go executable.

- **`ui/embed.go`**:
  - Exposes `Assets embed.FS` using the directive `//go:embed static/*`, compiling frontend templates directly into the binary.
- **`ui/static/index.html`**:
  - Single-page dashboard built with Tailwind CSS, HTMX, and Chart.js.
  - Connects to `/api/events` using the HTMX SSE extension (`hx-ext="sse"`, `sse-connect="/api/events"`).
  - Listens for `metrics` events to update metric counter cards without page reloads.
  - Listens for `telemetry` events in JavaScript to drive live line charts showing active concurrency and request trends over time.

---

### test/

Dedicated automated test suites decoupled from production application packages.

#### test/unit/
- **`test/unit/config_test.go`**:
  - Unit tests verifying environment variable parsing, default fallbacks, and invalid duration handling in `internal/config`.
- **`test/unit/discovery_test.go`**:
  - Unit tests verifying `DockerProvider` initialization, container scanning, label parsing, port resolution fallbacks, service listing, and subscriber channels using mock HTTP test servers.
- **`test/unit/metrics_test.go`**:
  - Unit tests verifying `TelemetryRingBuffer` initialization, push behavior, circular write wrap-around, empty buffer handling, and concurrent access.
  - Unit tests verifying `Collector` atomic counter increments and snapshot generation.
- **`test/unit/proxy_test.go`**:
  - Unit tests verifying reverse proxy routing, backend map updates, semaphore concurrency limits, queue timeout rejections, and host header normalization.
- **`test/unit/server_test.go`**:
  - Unit tests verifying gateway HTTP server routing, Chi middleware, static asset serving, and SSE event streaming endpoints.

---

### .github/

Continuous integration and automated GitHub workflows.

#### `.github/workflows/`
- **`.github/workflows/ci.yml`**:
  - GitHub Actions CI/CD workflow (`Fuckass Pipeline`) containing four jobs:
    - **`lint`**: Executes `go vet` and `golangci-lint-action` using root [`.golangci.yml`](file:///home/ninonakano/Desktop/Traffic-Proxy-Dashboard/.golangci.yml).
    - **`test`**: Executes `go test -race -coverpkg=./internal/...` with atomic coverage profile generation across `test/unit/`, enforcing a strict minimum coverage threshold of 60%.
    - **`build`**: Compiles static binary (`CGO_ENABLED=0`) and verifies Docker Buildx image creation with GitHub Actions cache.
    - **`release`**: Triggered on push to `main` branch, authenticating with GitHub Container Registry (`ghcr.io`), tagging, and pushing the production scratch image.

---

### doc/

Project technical and operational documentation.

- **`doc/DEVELOPER_GUIDE.md`**: In-depth explanation of every file, package, and component in the repository (this document).
- **`doc/USER_GUIDE.md`**: Operational guide for running TrafficProxy, setting container labels, and navigating the dashboard.
- **`doc/PIPELINE.md`**: Specification and local reproduction guide for the CI/CD pipeline.
- **`doc/UPDATE.md`**: Procedures for zero-downtime updates, version upgrades, and maintenance.

---

### .agents/

Agent configuration, workspace behavioral rules, and specialized project skills.

#### `.agents/rules/`
- **`.agents/rules/documentation-standards.md`**:
  - Documentation integrity rule enforcing uppercase file naming conventions (`ALL_CAPS.md`) for all markdown files under `doc/`, synchronizing `doc/DEVELOPER_GUIDE.md`, and maintaining valid links in `README.md`.

#### `.agents/skills/`
- **`.agents/skills/proxy-architecture/SKILL.md`**:
  - Architectural blueprint and development conventions for TrafficProxy (semaphore traffic queues, Docker discovery, atomic ring buffer metrics, and HTMX/SSE frontend).
- **`.agents/skills/docker-best-practices/SKILL.md`**:
  - Best practices for multi-stage Docker builds, rootless containers, and Compose orchestration.
- **`.agents/skills/go-development/SKILL.md`**:
  - Idiomatic Go development guidelines, concurrency patterns, and test suites.
- **`.agents/skills/htmx/SKILL.md`**:
  - HTMX development patterns, DOM manipulation, and SSE integration.
- **`.agents/skills/systematic-debugging/SKILL.md`**:
  - Structured debugging and root-cause tracing methodologies.

---

## End-to-End Operational Workflow

This section outlines how TrafficProxy operates from startup to shutdown, correlating every Go source file in the repository with its explicit responsibility during runtime execution.

### 1. Comprehensive System Architecture Diagram

```
                             [ Operating System Environment / Docker Daemon ]
                                                    │
                      ┌─────────────────────────────┼─────────────────────────────┐
                      │ /var/run/docker.sock        │ Environment Variables       │ OS Signals (SIGINT/SIGTERM)
                      ▼                             ▼                             ▼
         internal/discovery/discovery.go   internal/config/config.go      cmd/proxy/main.go
         (DockerProvider.Start/scan)       (Config.Load)                 (signal.NotifyContext)
                      │                                                           │
                      │ Discovered ServiceTargets                                 │ Coordinates Bootstrap
                      ▼                                                           ▼
         cmd/proxy/main.go (Subscriber)                               internal/metrics/metrics.go
                      │                                               (Collector & Ring Buffer)
                      │ Synchronizes Host Routes                                  │
                      ▼                                                           │
          internal/proxy/proxy.go ◄───────────────────────────────────────────────┤
          (Router.UpdateBackends)                                                 │
                      ▲                                                           │
                      │ Forwards Unmatched HTTP Requests                          │ Real-time Telemetry
                      │ (r.NotFound)                                              ▼
          internal/server/server.go ◄─────────────────────────────────────────────┘
          (SetupRouter / Chi Router)
           │                    │
           ├─► Route: /static/* │ Route: /api/events (SSE)
           │   Route: /         │
           ▼                    ▼
       ui/embed.go        Browser Client (HTMX / Chart.js)
       (Embedded Assets)

  [ Synthetic Load Simulation ] ──► cmd/trafficgen/main.go ──► Tests all HTTP & SSE routes
  [ Automated Verification ]    ──► test/unit/*.go          ──► Validates isolated unit behaviors
```

---

### 2. Go File Correlation by Runtime Phase

#### Phase 1: Bootstrapping & Component Wiring

| Go File | Lifecycle Role & Executed Logic |
| :--- | :--- |
| **`internal/config/config.go`** | **Configuration Parsing**: Reads environment overrides (`PROXY_PORT`, `PROXY_MAX_CONCURRENT`, `PROXY_QUEUE_TIMEOUT`, `DOCKER_POLL_INTERVAL`, `LOG_LEVEL`) using `config.Load()`. Validates strings, parses durations, and sets default fallbacks (e.g. port `:80`, 5000 max concurrency, 3s queue timeout). |
| **`cmd/proxy/main.go`** | **Root Orchestrator**: Entrypoint `func main()`. Configures structured JSON logging (`log/slog`) with the configured log level, creates a root cancellable context listening for `os.Interrupt` and `syscall.SIGTERM`, and wires the dependency graph between config, metrics, proxy router, discovery, and HTTP server. |
| **`internal/metrics/metrics.go`** | **Telemetry Storage Initialization**: `metrics.NewCollector()` instantiates atomic counters (`TotalRequests`, `ActiveConcurrency`, `QueuedRequests`) and allocates an in-memory 1024-slot circular ring buffer (`TelemetryRingBuffer`) using power-of-two bitwise masking. |
| **`internal/proxy/proxy.go`** | **Traffic Engine Initialization**: `proxy.NewRouter()` sets up the proxy engine with a weighted semaphore (`semaphore.Weighted(cfg.MaxConcurrentRequests)`), stores the configured queue timeout, and initializes an empty backend route map. |
| **`ui/embed.go`** | **Static Asset Compilation**: Directs the Go compiler via `//go:embed static/*` to pack `index.html`, Tailwind CSS, HTMX, and Chart.js bundles directly into the binary's read-only data segment. |
| **`internal/server/server.go`** | **Router Construction**: `server.SetupRouter()` builds the Chi router (`chi.NewRouter()`), attaches standard middleware (`Logger`, `Recoverer`), binds the embedded static file server to `/static/*` and root `/`, mounts the `/api/events` SSE stream, and attaches the catch-all proxy handler. |

---

#### Phase 2: Continuous Service Discovery & Dynamic Route Synchronization

| Go File | Lifecycle Role & Executed Logic |
| :--- | :--- |
| **`internal/discovery/discovery.go`** | **Container Polling & Inspection**: `DockerProvider.Start(ctx)` initiates a background loop polling `/var/run/docker.sock` every `cfg.DockerPollInterval` (default 5s). Its internal `scan()` method queries `ContainerList`, selects containers with `traffic-proxy.enable=true` and valid `traffic-proxy.rule`, resolves private IPs from attached bridge networks, parses target ports (`traffic-proxy.port` or first exposed port), constructs `ServiceTarget` records, and pushes updated target slices to `eventsChan`. |
| **`cmd/proxy/main.go`** | **Discovery Event Consumer**: A long-running goroutine reads from `dockerProvider.Subscribe()`. For each target list received, it filters healthy targets, maps hostname rules to target backend `*url.URL` instances, and pushes the map into `router.UpdateBackends(routes)`. |
| **`internal/proxy/proxy.go`** | **Hot Route Swapping**: In `router.UpdateBackends(routes)`, acquiring a write lock (`r.mu.Lock()`) creates fresh `httputil.ReverseProxy` instances configured with persistent HTTP connection pooling (`MaxIdleConns: 100`, `IdleConnTimeout: 90s`) and replaces `r.backends`. Running proxy instances are hot-reloaded without downtime or dropped in-flight requests. |

---

#### Phase 3: Client Request Ingestion, Queue Throttling, & Reverse Proxy Forwarding

| Go File | Lifecycle Role & Executed Logic |
| :--- | :--- |
| **`internal/server/server.go`** | **Request Ingestion & Route Matching**: Chi receives incoming HTTP requests. If the request matches `/static/*` or `/`, it is served immediately from embedded memory. All other traffic falls through to the catch-all `r.NotFound(proxyRouter.ServeHTTP)`. |
| **`internal/metrics/metrics.go`** | **Atomic Gauge Tracking**: At the very start of request handling, `r.metrics.TotalRequests.Add(1)` atomically increments the cumulative request counter with zero lock contention. |
| **`internal/proxy/proxy.go`** | **Concurrency Queue & Timeout Throttling**: <br>1. Increments `QueuedRequests.Add(1)`.<br>2. Invokes `r.sem.Acquire(ctx, 1)` with a 3-second timeout context (`QueueTimeout`).<br>3. Decrements `QueuedRequests.Add(-1)` upon exit.<br>4. If timeout expires before acquiring a slot, replies with **`503 Service Unavailable`** to protect backends from cascading collapse.<br>5. Upon slot acquisition, increments `ActiveConcurrency.Add(1)` and defers both `ActiveConcurrency.Add(-1)` and `sem.Release(1)`.<br>6. Normalizes `req.Host` via `normalizeHost(req.Host)` (stripping port and lowercase conversion).<br>7. Queries `r.backends[cleanHost]` under read lock (`r.mu.RLock()`). If missing, replies with **`502 Bad Gateway`**.<br>8. Forwards the request downstream via `proxy.ServeHTTP(w, req)` through the pooled connection. |

---

#### Phase 4: Real-Time Telemetry Collection & SSE Streaming

| Go File | Lifecycle Role & Executed Logic |
| :--- | :--- |
| **`internal/server/server.go`** | **SSE Connection Management**: Client browser connects to `/api/events`. The handler sets HTTP streaming headers (`Content-Type: text/event-stream`, `Cache-Control: no-cache`), grabs the `http.Flusher`, and starts a 1-second `time.Ticker` loop. |
| **`internal/discovery/discovery.go`** | **Live Target Counting**: The ticker invokes `dockerProvider.Services()` to obtain the current count of healthy discovered backends. |
| **`internal/metrics/metrics.go`** | **Lock-Free Telemetry Snapshotting**: `collector.Snapshot(serviceCount)` takes atomic readings of `TotalRequests`, `ActiveConcurrency`, and `QueuedRequests`, packages them into a `MetricSnapshot`, and appends the snapshot to the ring buffer via bitwise index masking (`writeIdx & mask`). |
| **`internal/server/server.go`** | **Dual Event Dispatch**: <br>1. Formats an Out-of-Band (OOB) HTML snippet with updated metric counters and flushes `event: metrics` for HTMX DOM swaps.<br>2. Marshals `MetricSnapshot` into JSON and flushes `event: telemetry` for JavaScript Chart.js line graph updates. |
| **`ui/embed.go`** | **Frontend Presentation**: `ui/static/index.html` receives the dual events: HTMX automatically swaps `#metric-total-requests`, `#metric-active-concurrency`, `#metric-discovered-services`, and `#metric-queued-requests` in-place, while Chart.js dynamically plots concurrency trends. |

---

#### Phase 5: Synthetic Traffic Generation & Performance Validation

| Go File | Lifecycle Role & Executed Logic |
| :--- | :--- |
| **`cmd/trafficgen/main.go`** | **Synthetic Load Generator**: Standalone CLI binary simulating diverse traffic scenarios against the running gateway. Spawns configurable concurrent worker goroutines (`-concurrency`), hits valid routes (`app.local`), delayed routes (`slow.local`), unmapped routes (triggering 502s), bursts above queue limits (triggering 503s), and subscribes to `/api/events` to verify telemetry stability under load. Tracks response status codes and throughput metrics atomically. |

---

#### Phase 6: Automated Verification & Unit Test Suite

| Go File | Lifecycle Role & Executed Logic |
| :--- | :--- |
| **`test/unit/config_test.go`** | **Config Unit Tests**: Verifies environment variable override loading, invalid format handling, fallback defaults, and log level parsing. |
| **`test/unit/discovery_test.go`** | **Discovery Unit Tests**: Mocks the Docker daemon using `httptest.Server` responding with synthetic container JSON payloads. Tests container label filtering, IP address resolution across networks, fallback container port selection, healthy/exited status mapping, service listing, and cancellation handling in `Start()`. |
| **`test/unit/metrics_test.go`** | **Metrics Unit Tests**: Verifies `TelemetryRingBuffer` initialization, push behavior, bitwise wrap-around at capacity (1024), thread-safe concurrent writes, and `Collector` atomic counter increments. |
| **`test/unit/proxy_test.go`** | **Proxy Unit Tests**: Uses `httptest.Server` backends to verify reverse proxy routing, host header normalization, dynamic backend map replacement (`UpdateBackends`), default config fallbacks, and semaphore queue exhaustion timeouts returning 503. |
| **`test/unit/server_test.go`** | **Server Unit Tests**: Verifies Chi HTTP router endpoints (`/`, `/static/*`, `/api/events`, unmatched fallback routing), static file embedding, and SSE streaming headers. |

---

### 3. Go File Correlation Summary Matrix

| Go File | System Responsibility | Upstream Caller / Trigger | Downstream Dependencies |
| :--- | :--- | :--- | :--- |
| **`cmd/proxy/main.go`** | Application bootstrap, signal trapping, server lifecycle | OS / Docker container entrypoint | `internal/config`, `internal/metrics`, `internal/proxy`, `internal/discovery`, `internal/server` |
| **`cmd/trafficgen/main.go`** | Synthetic traffic generator and stress testing tool | Developer CLI / `./scripts/seed-traffic.sh` | Gateway HTTP server `:80` |
| **`internal/config/config.go`** | Environment variable loader & default validator | `cmd/proxy/main.go`, `test/unit/config_test.go` | `os.Getenv`, `log/slog` |
| **`internal/discovery/discovery.go`** | Docker socket scanner & container label parser | `cmd/proxy/main.go`, `test/unit/discovery_test.go` | `github.com/docker/docker/client` |
| **`internal/metrics/metrics.go`** | Lock-free ring buffer & atomic performance counters | `internal/proxy`, `internal/server`, `test/unit/metrics_test.go` | `sync/atomic`, `sync.RWMutex` |
| **`internal/proxy/proxy.go`** | Semaphore concurrency control & reverse proxy routing | `internal/server/server.go`, `test/unit/proxy_test.go` | `golang.org/x/sync/semaphore`, `net/http/httputil`, `internal/metrics` |
| **`internal/server/server.go`** | Chi HTTP routing, static assets, & SSE telemetry | `cmd/proxy/main.go`, `test/unit/server_test.go` | `github.com/go-chi/chi/v5`, `ui.Assets`, `internal/metrics`, `internal/proxy`, `internal/discovery` |
| **`ui/embed.go`** | Embedded static asset filesystem bundle | `internal/server/server.go` | `embed.FS` |
| **`test/unit/config_test.go`** | Unit test suite for runtime configuration | `go test ./test/unit/...` | `internal/config` |
| **`test/unit/discovery_test.go`**| Unit test suite for Docker discovery | `go test ./test/unit/...` | `internal/discovery`, Docker API types |
| **`test/unit/metrics_test.go`**  | Unit test suite for telemetry & ring buffer | `go test ./test/unit/...` | `internal/metrics` |
| **`test/unit/proxy_test.go`**    | Unit test suite for traffic queuing & routing | `go test ./test/unit/...` | `internal/proxy`, `internal/metrics` |
| **`test/unit/server_test.go`**   | Unit test suite for HTTP server & SSE endpoints | `go test ./test/unit/...` | `internal/server`, `internal/metrics`, `internal/proxy` |

---

## Traffic Monitoring & Control Flow

This section details how HTTP requests pass through the gateway, how each stage is actively monitored and throttled, and how real-time operational data is collected and transmitted to the web interface.

### 1. Request Lifecycle & Monitoring Diagram

```
[ Incoming HTTP Request ]
           │
           ▼
    Chi HTTP Router (internal/server/server.go)
           │
  ┌────────┴─────────────────────────────┬───────────────────────────────┐
  │ Route: /static/*, /                  │ Route: /api/events            │ Unmatched (r.NotFound)
  ▼                                      ▼                               ▼
ui/embed.go (Web Assets)       SSE Telemetry Stream             Router.ServeHTTP (internal/proxy/proxy.go)
                               (Pushes live metrics to UI)               │
                                                                         ├─► 1. TotalRequests.Add(1)
                                                                         │
                                                                         ├─► 2. QueuedRequests.Add(1)
                                                                         │      Wait for Semaphore Slot (timeout: 3s)
                                                                         │      QueuedRequests.Add(-1)
                                                                         │        └─► [Timeout] ──► Return 503 Service Unavailable
                                                                         │
                                                                         ├─► 3. ActiveConcurrency.Add(1)
                                                                         │      (defer ActiveConcurrency.Add(-1))
                                                                         │
                                                                         ├─► 4. Match req.Host in backends table
                                                                         │        └─► [No match] ──► Return 502 Bad Gateway
                                                                         │
                                                                         ▼
                                                                httputil.ReverseProxy ──► Downstream Container Backend
```

### 2. How Traffic is Monitored Step-by-Step

#### Step A: Instant Atomic Request Counting
Every incoming HTTP request arriving at `Router.ServeHTTP` immediately increments the atomic counter `TotalRequests`:
```go
r.metrics.TotalRequests.Add(1)
```
This is lock-free and thread-safe, ensuring zero latency overhead even under high traffic loads.

#### Step B: Queue Throttling & Wait Tracking
Before forwarding to backend containers:
1. `QueuedRequests` is incremented by 1:
   ```go
   r.metrics.QueuedRequests.Add(1)
   err := r.sem.Acquire(ctx, 1)
   r.metrics.QueuedRequests.Add(-1)
   ```
2. The request tries to acquire a permit from `semaphore.Weighted` within a 3-second timeout window (`QueueTimeout`).
3. `QueuedRequests` is decremented immediately upon either acquiring a permit or timing out.
4. If the queue is saturated and 3 seconds elapse without acquiring a permit, TrafficProxy rejects the request with `503 Service Unavailable`, protecting downstream containers from cascading overload.

#### Step C: Active Concurrency Tracking
Once a semaphore permit is acquired, `ActiveConcurrency` is incremented. A Go `defer` statement ensures that when the request finishes—whether successful, client-aborted, or terminated by a downstream timeout—the permit is released and `ActiveConcurrency` is decremented:
```go
r.metrics.ActiveConcurrency.Add(1)
defer r.metrics.ActiveConcurrency.Add(-1)
```

#### Step D: Docker Service Discovery Monitoring
In the background, `DockerProvider` polls `/var/run/docker.sock` every 5 seconds. It discovers running containers matching the `traffic-proxy.enable=true` label, checks their health, and dynamically tracks the current count of healthy upstream services (`DiscoveredServices`).

#### Step E: Lock-Free Ring Buffer Storage
Every second, `collector.Snapshot(serviceCount)` reads the current atomic metrics and writes a timestamped snapshot (`MetricSnapshot`) into a circular ring buffer (`TelemetryRingBuffer`, size: 1024):
```go
snapshot := collector.Snapshot(serviceCount)
```
This maintains a recent history of performance metrics entirely in-memory without generating heap allocations or garbage collection pauses.

#### Step F: Real-Time Telemetry Delivery (SSE + HTMX)
The endpoint `/api/events` maintains a persistent Server-Sent Events (SSE) connection with client browsers:
1. **DOM Updates (`event: metrics`)**: Pushes pre-rendered HTML snippets with `hx-swap-oob="outerHTML"`. HTMX on the browser automatically updates counter cards without reloading the page.
2. **Chart Updates (`event: telemetry`)**: Pushes JSON payloads containing timestamps and concurrency metrics directly to Chart.js, rendering dynamic real-time traffic graphs.

---

## Go (Golang) Primer for Beginners

A comprehensive, plain-English reference for developers who are new to Go or coming from other languages like JavaScript, Python, Java, or C#.

---

### 1. Go Syntax Demystified in Plain English

If you are reading Go code for the first time, you will notice a few patterns that look strange compared to traditional object-oriented languages. Here is what they actually mean:

#### A. Structs and Methods (No Classes or `this`)
Go does **not** have classes, inheritance, or a `this`/`self` keyword. Instead:
- Data fields are grouped into a **`struct`** (like a blueprint or TypeScript interface).
- Functions attached to that struct are called **methods**, and they declare a **receiver** in parentheses before the function name:

```go
// 1. Defining the struct
type Router struct {
    cfg      Config
    sem      *semaphore.Weighted
    backends map[string]*httputil.ReverseProxy
}

// 2. Attaching a method to Router
// (r *Router) is the receiver — it means "inside this function, 'r' refers to the Router calling this method"
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
    // Access struct fields using r.fieldName
    r.metrics.TotalRequests.Add(1)
}
```

#### B. Pointers (`*` and `&`) vs. Values
In Go, variables are passed by value (copied) by default. Pointers allow you to share and modify the original memory address:
- **`&MyStruct{...}`**: The `&` operator takes the **memory address** of a struct (creates a pointer).
- **`*Router`**: The `*` in a type definition means "a pointer pointing to a `Router` in memory, not a copy of it".
- **Why we use pointer receivers `(r *Router)`**:
  1. It avoids copying large structs in memory every time a method is called.
  2. It allows methods to modify the fields of the struct directly (e.g. updating the backend map).

#### C. Goroutines: Super-Cheap Threads (`go func()`)
In traditional languages (Python, Java, Node.js), spawning 1,000 OS threads will exhaust system memory. 
- Go has **goroutines**: green threads managed by the Go runtime that start at only **2 KB of memory**.
- Adding the `go` keyword before any function call runs it in the background immediately without blocking:
```go
go func() {
    dockerProvider.Start(ctx) // Runs continuously in the background!
}()
```
You can easily spawn 50,000+ goroutines on a single machine without crashing.

#### D. Channels (`chan`): Safe Conveyor Belts Between Goroutines
How do background goroutines talk to each other without corrupting memory? Go uses **channels**—thread-safe conveyor belts:
```go
// 1. Send data onto a channel:
d.eventsChan <- targets // "Puts new container targets onto the belt"

// 2. Receive data from a channel:
targets := <-sub        // "Waits and takes targets off the belt"
```

#### E. The `select` Statement: Event Listener for Channels
Think of `select` as `switch`, but for channels. It blocks until **one** of its cases is ready to send or receive data:
```go
select {
case <-ctx.Done():
    return // The app is shutting down, exit immediately!
case targets := <-sub:
    router.UpdateBackends(routes) // New containers found! Update proxy routes.
}
```

#### F. `defer`: Guaranteed Cleanup Before Exiting
The `defer` keyword schedules a function call to run **right before the surrounding function returns**, no matter how or where the function exits (even on errors):
```go
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
    r.metrics.ActiveConcurrency.Add(1)
    defer r.metrics.ActiveConcurrency.Add(-1) // Guaranteed to run when ServeHTTP finishes!

    // ... handle request ...
}
```
This guarantees you never forget to release a semaphore slot, unlock a mutex, or close an open connection.

#### G. Lock-Free Atomic Operations (`sync/atomic`)
Normally, if two threads change a counter at the same time, they cause race conditions and data corruption. Mutex locks solve this, but they force threads to wait in line.
- `sync/atomic` performs mathematical operations directly at the **CPU instruction level** (e.g. `LOCK XADD` on x86-64).
- `c.TotalRequests.Add(1)` and `c.TotalRequests.Load()` are 100% thread-safe and lock-free, executing in single-digit nanoseconds under heavy traffic.

#### H. Explicit Errors (No Hidden Exceptions)
Go does not have `try / catch / throw`. Functions return an error value alongside their result:
```go
provider, err := discovery.NewDockerProvider(interval)
if err != nil {
    // Handle the failure immediately!
    slog.Warn("Failed to connect to Docker", "error", err)
}
```
This forces developers to handle edge cases explicitly instead of letting unexpected exceptions crash the process.

#### I. Embedded Files (`//go:embed`)
The directive `//go:embed static/*` instructs the Go compiler to read the frontend files from your disk during `go build` and bake them directly into the binary executable. When running TrafficProxy, you do **not** need a `public/` or `dist/` folder on the server—everything is self-contained in a single executable file.

#### J. Capital vs. Lowercase Visibility
Go has no `public` or `private` keywords:
- **Capitalized name** (e.g., `ServeHTTP`, `Config`, `Router`): **Public / Exported** (accessible from other packages).
- **Lowercase name** (e.g., `normalizeHost`, `writeIdx`, `scan`): **Private / Unexported** (only accessible inside its own package).

---

### 2. What Each File's Core Function Does

Here is what every file in the project does in simple layman terms:

| File | Layman Metaphor | Core Responsibility |
| :--- | :--- | :--- |
| **`cmd/proxy/main.go`** | **The Grand Conductor** | Boots up the entire application. Loads settings, starts the background Docker scanner, connects discovery updates to the proxy router, starts the web server on `:80`, and gracefully shuts everything down when you press Ctrl+C. |
| **`internal/config/config.go`** | **The Settings Desk** | Reads environment variables (port, queue timeouts, concurrency limits) and validates them. If an environment variable is missing or invalid, it provides safe, battle-tested defaults. |
| **`internal/discovery/discovery.go`** | **The Container Scout** | Constantly monitors Docker in the background. Finds running containers labeled with `traffic-proxy.enable=true`, determines their IP and port, and alerts the proxy whenever containers appear, restart, or shut down. |
| **`internal/proxy/proxy.go`** | **The Traffic Bouncer & Router** | The reverse proxy engine. Accepts incoming web traffic, checks if the server is too busy using a semaphore, lines requests up in a queue if needed, finds the correct downstream container, and forwards the HTTP request. |
| **`internal/metrics/metrics.go`** | **The Scoreboard & Flight Recorder** | Keeps score of system performance. Tracks total requests, active concurrent connections, and queued requests with atomic counters, saving snapshots into a 1024-slot circular ring buffer in memory. |
| **`internal/server/server.go`** | **The Front Door & Broadcast Tower** | Sets up the web server routes. Serves the embedded dashboard files at `/`, pushes live real-time metrics over Server-Sent Events (SSE) at `/api/events`, and hands off all other requests to the proxy bouncer. |
| **`ui/embed.go`** | **The Packed Backpack** | Bundles the dashboard HTML, Tailwind CSS, HTMX, and Chart.js files directly into the compiled binary so the entire gateway can be deployed as a single standalone executable. |
| **`cmd/trafficgen/main.go`** | **The Stress Testing Drill** | A synthetic load generator CLI that fires concurrent streams of simulated HTTP requests against the proxy to test how it handles heavy loads, queueing, and traffic spikes. |
| **`test/unit/*.go`** | **The Safety Inspectors** | Isolated automated test suites that verify configuration parsing, container discovery, atomic metrics, and proxy routing to make sure new code changes don't break the system. |

---

### 3. Why We Need Semaphores, Queues, and Concurrency Limits

A common question from new Go developers is: *"Go can easily handle 50,000 goroutines at once—why do we need semaphores and queues to throttle requests?"*

#### The "Nightclub Bouncer" Analogy
Imagine TrafficProxy is a **bouncer** outside a popular nightclub, and your downstream containers (a Python Flask app, Node.js API, or database) are the **bartenders inside**:

1. **What happens WITHOUT a Semaphore?**
   - 10,000 visitors arrive at the nightclub at the exact same instant.
   - Go is so fast that it lets all 10,000 people rush through the doors into the bar simultaneously.
   - The bartenders (your downstream container) only have 4 staff members and 2 taps.
   - The bar gets overwhelmed, runs out of memory (OOM), crashes, and stops serving everyone. The entire backend collapses in a **cascading failure**.

2. **What the Weighted Semaphore Does (`semaphore.Weighted`)**:
   - The bouncer sets an occupancy limit (e.g. `MaxConcurrentRequests = 5000` in production, or `25` in local testing).
   - Only 5,000 requests are allowed to actively talk to the backends at any single moment.
   - Each request takes a "wristband" (permit) before entering and returns it when leaving.

3. **What the Request Queue Does (`QueuedRequests`)**:
   - When all 5,000 permits are in use, incoming request #5,001 does **not** get immediately rejected.
   - Instead, it waits patiently in line in memory (`QueuedRequests.Add(1)`).
   - As soon as request #200 finishes and releases its wristband, request #5,001 enters the bar.

4. **Why We MUST Have a Queue Timeout (`QueueTimeout: 3s` returning `503`)**:
   - What if the bartenders freeze up completely? Without a timeout, thousands of requests would wait in line forever, holding open connections and consuming memory until the proxy itself runs out of sockets.
   - If a request waits in line for more than **3 seconds**, TrafficProxy immediately cancels its wait and returns **`503 Service Unavailable`**.
   - **Why a fast 503 is a good thing**:
     - It immediately tells the client or load balancer: *"We are currently at capacity—retry in a few seconds."*
     - It instantly closes the HTTP connection and frees memory on the proxy.
     - It stops the traffic surge from reaching the already-struggling backend, giving your containers breathing room to recover and clear their backlogs.

5. **Why `sync/atomic` Instead of Mutex Locks for Metrics?**
   - A `sync.Mutex` is like a **single-occupancy bathroom with a lock on the door**: only one person can enter, lock the door, update a variable, and unlock the door. Under 10,000 requests/sec, goroutines spend most of their time waiting in line to grab the lock.
   - `sync/atomic` counters are like a **digital turnstile with an electronic clicker**: thousands of people can walk through simultaneously, and the hardware CPU directly increments the counter in nanoseconds with zero waiting.

---

### 4. Why Discovery Needs Fallbacks

When discovering containers dynamically via `/var/run/docker.sock`, real-world Docker environments are messy. Containers use different networking modes, omit configuration labels, or crash unexpectedly. TrafficProxy implements multi-layered fallbacks to guarantee resilience:

```
                  [ Discovered Docker Container ]
                                 │
     ┌───────────────────────────┼───────────────────────────┐
     ▼                           ▼                           ▼
[ IP Resolution ]       [ Port Resolution ]        [ Health Verification ]
  1. Bridge Network IP?   1. traffic-proxy.port?     1. State == "running"?
     └─► [No]                └─► [No]                   └─► [No: Ignore]
  2. Custom Mesh IP?      2. First Exposed Port?     2. TargetURL != nil?
     └─► [No]                └─► [No]                   └─► [No: Ignore]
  3. Fallback to Name     3. Fallback to Port 80     3. Healthy: Register Route!
```

#### A. Container IP Address Fallback
- **The Problem**: A container can be attached to multiple networks (e.g. Docker's default `bridge`, a user-defined compose network like `local-mesh`, and a database network). Some network adapters may not have an IP assigned yet, or may use internal overlay addressing.
- **The Fallback**: The discovery engine loops through `c.NetworkSettings.Networks`. As soon as it finds a valid, non-empty `net.IPAddress`, it uses it. If all network IP entries are empty (common in certain custom bridge configurations), it falls back to using the container's internal DNS name (`c.Names[0]`).

#### B. Target Port Fallback
- **The Problem**: Developers configure containers in different ways. Some specify a label, some define `EXPOSE` in their Dockerfile, and some do neither.
- **The Fallback Chain**:
  1. **Primary Check**: Looks for the explicit label `traffic-proxy.port` (e.g. `3000` or `8080`).
  2. **Secondary Check**: If the label is omitted, it inspects Docker's exposed port list (`c.Ports[0].PrivatePort`).
  3. **Final Fallback**: If no ports are declared anywhere, it defaults to standard HTTP port **`80`**.
  Without this three-tiered fallback, missing a single label would cause TrafficProxy to drop the container entirely.

#### C. Container State & Health Fallback
- **The Problem**: Containers frequently restart, crash, or enter an `exited` state during updates.
- **The Fallback**: Discovery explicitly checks `Healthy: c.State == "running"`. If a container is stopped, restarting, or unhealthy, the proxy ignores it and does **not** register it in the routing table. This prevents users from getting routed into a dead container.

#### D. Daemon Connection Fallback
- **The Problem**: What if Docker isn't installed locally (like on a developer workstation during quick testing), or the Docker daemon restarts?
- **The Fallback**: In `cmd/proxy/main.go`, if `discovery.NewDockerProvider` fails to connect to `/var/run/docker.sock`, it logs a warning:
  ```
  Docker provider initialization failed (continuing without docker sock)
  ```
  Instead of crashing the entire proxy binary, TrafficProxy continues running cleanly, serving the embedded UI dashboard and static routes.

---

### 5. Essential Go CLI Commands

```bash
# Download and clean up dependencies in go.mod and go.sum
go mod download
go mod tidy

# Run all unit tests with race detection and coverage check
go test -race -coverpkg=./internal/... -cover ./...

# Run static analysis (catches subtle bugs and unformatted code)
go vet ./...

# Compile binary locally for current OS
go build -ldflags="-s -w" -o traffic-proxy ./cmd/proxy

# Cross-compile a static Linux binary (matches production Docker scratch target)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -extldflags '-static'" -o traffic-proxy ./cmd/proxy
```

---

## Building & Testing Locally

```bash
# Run unit tests with race detection and coverage check
go test -race -coverpkg=./internal/... -cover ./...

# Run static analysis
go vet ./...

# Build static binary
CGO_ENABLED=0 go build -ldflags="-s -w" -o traffic-proxy ./cmd/proxy

# Build Docker image
docker build -t traffic-proxy:local .
```

### Local Development & Synthetic Traffic Replication

To spin up the local development harness with mock fast (`app.local`) and delayed (`slow.local`) backends and generate synthetic traffic:

```bash
# 1. Start local proxy and mock services
docker compose -f docker-compose.local.yml up -d --build

# 2. Seed diverse traffic patterns (30-second run with 20 workers)
./scripts/seed-traffic.sh start 20 30s

# 3. Simulate instant concurrency burst to exercise semaphore queueing & 503 limits
./scripts/seed-traffic.sh burst 40

# 4. Stream and observe live SSE telemetry events in the console
./scripts/seed-traffic.sh sse

# 5. Stop generator and teardown local containers
./scripts/seed-traffic.sh stop
docker compose -f docker-compose.local.yml down -v
```
