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
WITH_MOCK_BACKENDS=false
STOP_BACKENDS=false

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
  echo "  -m, --with-mock-backends      Launch mock backend containers via Docker"
  echo "  --stop-backends               Stop mock backend containers and exit"
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
    --stop-backends)
      STOP_BACKENDS=true
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

stop_mock_containers() {
  if docker info >/dev/null 2>&1; then
    echo "==> Stopping mock backends..."
    docker stop backend-api-local backend-slow-local >/dev/null 2>&1 || true
    docker rm -f backend-api-local backend-slow-local >/dev/null 2>&1 || true
    echo "Mock backends stopped."
  else
    echo "Docker is not running; skipping container cleanup."
  fi
}

if [ "$STOP_BACKENDS" = true ]; then
  stop_mock_containers
  exit 0
fi

if [ "$WITH_MOCK_BACKENDS" = true ]; then
  if ! docker info >/dev/null 2>&1; then
    echo "Warning: Docker daemon is not reachable. Cannot start mock backends without Docker."
  else
    echo "==> Starting mock backend containers..."
    docker network create local-mesh >/dev/null 2>&1 || true

    docker rm -f backend-api-local >/dev/null 2>&1 || true
    docker run -d --name backend-api-local \
      --network local-mesh \
      --label "traffic-proxy.enable=true" \
      --label "traffic-proxy.rule=app.local" \
      --label "traffic-proxy.port=5678" \
      hashicorp/http-echo:0.2.3 -text="OK: Response from backend-api (app.local)\n" -listen=:5678 >/dev/null

    PYTHON_SCRIPT='from http.server import HTTPServer, BaseHTTPRequestHandler
import time
class DelayedHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        time.sleep(0.2)
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.end_headers()
        self.wfile.write(b"OK: Delayed response from backend-slow (slow.local)\n")
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

    echo "Mock backends started: backend-api-local (app.local), backend-slow-local (slow.local)"
    trap 'stop_mock_containers' EXIT INT TERM
  fi
fi

if [[ "$PORT" != :* ]]; then
  PORT=":$PORT"
fi

echo "==> Building traffic-proxy binary..."
go build -o traffic-proxy ./cmd/proxy

export ENVIRONMENT="$ENVIRONMENT"
export PROXY_PORT="$PORT"
export PROXY_MAX_CONCURRENT="$MAX_CONCURRENT"
export PROXY_QUEUE_TIMEOUT="$QUEUE_TIMEOUT"
export DOCKER_POLL_INTERVAL="$POLL_INTERVAL"
export LOG_LEVEL="$LOG_LEVEL"

echo "==> Starting SanProx on port $PORT (Environment: $ENVIRONMENT, LogLevel: $LOG_LEVEL, MaxConcurrent: $MAX_CONCURRENT, QueueTimeout: $QUEUE_TIMEOUT)..."
echo "Press Ctrl+C to stop."

./traffic-proxy
