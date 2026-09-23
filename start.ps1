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
    try {
        $null = & docker info 2>&1
        return ($LASTEXITCODE -eq 0)
    } catch {
        return $false
    }
}

function Stop-MockContainers {
    if (Test-DockerAvailable) {
        Write-Host "==> Stopping mock backends..." -ForegroundColor Yellow
        try {
            & docker stop backend-api-local backend-slow-local 2>&1 | Out-Null
            & docker rm -f backend-api-local backend-slow-local 2>&1 | Out-Null
            Write-Host "Mock backends stopped." -ForegroundColor Green
        } catch {
            # Suppress cleanup errors
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
        Write-Host "==> Starting mock backend containers..." -ForegroundColor Cyan
        try {
            & docker network create local-mesh 2>&1 | Out-Null

            & docker rm -f backend-api-local 2>&1 | Out-Null
            & docker run -d --name backend-api-local `
                --network local-mesh `
                --label "traffic-proxy.enable=true" `
                --label "traffic-proxy.rule=app.local" `
                --label "traffic-proxy.port=5678" `
                hashicorp/http-echo:0.2.3 -text="OK: Response from backend-api (app.local)`n" -listen=:5678 | Out-Null

            $pythonScript = @'
from http.server import HTTPServer, BaseHTTPRequestHandler
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
HTTPServer(("", 8080), DelayedHandler).serve_forever()
'@

            & docker rm -f backend-slow-local 2>&1 | Out-Null
            & docker run -d --name backend-slow-local `
                --network local-mesh `
                --label "traffic-proxy.enable=true" `
                --label "traffic-proxy.rule=slow.local" `
                --label "traffic-proxy.port=8080" `
                python:3.12-alpine python3 -c $pythonScript | Out-Null

            Write-Host "Mock backends started: backend-api-local (app.local), backend-slow-local (slow.local)" -ForegroundColor Green
        } catch {
            Write-Warning "Failed to start mock containers: $_"
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
