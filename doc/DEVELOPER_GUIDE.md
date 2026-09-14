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
  - [ui/](#ui)
  - [.github/](#github)
  - [doc/](#doc)
  - [.agents/](#agents)
- [Control & Data Flows](#control--data-flows)
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
  - Configures `golangci-lint` (version 2 schema). Enables `errcheck`, `gosimple`, `govet` (with `enable-all: true`), `ineffassign`, `staticcheck`, `unused`, `gofmt`, and `misspell`. Sets execution timeout to 5 minutes.

- **`.dockerignore`**:
  - Excludes `.git`, `.gitignore`, `README.md`, `Dockerfile`, `docker-compose.yml`, `learning_proposal.md`, and `*.log` from the Docker build context.

- **`.gitignore`**:
  - Ignores build artifacts (`*.exe`, `*.so`, `*.test`), test coverage files (`coverage.*`, `*.out`), workspace files (`go.work`), environment files (`.env`), and IDE configurations (`.idea/`, `.vscode/`).

---

### cmd/

Application entrypoints and runnable binary mains.

#### `cmd/proxy/`
- **`cmd/proxy/main.go`**:
  - Main binary entrypoint.
  - **Logging**: Initializes structured JSON logging via Go `log/slog` writing to `os.Stdout`.
  - **Signal Handling**: Sets up context cancellation responding to `os.Interrupt` and `syscall.SIGTERM`.
  - **Component Instantiation**: Initializes `metrics.NewCollector()` and `proxy.NewRouter()` with a maximum concurrency limit of 5,000 requests and a 3-second queue timeout.
  - **Docker Discovery**: Instantiates `discovery.NewDockerProvider()` with a 5-second polling interval. Spawns background goroutines to run the scanner (`dockerProvider.Start(ctx)`) and subscribe to target updates (`dockerProvider.Subscribe()`), continuously pushing route tables to the router (`router.UpdateBackends(routes)`).
  - **Router & Middleware**: Constructs a Chi router (`chi.NewRouter()`) equipped with `middleware.Logger` and `middleware.Recoverer`.
  - **UI Serving**: Mounts the embedded static assets (`fs.Sub(ui.Assets, "static")`) under `/static/*` and serves `/index.html` at the root route `/`.
  - **SSE Telemetry Endpoint**: Implements `/api/events` using `http.Flusher`. Emits dual Server-Sent Events every second:
    1. `event: metrics`: Out-of-Band (OOB) HTML snippet updating HTMX DOM nodes (`#metric-total-requests`, `#metric-active-concurrency`, `#metric-discovered-services`, `#metric-queued-requests`).
    2. `event: telemetry`: Raw JSON string of the current `MetricSnapshot` for Chart.js graphing.
  - **Reverse Proxy Catch-All**: Routes all unmatched paths through `r.NotFound(router.ServeHTTP)`.
  - **Lifecycle Management**: Runs HTTP server on `:80` and manages graceful shutdown with a 10-second timeout deadline.

---

### internal/

Internal libraries and packages private to TrafficProxy.

#### internal/discovery/
Container auto-discovery and backend synchronization.

- **`internal/discovery/discovery.go`**:
  - **`ServiceTarget`**: Struct capturing discovered container metadata (`ID`, `Name`, `Host`, `Port`, `HostRule`, `TargetURL`, `Labels`, `Healthy`, `CreatedAt`).
  - **`Provider` Interface**: Generic abstraction exposing `Name() string`, `Start(ctx context.Context) error`, `Services() ([]ServiceTarget, error)`, and `Subscribe() <-chan []ServiceTarget`. Designed to support Swarm, Nomad, Kubernetes, or gossip backends.
  - **`DockerProvider`**: Implements `Provider` using `github.com/docker/docker/client`.
  - **`scan(ctx)`**: Queries `/var/run/docker.sock` via `ContainerList`. Filters containers possessing the label `traffic-proxy.enable=true` and an active `traffic-proxy.rule`. Resolves container network IP addresses across attached networks and port definitions (`traffic-proxy.port` -> exposed port -> fallback `80`). Updates internal synchronized slice and publishes targets to the subscriber channel.

#### internal/metrics/
Thread-safe metrics collection and telemetry storage.

- **`internal/metrics/metrics.go`**:
  - **`MetricSnapshot`**: JSON-serializable telemetry payload holding `Timestamp`, `TotalRequests`, `ActiveConcurrency`, `QueuedRequests`, and `DiscoveredServices`.
  - **`TelemetryRingBuffer`**: Thread-safe ring buffer utilizing bitwise power-of-two capacity masking (`writeIdx & mask`) for high-throughput slot placement without pointer reshuffling.
  - **`Collector`**: Central metrics aggregator utilizing atomic primitives (`sync/atomic.Uint64` for total request counter, `sync/atomic.Int64` for active concurrency and queue gauges).
  - **`Snapshot(discoveredCount int)`**: Takes an atomic reading of all metrics, appends the snapshot into the ring buffer, and returns the snapshot instance.
- **`internal/metrics/metrics_test.go`**:
  - Unit tests verifying `TelemetryRingBuffer` initialization, push behavior, circular write wrap-around, empty buffer handling, and concurrent access.
  - Unit tests verifying `Collector` atomic counter increments and snapshot generation.

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
  - **`normalizeHost(host)`**: Helper utility extracting hostnames from host/port pairs and returning lowercase strings.
- **`internal/proxy/proxy_test.go`**:
  - Unit tests verifying reverse proxy routing, backend map updates, semaphore concurrency limits, queue timeout rejections, and host header normalization.

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

### .github/

Continuous integration and automated GitHub workflows.

#### `.github/workflows/`
- **`.github/workflows/ci.yml`**:
  - GitHub Actions CI/CD workflow (`Fuckass Pipeline`) containing four jobs:
    - **`lint`**: Executes `go vet` and `golangci-lint-action` using root [`.golangci.yml`](file:///home/ninonakano/Desktop/Traffic-Proxy-Dashboard/.golangci.yml).
    - **`test`**: Executes `go test -race` with atomic coverage profile generation, enforcing a strict minimum coverage threshold of 60%.
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

## Control & Data Flows

```
[ Incoming HTTP Request ]
          │
          ▼
   cmd/proxy/main.go (Chi Router)
          │
          ├── /static/*, /  ──► ui/embed.go (Embedded Dashboard)
          ├── /api/events   ──► internal/metrics/metrics.go (SSE Stream)
          │
          └── Catch-All (r.NotFound)
                    │
                    ▼
          internal/proxy/proxy.go (Router.ServeHTTP)
                    │
                    ├─► Acquire Semaphore (Queue Timeout: 3s)
                    │     └─► [Timeout] ──► 503 Service Unavailable
                    │
                    ├─► Lookup Host in Routes Map
                    │     └─► [Not Found] ──► 502 Bad Gateway
                    │
                    ▼
          httputil.ReverseProxy ──► Downstream Container Backend
```

---

## Building & Testing Locally

```bash
# Run unit tests with race detection and coverage check
go test -race -cover ./...

# Run static analysis
go vet ./...

# Build static binary
CGO_ENABLED=0 go build -ldflags="-s -w" -o traffic-proxy ./cmd/proxy

# Build Docker image
docker build -t traffic-proxy:local .
```
