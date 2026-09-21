#!/usr/bin/env bash
set -e

TARGET="${TARGET:-http://localhost:80}"
PID_FILE="/tmp/traffic-proxy-seeder.pid"

usage() {
  echo "Usage: $0 {start|burst|sse|stop|status}"
  echo ""
  echo "Commands:"
  echo "  start [concurrency] [duration]  Launch continuous traffic generator against $TARGET"
  echo "  burst [concurrency]             Trigger an instant high-concurrency burst to test queueing & 503s"
  echo "  sse                             Stream live Server-Sent Events (SSE) telemetry in terminal"
  echo "  stop                            Terminate background traffic workers"
  echo "  status                          Check if background traffic generator is running"
  exit 1
}

case "$1" in
  start)
    CONCURRENCY="${2:-15}"
    DURATION="${3:-30s}"
    echo "==> Starting traffic generator targeting $TARGET (concurrency: $CONCURRENCY, duration: $DURATION)..."
    if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
      echo "Traffic generator already running (PID: $(cat "$PID_FILE")). Stop it first."
      exit 1
    fi
    go run ./cmd/trafficgen -target "$TARGET" -concurrency "$CONCURRENCY" -duration "$DURATION" &
    echo $! > "$PID_FILE"
    echo "Generator started in background (PID: $(cat "$PID_FILE")). View logs via 'tail -f' or run './scripts/seed-traffic.sh status'."
    ;;

  burst)
    BURST_COUNT="${2:-35}"
    echo "==> Triggering concurrent burst of $BURST_COUNT requests to slow.local (testing queue & semaphore)..."
    for i in $(seq 1 "$BURST_COUNT"); do
      curl -s -o /dev/null -w "%{http_code}\n" -H "Host: slow.local" "$TARGET/" &
    done | sort | uniq -c
    wait
    echo "Burst complete."
    ;;

  sse)
    echo "==> Connecting to live SSE telemetry stream at $TARGET/api/events (press Ctrl+C to exit)..."
    curl -N -H "Accept: text/event-stream" "$TARGET/api/events"
    ;;

  stop)
    if [ -f "$PID_FILE" ]; then
      PID=$(cat "$PID_FILE")
      if kill -0 "$PID" 2>/dev/null; then
        echo "==> Stopping traffic generator (PID: $PID)..."
        kill "$PID" || true
        rm -f "$PID_FILE"
        echo "Stopped."
      else
        echo "Traffic generator process $PID is not running."
        rm -f "$PID_FILE"
      fi
    else
      echo "No active traffic generator PID found."
    fi
    ;;

  status)
    if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
      echo "Traffic generator is RUNNING (PID: $(cat "$PID_FILE"))."
    else
      echo "Traffic generator is NOT running."
      rm -f "$PID_FILE" 2>/dev/null || true
    fi
    ;;

  *)
    usage
    ;;
esac
