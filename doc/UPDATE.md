# Update & Maintenance Guide

Procedures for upgrading, maintaining, and applying updates to TrafficProxy Edge Gateway.

---

## Updating via Docker Compose

To pull the latest release or rebuild with updated local code:

```bash
# 1. Pull the latest base images and build with latest code
docker compose build --no-cache

# 2. Recreate containers with minimal interruption
docker compose up -d --force-recreate
```

---

## Updating Local Builds

When running locally without Docker or iterating on local Go code:

1. Pull latest git changes:
```bash
git pull origin develop
```

2. Recompile and run via start scripts:

**Linux / macOS (Bash):**
```bash
./start.sh
```

**Windows (PowerShell):**
```powershell
.\start.ps1
```

---

## Updating Prebuilt Images from GHCR

When running images published by the CI/CD pipeline:

```bash
# Pull newest image tag
docker pull ghcr.io/<owner>/traffic-proxy:latest

# Restart service with new image
docker compose up -d traffic-proxy
```

---

## Go Dependency & Security Updates

To upgrade Go module dependencies:

```bash
# 1. Update direct dependencies
go get -u ./...

# 2. Prune unused dependencies and sync go.sum
go mod tidy

# 3. Verify tests and race conditions
go test -race ./...

# 4. Check static analysis
go vet ./...
```

---

## Versioning & Release Workflow

1. **Tagging**: Create and push a semantic version tag:
   ```bash
   git tag -a v1.1.0 -m "Release v1.1.0"
   git push origin v1.1.0
   ```
2. **Automated CI Build**: Pushing commits to `main` or version tags triggers `.github/workflows/ci.yml`, automatically compiling and publishing the container image to GitHub Container Registry.
