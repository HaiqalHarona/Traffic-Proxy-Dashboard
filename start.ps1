<#
.SYNOPSIS
    Starts TrafficProxy Edge Gateway for local development.
.DESCRIPTION
    Builds the proxy binary from ./cmd/proxy and runs it with local development defaults.
    Optionally spins up mock backends (backend-api and backend-slow) in Docker if requested.
.PARAMETER Port
    Port for the proxy to listen on (default: :80 or $env:PROXY_PORT)
.PARAMETER MaxConcurrent
    Max concurrent requests (default: 25)
.PARAMETER QueueTimeout
    Queue timeout before 503 response (default: 500ms)
.PARAMETER PollInterval
    Docker container poll interval (default: 2s)
.PARAMETER LogLevel
    Log level: DEBUG, INFO, WARN, ERROR (default: DEBUG)
.PARAMETER WithMockBackends
    If set, launches mock backend containers (backend-api-local and backend-slow-local) via Docker.
.PARAMETER StopBackends
    If set, stops running mock backend containers and exits.
.PARAMETER Clean
    If set, removes compiled .exe binaries from workspace and exits.
.PARAMETER KeepBinary
    If set, preserves traffic-proxy.exe after process exit (default: automatically deletes upon exit).
#>
[CmdletBinding()]
param (
    [string]$Environment = $(if ($env:ENVIRONMENT) { $env:ENVIRONMENT } else { "DEVELOPMENT" }),
    [string]$Port = $(if ($env:PROXY_PORT) { $env:PROXY_PORT } else { ":80" }),
    [int]$MaxConcurrent = 25,
    [string]$QueueTimeout = "500ms",
    [string]$PollInterval = "2s",
    [string]$LogLevel = "DEBUG",
    [switch]$WithMockBackends,
    [switch]$StopBackends,
    [switch]$Clean,
    [switch]$KeepBinary
)

$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $scriptDir

# Auto-discover docker.exe in standard Windows installation paths if not already in PATH
if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    $knownDockerDirs = @(
        "$env:LOCALAPPDATA\Programs\DockerDesktop\resources\bin",
        "$env:ProgramFiles\Docker\Docker\resources\bin",
        "${env:ProgramFiles(x86)}\Docker\Docker\resources\bin"
    )
    foreach ($dir in $knownDockerDirs) {
        if ($dir -and (Test-Path (Join-Path $dir "docker.exe"))) {
            $env:PATH = "$dir;$env:PATH"
            break
        }
    }
}

# Load .env file if present
$envFile = Join-Path $scriptDir ".env"
if (Test-Path $envFile) {
    Get-Content $envFile | ForEach-Object {
        $line = $_.Trim()
        if ($line -and -not $line.StartsWith("#")) {
            $parts = $line -split "=", 2
            if ($parts.Length -eq 2) {
                $k = $parts[0].Trim()
                $v = $parts[1].Trim().Trim('"').Trim("'")
                if (-not [Environment]::GetEnvironmentVariable($k, "Process")) {
                    [Environment]::SetEnvironmentVariable($k, $v, "Process")
                }
            }
        }
    }
}

# Windows Docker Desktop runs over named pipe npipe:////./pipe/docker_engine.
# If DOCKER_HOST was populated with a Unix socket (/var/run/docker.sock), normalize or unset it for Windows.
if ($env:DOCKER_HOST -and ($env:DOCKER_HOST -like "*/var/run/docker.sock*" -or $env:DOCKER_HOST -like "unix://*")) {
    $env:DOCKER_HOST = "npipe:////./pipe/docker_engine"
}

# Override parameter defaults from environment if not explicitly passed as CLI arguments
if (-not $PSBoundParameters.ContainsKey('Port') -and $env:PROXY_PORT) {
    $Port = $env:PROXY_PORT
}
if (-not $PSBoundParameters.ContainsKey('MaxConcurrent') -and $env:PROXY_MAX_CONCURRENT) {
    $MaxConcurrent = [int]$env:PROXY_MAX_CONCURRENT
}
if (-not $PSBoundParameters.ContainsKey('QueueTimeout') -and $env:PROXY_QUEUE_TIMEOUT) {
    $QueueTimeout = $env:PROXY_QUEUE_TIMEOUT
}
if (-not $PSBoundParameters.ContainsKey('PollInterval') -and $env:DOCKER_POLL_INTERVAL) {
    $PollInterval = $env:DOCKER_POLL_INTERVAL
}
if (-not $PSBoundParameters.ContainsKey('LogLevel') -and $env:LOG_LEVEL) {
    $LogLevel = $env:LOG_LEVEL
}
if (-not $PSBoundParameters.ContainsKey('WithMockBackends') -and $env:WITH_MOCK_BACKENDS) {
    $WithMockBackends = [bool]($env:WITH_MOCK_BACKENDS -match '^(?i:true|1|yes)$')
}

function Test-DockerAvailable {
    $dockerCmd = Get-Command docker -ErrorAction SilentlyContinue
    if (-not $dockerCmd) {
        return $false
    }
    $prevEAP = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'SilentlyContinue'
        & $dockerCmd.Source version 2>$null | Out-Null
        return ($LASTEXITCODE -eq 0)
    } catch {
        return $false
    } finally {
        $ErrorActionPreference = $prevEAP
    }
}

$mockContainerNames = @(
    "web-frontend-1",
    "web-frontend-2",
    "api-service",
    "auth-service",
    "store-service",
    "backend-slow-local",
    "backend-api-local"
)

function Stop-MockContainers {
    if (Test-DockerAvailable) {
        Write-Host "==> Stopping mock backends..." -ForegroundColor Yellow
        $prevEAP = $ErrorActionPreference
        try {
            $ErrorActionPreference = 'SilentlyContinue'
            & docker stop $mockContainerNames 2>$null | Out-Null
            & docker rm -f $mockContainerNames 2>$null | Out-Null
            Write-Host "Mock backends stopped." -ForegroundColor Green
        } catch {
            # Suppress cleanup errors
        } finally {
            $ErrorActionPreference = $prevEAP
        }
    } else {
        Write-Host "Docker is not running; skipping container cleanup." -ForegroundColor Gray
    }
}

if ($StopBackends) {
    Stop-MockContainers
    exit 0
}

function Remove-CompiledBinaries {
    Write-Host "==> Cleaning up compiled binaries..." -ForegroundColor Gray
    Get-ChildItem -Path $scriptDir -Filter "*.exe" -File | ForEach-Object {
        try {
            Remove-Item -Path $_.FullName -Force -ErrorAction SilentlyContinue
        } catch {
            # Suppress errors if file is temporarily held
        }
    }
}

if ($Clean) {
    Remove-CompiledBinaries
    exit 0
}

if ($WithMockBackends) {
    if (-not (Test-DockerAvailable)) {
        Write-Warning "Docker daemon is not reachable. Cannot start mock backends without Docker."
    } else {
        Write-Host "==> Starting mock backend containers (simulating multi-tier website cluster)..." -ForegroundColor Cyan
        $prevEAP = $ErrorActionPreference
        try {
            $ErrorActionPreference = 'SilentlyContinue'
            $netExists = (& docker network ls -q --filter "name=^local-mesh$")
            if (-not $netExists) {
                & docker network create local-mesh 2>$null | Out-Null
            }

            # 1. Web Frontend Node 1 (web.local - round-robin pool)
            & docker rm -f web-frontend-1 2>$null | Out-Null
            & docker run -d --name web-frontend-1 `
                --network local-mesh `
                --label "traffic-proxy.enable=true" `
                --label "traffic-proxy.rule=web.local" `
                --label "traffic-proxy.balance=round-robin" `
                --label "traffic-proxy.port=5678" `
                hashicorp/http-echo:0.2.3 -text="OK: Web Frontend Node 1 (web.local)`n" -listen=:5678 2>$null | Out-Null

            # 2. Web Frontend Node 2 (web.local - round-robin pool)
            & docker rm -f web-frontend-2 2>$null | Out-Null
            & docker run -d --name web-frontend-2 `
                --network local-mesh `
                --label "traffic-proxy.enable=true" `
                --label "traffic-proxy.rule=web.local" `
                --label "traffic-proxy.balance=round-robin" `
                --label "traffic-proxy.port=5678" `
                hashicorp/http-echo:0.2.3 -text="OK: Web Frontend Node 2 (web.local)`n" -listen=:5678 2>$null | Out-Null

            # 3. Core API Service (app.local/api - path routing & strip-prefix)
            & docker rm -f api-service 2>$null | Out-Null
            & docker run -d --name api-service `
                --network local-mesh `
                --label "traffic-proxy.enable=true" `
                --label "traffic-proxy.rule=app.local" `
                --label "traffic-proxy.path=/api" `
                --label "traffic-proxy.strip-prefix=true" `
                --label "traffic-proxy.priority=10" `
                --label "traffic-proxy.port=5678" `
                hashicorp/http-echo:0.2.3 -text="OK: Core API Service (app.local/api)`n" -listen=:5678 2>$null | Out-Null

            # 4. Auth & Identity Service (app.local/auth - path routing)
            & docker rm -f auth-service 2>$null | Out-Null
            & docker run -d --name auth-service `
                --network local-mesh `
                --label "traffic-proxy.enable=true" `
                --label "traffic-proxy.rule=app.local" `
                --label "traffic-proxy.path=/auth" `
                --label "traffic-proxy.priority=5" `
                --label "traffic-proxy.port=5678" `
                hashicorp/http-echo:0.2.3 -text="OK: Auth & Identity Service (app.local/auth)`n" -listen=:5678 2>$null | Out-Null

            # 5. Store / E-Commerce Catalog Service (store.local)
            & docker rm -f store-service 2>$null | Out-Null
            & docker run -d --name store-service `
                --network local-mesh `
                --label "traffic-proxy.enable=true" `
                --label "traffic-proxy.rule=store.local" `
                --label "traffic-proxy.port=5678" `
                hashicorp/http-echo:0.2.3 -text="OK: Store & Catalog Service (store.local)`n" -listen=:5678 2>$null | Out-Null

            # 6. Analytics / Heavy Delayed Service (slow.local - 200ms delay)
            $pythonScript = @'
from http.server import HTTPServer, BaseHTTPRequestHandler
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
HTTPServer(("", 8080), DelayedHandler).serve_forever()
'@

            & docker rm -f backend-slow-local 2>$null | Out-Null
            & docker run -d --name backend-slow-local `
                --network local-mesh `
                --label "traffic-proxy.enable=true" `
                --label "traffic-proxy.rule=slow.local" `
                --label "traffic-proxy.port=8080" `
                python:3.12-alpine python3 -c $pythonScript 2>$null | Out-Null

            Write-Host "Mock backends started (6 containers):" -ForegroundColor Green
            Write-Host "  - web-frontend-1 & web-frontend-2 (http://web.local/ [round-robin])" -ForegroundColor Gray
            Write-Host "  - api-service (http://app.local/api [prefix stripped])" -ForegroundColor Gray
            Write-Host "  - auth-service (http://app.local/auth)" -ForegroundColor Gray
            Write-Host "  - store-service (http://store.local/)" -ForegroundColor Gray
            Write-Host "  - backend-slow-local (http://slow.local/ [200ms latency])" -ForegroundColor Gray
        } catch {
            Write-Warning "Failed to start mock containers: $_"
        } finally {
            $ErrorActionPreference = $prevEAP
        }
    }
}

# Remove any leftover binary before compiling
Remove-Item -Path (Join-Path $scriptDir "traffic-proxy.exe") -Force -ErrorAction SilentlyContinue

Write-Host "==> Building traffic-proxy binary..." -ForegroundColor Cyan
go build -o traffic-proxy.exe ./cmd/proxy
if ($LASTEXITCODE -ne 0) {
    Write-Error "Failed to build traffic-proxy"
    exit $LASTEXITCODE
}

if (-not $Port.StartsWith(":")) {
    $Port = ":$Port"
}

$env:ENVIRONMENT = $Environment
$env:PROXY_PORT = $Port
$env:PROXY_MAX_CONCURRENT = $MaxConcurrent.ToString()
$env:PROXY_QUEUE_TIMEOUT = $QueueTimeout
$env:DOCKER_POLL_INTERVAL = $PollInterval
$env:LOG_LEVEL = $LogLevel

Write-Host "==> Starting SanProx on port $Port (Environment: $Environment, LogLevel: $LogLevel, MaxConcurrent: $MaxConcurrent, QueueTimeout: $QueueTimeout, MockBackends: $WithMockBackends)..." -ForegroundColor Green
Write-Host "Press Ctrl+C to stop." -ForegroundColor Gray

try {
    .\traffic-proxy.exe
} finally {
    if (-not $KeepBinary) {
        Write-Host "==> Cleaning up compiled binary..." -ForegroundColor Gray
        Start-Sleep -Milliseconds 150
        Remove-Item -Path (Join-Path $scriptDir "traffic-proxy.exe") -Force -ErrorAction SilentlyContinue
    }
    if ($WithMockBackends) {
        Stop-MockContainers
    }
}
