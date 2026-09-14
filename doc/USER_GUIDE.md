# User Guide

Operational guide for configuring, deploying, and monitoring TrafficProxy Edge Gateway.

---

## Table of Contents

- [Quickstart](#quickstart)
- [Docker Compose Deployment](#docker-compose-deployment)
- [Container Label Configuration](#container-label-configuration)
  - [Supported Labels](#supported-labels)
  - [Example Backend Services](#example-backend-services)
- [Web Dashboard & Telemetry](#web-dashboard--telemetry)
  - [Real-Time Metrics](#real-time-metrics)
  - [Telemetry Chart](#telemetry-chart)
- [Error Responses](#error-responses)
- [Network Isolation & Security](#network-isolation--security)

---

## Quickstart

Start the gateway using Docker Compose:

```bash
docker compose up -d --build
```

Access the dashboard by navigating to `http://localhost` in your browser.

To stop the gateway:

```bash
docker compose down
```

---

## Docker Compose Deployment

The provided `docker-compose.yml` mounts the Docker daemon socket in read-only mode and exposes standard HTTP ports:

```yaml
version: '3.8'

services:
  traffic-proxy:
    build:
      context: .
      dockerfile: Dockerfile
    container_name: traffic-proxy
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    environment:
      - DOCKER_HOST=unix:///var/run/docker.sock
    security_opt:
      - no-new-privileges:true
    read_only: true
```

---

## Container Label Configuration

TrafficProxy monitors `/var/run/docker.sock` every 5 seconds to register and update routes dynamically.

### Supported Labels

| Label | Required | Description | Default |
| :--- | :--- | :--- | :--- |
| `traffic-proxy.enable` | **Yes** | Must be set to `"true"` for TrafficProxy to route traffic to the container. | `false` |
| `traffic-proxy.rule` | **Yes** | Target virtual host header to match (e.g., `app.local`, `api.homelab.internal`). | None |
| `traffic-proxy.port` | No | Target internal container port. If omitted, uses first exposed container port, falling back to `80`. | First exposed or `80` |

### Example Backend Services

Add labels to any service in your Docker Compose or Docker run command:

```yaml
services:
  whoami:
    image: traefik/whoami
    container_name: whoami-service
    labels:
      - "traffic-proxy.enable=true"
      - "traffic-proxy.rule=whoami.local"
      - "traffic-proxy.port=80"

  grafana:
    image: grafana/grafana-oss:latest
    container_name: grafana-service
    labels:
      - "traffic-proxy.enable=true"
      - "traffic-proxy.rule=metrics.local"
      - "traffic-proxy.port=3000"
```

To route traffic to these backends, send requests with the matching `Host` header:

```bash
curl -H "Host: whoami.local" http://localhost/
curl -H "Host: metrics.local" http://localhost/
```

---

## Web Dashboard & Telemetry

The embedded dashboard at `http://localhost/` provides live visibility into gateway operations via Server-Sent Events (`/api/events`).

### Real-Time Metrics

- **Total Requests**: Cumulative HTTP requests processed since process inception.
- **Active Concurrency**: Number of requests actively being processed by downstream backends.
- **Discovered Services**: Number of running containers discovered with valid `traffic-proxy` labels.
- **Queued Requests**: Number of incoming requests waiting for a semaphore slot.

### Telemetry Chart

The telemetry chart graphs active concurrency trends over time, dynamically updating every second through Chart.js and SSE.

---

## Error Responses

| Status Code | Reason | Cause / Remediation |
| :--- | :--- | :--- |
| `502 Bad Gateway` | Gateway Error: No matching backend route | The incoming request's `Host` header does not match any registered container rule. Verify container labels and health. |
| `503 Service Unavailable` | Service Overloaded: Request queue timeout | Concurrency limit reached (5,000 active requests) and queue wait time exceeded 3 seconds. Scale downstream backends or adjust concurrency limits. |

---

## Network Isolation & Security

- **Socket Mount**: Mount `/var/run/docker.sock:ro` strictly in read-only mode.
- **Rootless Binary**: The production container executes under UID `65534` (`nobody`).
- **Read-Only Filesystem**: Container rootfs is configured as read-only (`read_only: true`).
