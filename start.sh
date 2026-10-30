#!/usr/bin/env bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Load .env file if present
if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  source .env 2>/dev/null || true
  set +a
fi

PORT="${PROXY_PORT:-:80}"
ENVIRONMENT="${ENVIRONMENT:-DEVELOPMENT}"
MAX_CONCURRENT="${PROXY_MAX_CONCURRENT:-25}"
QUEUE_TIMEOUT="${PROXY_QUEUE_TIMEOUT:-500ms}"
POLL_INTERVAL="${DOCKER_POLL_INTERVAL:-2s}"
LOG_LEVEL="${LOG_LEVEL:-DEBUG}"
WITH_MOCK_BACKENDS="${WITH_MOCK_BACKENDS:-false}"
START_BACKENDS=false
STOP_BACKENDS=false
CLEAN=false
KEEP_BINARY=false
WATCH=false

usage() {
  echo "Usage: $0 [options]"
  echo ""
  echo "Options:"
  echo "  -e, --env <env>               Environment: DEVELOPMENT, PRODUCTION (default: DEVELOPMENT)"
  echo "  -p, --port <port>             Port to listen on (default: :80 or \$PROXY_PORT)"
  echo "  -c, --concurrency <num>       Max concurrent requests (default: 25)"
  echo "  -t, --timeout <duration>      Queue timeout duration (default: 500ms)"
  echo "  -i, --interval <duration>     Docker poll interval (default: 2s)"
  echo "  -l, --log-level <level>       Log level: DEBUG, INFO, WARN, ERROR (default: DEBUG)"
  echo "  -m, --with-mock-backends      Launch mock backend containers (or set WITH_MOCK_BACKENDS=true in .env)"
  echo "  --start-backends              Start mock backend containers and exit"
  echo "  --stop-backends               Stop mock backend containers and exit"
  echo "  --clean                       Remove compiled binaries from workspace and exit"
  echo "  --keep-binary                 Preserve compiled binary after exit (default: auto-clean)"
  echo "  -w, --watch                   Enable live reload via Air (watches Go and UI files)"
  echo "  -h, --help                    Show this help message"
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -e|--env)
      ENVIRONMENT="$2"
      shift 2
      ;;
    -p|--port)
      PORT="$2"
      shift 2
      ;;
    -c|--concurrency)
      MAX_CONCURRENT="$2"
      shift 2
      ;;
    -t|--timeout)
      QUEUE_TIMEOUT="$2"
      shift 2
      ;;
    -i|--interval)
      POLL_INTERVAL="$2"
      shift 2
      ;;
    -l|--log-level)
      LOG_LEVEL="$2"
      shift 2
      ;;
    -m|--with-mock-backends)
      WITH_MOCK_BACKENDS=true
      shift
      ;;
    --start-backends)
      START_BACKENDS=true
      shift
      ;;
    --stop-backends)
      STOP_BACKENDS=true
      shift
      ;;
    --clean)
      CLEAN=true
      shift
      ;;
    --keep-binary)
      KEEP_BINARY=true
      shift
      ;;
    -w|--watch)
      WATCH=true
      shift
      ;;
    -h|--help)
      usage
      ;;
    *)
      echo "Unknown option: $1"
      usage
      ;;
  esac
done

clean_binaries() {
  echo "==> Cleaning up compiled binaries..."
  rm -f "$SCRIPT_DIR/traffic-proxy" "$SCRIPT_DIR/traffic-proxy.exe" "$SCRIPT_DIR"/*.exe 2>/dev/null || true
}

if [ "$CLEAN" = true ]; then
  clean_binaries
  exit 0
fi

MOCK_CONTAINERS=(
  "web-frontend-1"
  "web-frontend-2"
  "api-service"
  "auth-service"
  "store-service"
  "backend-slow-local"
  "backend-api-local"
)

stop_mock_containers() {
  if docker info >/dev/null 2>&1; then
    echo "==> Stopping mock backends..."
    docker stop "${MOCK_CONTAINERS[@]}" >/dev/null 2>&1 || true
    docker rm -f "${MOCK_CONTAINERS[@]}" >/dev/null 2>&1 || true
    echo "Mock backends stopped."
  else
    echo "Docker is not running; skipping container cleanup."
  fi
}

ensure_docker_service() {
  if docker info >/dev/null 2>&1; then
    return 0
  fi

  echo "==> Docker daemon is not responding. Checking Docker service..."

  # Arch Linux / systemd service check and activation
  if command -v systemctl >/dev/null 2>&1; then
    local svc_status
    svc_status=$(systemctl is-active docker 2>/dev/null || echo "inactive")
    if [ "$svc_status" != "active" ]; then
      echo "==> Docker service is '$svc_status'. Attempting to start docker.service..."
      if sudo -n systemctl start docker 2>/dev/null; then
        echo "==> Docker service started."
      else
        echo "==> Prompting for sudo to start docker.service on Arch Linux..."
        sudo systemctl start docker || true
      fi
    fi
  fi

  # Wait up to 5 seconds for Docker socket to become available
  local retries=5
  while [ $retries -gt 0 ]; do
    if docker info >/dev/null 2>&1; then
      echo "==> Docker daemon is ready."
      return 0
    fi
    sleep 1
    retries=$((retries - 1))
  done

  # Socket permission inspection
  if [ -e "/var/run/docker.sock" ] && ! [ -w "/var/run/docker.sock" ]; then
    echo "Error: Current user ($USER) cannot write to /var/run/docker.sock."
    echo "Fix: Add user to the docker group: sudo usermod -aG docker $USER && newgrp docker"
  else
    echo "Error: Docker daemon is not reachable at /var/run/docker.sock."
    echo "Fix: Start Docker manually via: sudo systemctl start docker"
  fi
  return 1
}

start_mock_containers() {
  if ! ensure_docker_service; then
    echo "Warning: Docker daemon is not reachable. Cannot start mock backends without Docker."
    return 1
  fi

  # Check if mock backends are already running
  if docker inspect -f '{{.State.Running}}' web-frontend-1 2>/dev/null | grep -q true; then
    echo "==> Mock backends are already running."
    return 0
  fi

  echo "==> Starting mock backend containers (simulating multi-tier website cluster)..."
  docker network create local-mesh >/dev/null 2>&1 || true

  # 1. Web Frontend Node 1 (web.local - round-robin pool)
  docker rm -f web-frontend-1 >/dev/null 2>&1 || true
  docker run -d --name web-frontend-1 \
    --network local-mesh \
    --label "traffic-proxy.enable=true" \
    --label "traffic-proxy.rule=web.local" \
    --label "traffic-proxy.balance=round-robin" \
    --label "traffic-proxy.port=5678" \
    hashicorp/http-echo:0.2.3 -text="OK: Web Frontend Node 1 (web.local)\n" -listen=:5678 >/dev/null

  # 2. Web Frontend Node 2 (web.local - round-robin pool)
  docker rm -f web-frontend-2 >/dev/null 2>&1 || true
  docker run -d --name web-frontend-2 \
    --network local-mesh \
    --label "traffic-proxy.enable=true" \
    --label "traffic-proxy.rule=web.local" \
    --label "traffic-proxy.balance=round-robin" \
    --label "traffic-proxy.port=5678" \
    hashicorp/http-echo:0.2.3 -text="OK: Web Frontend Node 2 (web.local)\n" -listen=:5678 >/dev/null

  # 3. Core API Service (app.local/api - path routing & strip-prefix)
  docker rm -f api-service >/dev/null 2>&1 || true
  docker run -d --name api-service \
    --network local-mesh \
    --label "traffic-proxy.enable=true" \
    --label "traffic-proxy.rule=app.local" \
    --label "traffic-proxy.path=/api" \
    --label "traffic-proxy.strip-prefix=true" \
    --label "traffic-proxy.priority=10" \
    --label "traffic-proxy.port=5678" \
    hashicorp/http-echo:0.2.3 -text="OK: Core API Service (app.local/api)\n" -listen=:5678 >/dev/null

  # 4. Auth & Identity Service (app.local/auth - path routing)
  docker rm -f auth-service >/dev/null 2>&1 || true
  docker run -d --name auth-service \
    --network local-mesh \
    --label "traffic-proxy.enable=true" \
    --label "traffic-proxy.rule=app.local" \
    --label "traffic-proxy.path=/auth" \
    --label "traffic-proxy.priority=5" \
    --label "traffic-proxy.port=5678" \
    hashicorp/http-echo:0.2.3 -text="OK: Auth & Identity Service (app.local/auth)\n" -listen=:5678 >/dev/null

  # 5. Store / E-Commerce Catalog Service (store.local)
  docker rm -f store-service >/dev/null 2>&1 || true
  docker run -d --name store-service \
    --network local-mesh \
    --label "traffic-proxy.enable=true" \
    --label "traffic-proxy.rule=store.local" \
    --label "traffic-proxy.port=5678" \
    hashicorp/http-echo:0.2.3 -text="OK: Store & Catalog Service (store.local)\n" -listen=:5678 >/dev/null

  # 6. Analytics / Heavy Delayed Service (slow.local - 200ms delay)
  PYTHON_SCRIPT='from http.server import HTTPServer, BaseHTTPRequestHandler
import time
class DelayedHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        time.sleep(0.2)
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.end_headers()
        self.wfile.write(b"OK: Delayed response from analytics-slow (slow.local)\n")
    def log_message(self, format, *args):
        pass
print("Starting slow backend on :8080 (delay: 200ms)...")
HTTPServer(("", 8080), DelayedHandler).serve_forever()'

  docker rm -f backend-slow-local >/dev/null 2>&1 || true
  docker run -d --name backend-slow-local \
    --network local-mesh \
    --label "traffic-proxy.enable=true" \
    --label "traffic-proxy.rule=slow.local" \
    --label "traffic-proxy.port=8080" \
    python:3.12-alpine python3 -c "$PYTHON_SCRIPT" >/dev/null

  echo "Mock backends started (6 containers):"
  echo "  - web-frontend-1 & web-frontend-2 (http://web.local/ [round-robin])"
  echo "  - api-service (http://app.local/api [prefix stripped])"
  echo "  - auth-service (http://app.local/auth)"
  echo "  - store-service (http://store.local/)"
  echo "  - backend-slow-local (http://slow.local/ [200ms latency])"
}

if [ "$START_BACKENDS" = true ]; then
  start_mock_containers
  exit $?
fi

if [ "$STOP_BACKENDS" = true ]; then
  stop_mock_containers
  exit 0
fi

if [[ "$WITH_MOCK_BACKENDS" =~ ^(true|1|yes|TRUE|YES)$ ]]; then
  start_mock_containers
fi

cleanup() {
  if [ "$KEEP_BINARY" != true ]; then
    echo "==> Cleaning up compiled binary..."
    rm -f "$SCRIPT_DIR/traffic-proxy" "$SCRIPT_DIR/traffic-proxy.exe" 2>/dev/null || true
  fi
  if [ "$WITH_MOCK_BACKENDS" = true ]; then
    stop_mock_containers
  fi
}
trap cleanup EXIT INT TERM

if [[ "$PORT" != :* ]]; then
  PORT=":$PORT"
fi

# Remove stale binary before compiling
rm -f "$SCRIPT_DIR/traffic-proxy" "$SCRIPT_DIR/traffic-proxy.exe" 2>/dev/null || true

# Verify Go binary availability
if ! command -v go >/dev/null 2>&1; then
  if [ -x "/usr/local/go/bin/go" ]; then
    export PATH="/usr/local/go/bin:$PATH"
  elif [ -x "$HOME/go/bin/go" ]; then
    export PATH="$HOME/go/bin:$PATH"
  elif [ -x "$HOME/.local/go/bin/go" ]; then
    export PATH="$HOME/.local/go/bin:$PATH"
  fi
fi

if [ -d "$HOME/go/bin" ]; then
  export PATH="$HOME/go/bin:$PATH"
fi

if ! command -v go >/dev/null 2>&1; then
  echo "Error: 'go' binary not found. Go is not installed on this system or not in PATH."
  echo "Install it via: sudo pacman -S go"
  exit 1
fi

export ENVIRONMENT="$ENVIRONMENT"
export PROXY_PORT="$PORT"
export PROXY_MAX_CONCURRENT="$MAX_CONCURRENT"
export PROXY_QUEUE_TIMEOUT="$QUEUE_TIMEOUT"
export DOCKER_POLL_INTERVAL="$POLL_INTERVAL"
export LOG_LEVEL="$LOG_LEVEL"

port_num="${PORT#:}"
if command -v ss >/dev/null 2>&1; then
  if ss -tulpn 2>/dev/null | grep -q ":$port_num "; then
    echo "Error: Port $port_num is already in use by another process. Please stop the existing process or set a different PROXY_PORT."
    exit 1
  fi
fi

if [ "$WATCH" = true ]; then
  if ! command -v air >/dev/null 2>&1; then
    echo "Error: 'air' binary not found. Install it via: go install github.com/air-verse/air@latest"
    exit 1
  fi
  echo "==> Starting SanProx in live reload mode via Air on port $PORT..."
  air
  exit $?
fi

echo "==> Building traffic-proxy binary..."
go build -o traffic-proxy ./cmd/proxy

echo "==> Starting SanProx on port $PORT (Environment: $ENVIRONMENT, LogLevel: $LOG_LEVEL, MaxConcurrent: $MAX_CONCURRENT, QueueTimeout: $QUEUE_TIMEOUT, MockBackends: $WITH_MOCK_BACKENDS)..."
echo "Press Ctrl+C to stop."

./traffic-proxy
