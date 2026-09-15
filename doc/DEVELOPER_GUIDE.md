# Developer Guide

Comprehensive architectural, structural, and technical reference for all directories and files in TrafficProxy Edge Gateway.

---

## Table of Contents

- [Project Overview](#project-overview)
- [Directory & File Reference](#directory--file-reference)
  - [Root Files](#root-files)
  - [cmd/](#cmd)
  - [internal/](#internal)
    - [internal/discovery/](#internaldiscovery)
    - [internal/metrics/](#internalmetrics)
    - [internal/proxy/](#internalproxy)
    - [internal/server/](#internalserver)
  - [ui/](#ui)
  - [test/](#test)
  - [.github/](#github)
  - [doc/](#doc)
  - [.agents/](#agents)
- [Traffic Monitoring & Control Flow](#traffic-monitoring--control-flow)
- [Go (Golang) Primer for Beginners](#go-golang-primer-for-beginners)
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
  - **Runtime Initialization**: Sets up structured JSON logging (`log/slog`), initializes root signal handling for `SIGINT`/`SIGTERM` via `signal.NotifyContext`, and creates core `metrics.Collector` and `proxy.Router` instances.
  - **Discovery Initialization**: Instantiates `discovery.DockerProvider` targeting `/var/run/docker.sock` and kicks off background polling.
  - **Dynamic Route Subscription**: Subscribes to discovery events and synchronizes the proxy routing table dynamically via `router.UpdateBackends(routes)`.
  - **Server Execution & Graceful Teardown**: Delegates router configuration to `server.SetupRouter`, runs HTTP server on `:80`, and manages graceful shutdown with a 10-second timeout deadline.

---

### internal/

Internal libraries and packages private to TrafficProxy.

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

## Traffic Monitoring & Control Flow

This section details how HTTP requests pass through the gateway, how each stage is actively monitored and throttled, and how real-time operational data is collected and transmitted to the web interface.

### 1. Request Lifecycle & Monitoring Diagram

```
[ Incoming HTTP Request ]
           │
           ▼
    Chi HTTP Router (cmd/proxy/main.go)
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

A guide for developers new to Go working on this project.

### 1. Key Go Concepts in This Codebase

- **Packages & Exporting**:
  - Code is organized into packages (e.g. `package proxy`, `package metrics`).
  - Identifiers starting with a **capital letter** (e.g., `ServeHTTP`, `Config`, `Router`) are **exported** (public to other packages).
  - Identifiers starting with a **lowercase letter** (e.g., `normalizeHost`, `writeIdx`) are **unexported** (private to their package).
  - The `internal/` directory is enforced by the Go compiler: packages inside `internal/` cannot be imported by external modules, preserving private architectural boundaries.

- **Structs and Methods (No Classes)**:
  - Go does not have classes or OOP inheritance. Instead, data fields are grouped into `struct` types:
    ```go
    type Router struct {
        cfg      Config
        sem      *semaphore.Weighted
        backends map[string]*httputil.ReverseProxy
    }
    ```
  - Functions attached to structs are called **methods**, declared with a receiver:
    ```go
    // (r *Router) is a pointer receiver: it operates directly on the Router instance
    func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) { ... }
    ```

- **Pointers (`*` and `&`)**:
  - `*Router` indicates a pointer to a `Router` instance (passed by reference, avoiding memory copies and enabling mutation).
  - `&Router{...}` allocates a `Router` struct and returns its memory address (pointer).

- **Concurrency: Goroutines, Channels, and `select`**:
  - `go func() { ... }()` spawns an ultra-lightweight concurrent thread called a **goroutine** (costing only a few kilobytes of stack memory).
  - **Channels (`chan`)** allow goroutines to pass typed data safely without manual locking:
    ```go
    sub := dockerProvider.Subscribe() // returns a receive-only channel: <-chan []ServiceTarget
    targets := <-sub                  // blocks until new targets are received
    ```
  - **`select`** waits on multiple channels concurrently:
    ```go
    select {
    case <-ctx.Done(): // triggers when shutdown is initiated
        return
    case targets := <-sub: // triggers when Docker discovers new containers
        router.UpdateBackends(routes)
    }
    ```

- **Context (`context.Context`)**:
  - Used throughout Go to manage timeouts, deadlines, and graceful shutdown signals across goroutines.
  - `ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)` creates a context that cancels automatically after 3 seconds.

- **Lock-Free Atomic Operations (`sync/atomic`)**:
  - Instead of mutex locks, TrafficProxy tracks high-throughput counters using atomic types (`atomic.Uint64`, `atomic.Int64`).
  - Methods like `c.TotalRequests.Add(1)` and `c.TotalRequests.Load()` compile into hardware-level atomic CPU instructions with zero lock contention.

- **Explicit Error Handling**:
  - Go does not have exceptions (`try/catch`). Functions return errors as normal return values:
    ```go
    err := r.sem.Acquire(ctx, 1)
    if err != nil {
        http.Error(w, "Service Overloaded", http.StatusServiceUnavailable)
        return
    }
    ```

- **Embedded Files (`//go:embed`)**:
  - In `ui/embed.go`, the directive `//go:embed static/*` bundles the HTML, CSS, and JS dashboard files directly into the compiled binary at build time.

### 2. Essential Go CLI Commands

```bash
# Download and clean up dependencies in go.mod and go.sum
go mod download
go mod tidy

# Run all unit tests with race detection
go test -race ./...

# Run static analysis
go vet ./...

# Compile binary locally
go build -o traffic-proxy ./cmd/proxy
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
