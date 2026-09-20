# TrafficProxy Edge Gateway

A lightweight, container-aware HTTP reverse proxy and edge gateway designed for homelabs and single-server deployments with cluster discovery expansion capability. 

It auto-discovers Docker backends via container labels, actively manages request queues with semaphores to prevent downstream overload, and presents operational metrics via an embedded HTMX dashboard.

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
│   ├── proxy/
│   │   └── main.go          # Binary entrypoint: Chi routing, telemetry setup, static UI serving
│   └── trafficgen/
│       └── main.go          # Synthetic traffic generator simulating diverse routing loads
├── doc/
│   ├── DEVELOPER_GUIDE.md   # In-depth file and directory reference
│   ├── PIPELINE.md          # CI/CD pipeline documentation
│   ├── UPDATE.md            # Container update and maintenance guide
│   └── USER_GUIDE.md        # User and deployment guide
├── internal/
│   ├── config/
│   │   └── config.go        # Environment variable runtime configurations
│   ├── discovery/
│   │   └── discovery.go     # Service discovery interface & Docker label provider
│   ├── metrics/
│   │   └── metrics.go       # Atomic counters & lock-free ring buffer for real-time telemetry
│   ├── proxy/
│   │   └── proxy.go         # Traffic queue management, semaphores & reverse proxying
│   └── server/
│       └── server.go        # Chi HTTP router, static UI file serving, proxy dispatch
├── scripts/
│   └── seed-traffic.sh      # CLI helper to generate continuous loads and bursts
├── test/
│   └── unit/                # Dedicated unit test suite decoupled from production code
├── ui/
│   ├── embed.go             # //go:embed static filesystem bundle
│   └── static/
│       └── index.html       # Embedded HTMX, Tailwind CSS & Chart.js dashboard
├── .dockerignore            # Docker build context exclusions
├── .golangci.yml            # golangci-lint linter configurations
├── Dockerfile               # Multi-stage scratch build (< 20MB)
├── docker-compose.yml       # Production Compose configuration
├── start.ps1                # PowerShell local build and start script
├── start.sh                 # Linux/Bash local build and start script
└── go.mod                   # Module definitions and dependencies
```

---

## Quickstart

### Running via Docker Compose

```bash
docker compose up -d --build
```

Access the dashboard at `http://localhost`.

### Local Development & Traffic Replication

To test local builds natively with mock backends and synthetic traffic:

#### 1. Start the Local Proxy

**Linux / macOS (Bash):**

- Start with mock Docker backends (`app.local` and `slow.local`):
```bash
./start.sh -m
```

- Start standalone proxy:
```bash
./start.sh
```

- Start on custom port with debug logging:
```bash
./start.sh -p 8080 -l DEBUG
```

**Windows (PowerShell):**

- Start with mock Docker backends (`app.local` and `slow.local`):
```powershell
.\start.ps1 -WithMockBackends
```

- Start standalone proxy:
```powershell
.\start.ps1
```

- Start on custom port with debug logging:
```powershell
.\start.ps1 -Port 8080 -LogLevel DEBUG
```

#### 2. Seed Diverse Traffic Patterns

Generate synthetic multi-route traffic for 30 seconds across 20 workers:
```bash
./scripts/seed-traffic.sh start 20 30s
```

#### 3. Simulate High-Concurrency Bursts

Trigger an instant burst of 40 concurrent requests to test semaphore queues and 503 limits:
```bash
./scripts/seed-traffic.sh burst 40
```

#### 4. Stop Traffic Generator

Terminate background traffic workers:
```bash
./scripts/seed-traffic.sh stop
```

#### 5. Teardown Mock Backends

Stop and remove mock Docker containers:

**Linux / macOS (Bash):**
```bash
./start.sh --stop-backends
```

**Windows (PowerShell):**
```powershell
.\start.ps1 -StopBackends
```

#### Start Script Options

| Linux / macOS (`./start.sh`) | Windows (`.\start.ps1`) | Description | Default |
| :--- | :--- | :--- | :--- |
| `-p`, `--port <port>` | `-Port <string>` | Gateway listening port | `:80` |
| `-c`, `--concurrency <num>` | `-MaxConcurrent <int>` | Maximum concurrent requests | `25` |
| `-t`, `--timeout <duration>` | `-QueueTimeout <string>` | Queue timeout before 503 | `500ms` |
| `-i`, `--interval <duration>` | `-PollInterval <string>` | Docker discovery poll interval | `2s` |
| `-l`, `--log-level <level>` | `-LogLevel <string>` | Log verbosity (DEBUG, INFO, WARN, ERROR) | `DEBUG` |
| `-m`, `--with-mock-backends` | `-WithMockBackends` | Spin up mock backend containers in Docker | `false` |
| `--stop-backends` | `-StopBackends` | Stop and remove mock containers | - |

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
4. **Embedded UI (`ui/`)**: Single static executable binary serving embedded HTMX, Tailwind CSS, and Chart.js dashboard (real-time SSE streaming planned for Milestone 5).
