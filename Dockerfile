# Build stage
FROM golang:1.22-alpine AS builder

# Install build dependencies & ca-certificates
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Copy dependency specifications first for caching
COPY go.mod go.sum* ./
RUN go mod download

# Copy source code
COPY . .

# Build static binary targeting scratch
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -extldflags '-static'" \
    -o /app/traffic-proxy ./cmd/proxy

# Minimal final scratch container
FROM scratch

# Copy root CA certificates & timezone data for TLS termination / outbound requests
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy compiled binary
COPY --from=builder /app/traffic-proxy /traffic-proxy

# Use non-root user (nobody / 65534)
USER 65534:65534

EXPOSE 80 443

ENTRYPOINT ["/traffic-proxy"]
