# TrafficProxy Edge Gateway

A lightweight, container-aware HTTP reverse proxy and edge gateway designed for homelabs and single-server deployments with cluster discovery expansion capability. 

It auto-discovers Docker backends via container labels, actively manages request queues with semaphores to prevent downstream overload, and streams real-time telemetry to an embedded HTMX dashboard via Server-Sent Events (SSE).

---

## Tech Stack

- **Core Engine**: Go 1.22+
- **Routing & Proxy**: `net/http/httputil` and `github.com/go-chi/chi/v5`
- **Concurrency Control**: Native Go channels, `sync/atomic` counters, and `golang.org/x/sync/semaphore`
- **Discovery**: Docker Engine API (`github.com/docker/docker/client`) targeting `/var/run/docker.sock`
- **Telemetry State**: High-performance in-memory lock-free ring buffer
- **Frontend**: HTMX, Tailwind CSS, and Chart.js embedded into static binary via Go `//go:embed`
- **Deployment**: Static multi-stage Docker compilation (`scratch` container, `< 20MB`)

---

## Documentation

- [Developer Guide](doc/DEVELOPER_GUIDE.md): In-depth breakdown explaining what every file and directory does.
- [User Guide](doc/USER_GUIDE.md): Deployment, container label configuration, and dashboard monitoring.
- [CI/CD Pipeline](doc/PIPELINE.md): CI workflow specification and local execution guide.
- [Update Guide](doc/UPDATE.md): Container update and maintenance procedures.

---

## Project Directory Structure

For an in-depth explanation of every file and directory, refer to the [Developer Guide](doc/DEVELOPER_GUIDE.md).

```
Traffic-Proxy-Dashboard/
├── .agents/                 # Workspace skills and documentation rules
├── .github/
│   └── workflows/
│       └── ci.yml           # GitHub Actions CI/CD workflow
├── cmd/
│   └── proxy/
│       └── main.go          # Binary entrypoint: Chi routing, SSE telemetry, static UI serving
├── doc/
│   ├── DEVELOPER_GUIDE.md   # In-depth file and directory reference
│   ├── PIPELINE.md          # CI/CD pipeline documentation
│   ├── UPDATE.md            # Container update and maintenance guide
│   └── USER_GUIDE.md        # User and deployment guide
├── internal/
│   ├── discovery/
│   │   └── discovery.go     # Service discovery interface & Docker label provider
│   ├── metrics/
│   │   └── metrics.go       # Atomic counters & lock-free ring buffer for real-time telemetry
│   └── proxy/
│       └── proxy.go         # Traffic queue management, semaphores & reverse proxying
├── ui/
│   ├── embed.go             # //go:embed static filesystem bundle
│   └── static/
│       └── index.html       # Embedded HTMX, Tailwind CSS & Chart.js dashboard
├── .dockerignore            # Docker build context exclusions
├── .golangci.yml            # golangci-lint linter configurations
├── Dockerfile               # Multi-stage scratch build (< 20MB)
├── docker-compose.yml       # Production Compose configuration
└── go.mod                   # Module definitions and dependencies
```

---

## Quickstart

### Running via Docker Compose

```bash
docker compose up -d --build
```

Access the dashboard at `http://localhost`.

---

## Auto-Discovery Label Configuration

To expose a Docker container through TrafficProxy, add the following labels to your target container:

```yaml
labels:
  - "traffic-proxy.enable=true"
  - "traffic-proxy.rule=app.local"
```

TrafficProxy will poll `/var/run/docker.sock` and update internal routing tables dynamically.

---

## Architecture Components

1. **Modular Discovery (`internal/discovery`)**: Abstract `Provider` interface (`Start()`, `Services()`, `Subscribe()`) allowing simple expansion to Docker Swarm, Nomad, Kubernetes, or gossip-based cluster management.
2. **Traffic Control (`internal/proxy`)**: Uses `golang.org/x/sync/semaphore` to cap max active concurrency and queue incoming HTTP requests safely with configurable timeout rejection (`503 Service Unavailable`).
3. **Telemetry Engine (`internal/metrics`)**: Atomic counters for active requests and queued concurrency feeding into a lock-free power-of-two ring buffer.
4. **Embedded UI (`ui/`)**: Single static executable binary serving static assets from embedded FS and streaming SSE updates directly to HTMX frontend.
