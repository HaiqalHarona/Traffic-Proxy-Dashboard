# CI/CD Pipeline Documentation

Specification and execution details for the continuous integration and release pipeline configured in `.github/workflows/ci.yml`.

---

## Overview

The CI pipeline (`Fuckass Pipeline` defined in `.github/workflows/ci.yml`) runs automated checks on all branches and pull requests targeting primary development branches. It enforces linting via `.golangci.yml`, race-detector unit testing, code coverage thresholds (≥ 60%), static compilation, and automated container image publishing to GitHub Container Registry (`ghcr.io`).

---

## Triggers and Concurrency

- **Push Events**: Triggered on branches matching `main`, `develop`, `feature/**`, `fix/**`, `hotfix/**`.
- **Pull Request Events**: Triggered on pull requests targeting `main` and `develop`.
- **Concurrency Group**: Defined by `${{ github.workflow }}-${{ github.ref }}` with `cancel-in-progress: true` to prevent redundant builds on rapid pushes.

---

## Pipeline Stages

```
   ┌────────────────┐      ┌────────────────┐
   │  Job 1: Lint   │      │  Job 2: Test   │
   │  - go vet      │      │  - go test -race│
   │  - golangci    │      │  - coverage ≥60│
   └───────┬────────┘      └───────┬────────┘
           │                       │
           └───────────┬───────────┘
                       ▼
             ┌──────────────────┐
             │  Job 3: Build    │
             │  - Static binary │
             │  - Buildx cache  │
             └─────────┬────────┘
                       ▼
             ┌──────────────────┐
             │  Job 4: Release  │ (main branch push only)
             │  - Login GHCR    │
             │  - Tag & Push    │
             └──────────────────┘
```

### Job 1: Lint
- **Environment**: `ubuntu-latest`
- **Go Version**: `1.22` with automated module caching (`cache: true`).
- **Steps**:
  1. `go vet ./...`: Standard Go code vetting for suspicious constructs.
  2. `golangci-lint`: Executes via `golangci-lint-action@v6` with `--timeout 5m`, referencing root configuration [`.golangci.yml`](file:///home/ninonakano/Desktop/Traffic-Proxy-Dashboard/.golangci.yml) (enabling `errcheck`, `gosimple`, `govet` with `enable-all: true`, `ineffassign`, `staticcheck`, `unused`, `gofmt`, and `misspell`).

### Job 2: Test
- **Environment**: `ubuntu-latest`
- **Steps**:
  1. Executes tests with the Go race detector enabled:
     ```bash
     go test -race -coverprofile=coverage.out -covermode=atomic ./...
     ```
  2. Parses code coverage and verifies against the minimum required threshold of **60%**:
     ```bash
     COVERAGE=$(go tool cover -func=coverage.out | grep total | awk '{print $3}' | tr -d '%')
     echo "Total coverage: ${COVERAGE}%"
     if (( $(echo "$COVERAGE < 60" | bc -l) )); then
       echo "FAIL: Coverage ${COVERAGE}% is below the required 60% threshold"
       exit 1
     fi
     echo "PASS: Coverage ${COVERAGE}% meets the required threshold"
     ```
     Fails the workflow if coverage is below 60%.
  3. Uploads the generated `coverage.out` artifact for audit retention (7 days).

### Job 3: Build
- **Dependencies**: Requires successful completion of both `lint` and `test`.
- **Steps**:
  1. Compiles a production-ready, CGO-free static binary:
     ```bash
     CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
       go build -ldflags="-s -w -extldflags '-static'" \
       -o /tmp/traffic-proxy ./cmd/proxy
     ```
  2. Sets up Docker Buildx (`docker/setup-buildx-action@v3`).
  3. Executes a test build of the Docker container using GitHub Actions cache (`type=gha`) without pushing.

### Job 4: Release
- **Dependencies**: Requires `build`.
- **Condition**: Only runs on `push` to `refs/heads/main`.
- **Permissions**: `contents: read`, `packages: write`.
- **Steps**:
  1. Authenticates against GitHub Container Registry (`ghcr.io`) using `github.actor` and `secrets.GITHUB_TOKEN`.
  2. Computes Docker metadata tags via `docker/metadata-action@v5`:
     - Git commit SHA (`sha-xxxxxxx`)
     - Git branch name
     - Semantic version tags
     - `latest` tag on the default branch.
  3. Builds and pushes the multi-stage scratch image to `ghcr.io/<owner>/traffic-proxy`.

---

## Local Pipeline Replication

Run the equivalent CI steps locally prior to opening pull requests:

```bash
# 1. Run vet, linting, and tests with race detection
go vet ./...
golangci-lint run
go test -race -coverprofile=coverage.out -covermode=atomic ./...

# 2. Check coverage percentage
go tool cover -func=coverage.out

# 3. Compile static binary
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -extldflags '-static'" -o /tmp/traffic-proxy ./cmd/proxy

# 4. Build Docker container
docker build -t traffic-proxy:test .
```
