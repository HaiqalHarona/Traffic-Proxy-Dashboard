// SanProx - Real-time SSE Telemetry & Health Indicator

(function () {
    function getEnvironment() {
        return window.SANPROX_ENV || "PRODUCTION";
    }

    function isDevelopment() {
        return getEnvironment() === "DEVELOPMENT";
    }

    // HTMX SSE Event Handler for telemetry stream
    document.body.addEventListener('htmx:sseMessage', function (e) {
        if (e.detail.type === 'telemetry') {
            try {
                const data = JSON.parse(e.detail.data);
                const timeLabel = new Date().toLocaleTimeString();

                // Forward data to Chart.js sliding window
                if (typeof window.appendTelemetryData === 'function') {
                    window.appendTelemetryData(data.active_concurrency, data.queued_requests, timeLabel);
                }

                // Dynamic status dot beside SanProx logo
                const dot = document.getElementById('gateway-status-dot');
                if (dot) {
                    const dev = isDevelopment();
                    if (data.system_healthy || dev) {
                        dot.className = "w-2.5 h-2.5 rounded-full bg-emerald-500 ring-4 ring-emerald-500/20 transition-all duration-300";
                        dot.title = dev && data.discovered_services === 0
                            ? "Development gateway active (0 containers)"
                            : `${data.healthy_services}/${data.discovered_services} containers healthy`;
                    } else {
                        dot.className = "w-2.5 h-2.5 rounded-full bg-rose-500 ring-4 ring-rose-500/20 animate-pulse transition-all duration-300";
                        dot.title = data.discovered_services === 0
                            ? "No active backend containers"
                            : `${data.discovered_services - data.healthy_services} containers down`;
                    }
                }
            } catch (err) {
                console.error("Failed to parse telemetry JSON", err);
            }
        }
    });

    // SSE Disconnect / Connection Error Handler
    document.body.addEventListener('htmx:sseError', function () {
        const dot = document.getElementById('gateway-status-dot');
        if (dot && !isDevelopment()) {
            dot.className = "w-2.5 h-2.5 rounded-full bg-rose-500 ring-4 ring-rose-500/20 animate-pulse transition-all duration-300";
            dot.title = "Gateway SSE connection error (offline)";
        }
    });
})();
