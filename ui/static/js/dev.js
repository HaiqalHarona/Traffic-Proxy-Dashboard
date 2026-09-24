// SanProx - Developer Console & Synthetic Load Actions

function getEnvironment() {
    return window.SANPROX_ENV || "PRODUCTION";
}

function isDevelopment() {
    return getEnvironment() === "DEVELOPMENT";
}

function initEnvironmentUI() {
    const env = getEnvironment();
    const isDev = isDevelopment();

    const badge = document.getElementById('env-badge');
    const utilityBar = document.getElementById('top-utility-bar');
    const dot = document.getElementById('gateway-status-dot');
    const devCard = document.getElementById('dev-console-card');
    const devTabBtn = document.getElementById('tab-btn-devtools');

    if (badge) {
        badge.textContent = env;
        if (isDev) {
            badge.className = "text-xs px-2 py-0.5 rounded font-mono font-bold border uppercase tracking-wider bg-emerald-500/20 text-emerald-400 border-emerald-500/40";
        } else {
            badge.className = "text-xs px-2 py-0.5 rounded font-mono font-medium border uppercase tracking-wider bg-zinc-800 text-zinc-400 border-zinc-700";
        }
    }

    if (utilityBar && isDev) {
        utilityBar.classList.remove('border-zinc-800');
        utilityBar.classList.add('border-emerald-500/40', 'border-t-2', 'border-t-emerald-500');
    }

    if (dot && isDev) {
        dot.className = "w-2.5 h-2.5 rounded-full bg-emerald-500 ring-4 ring-emerald-500/20 transition-all duration-300";
        dot.title = "Development gateway active";
    }

    if (devCard) {
        if (isDev) {
            devCard.classList.remove('hidden');
        } else {
            devCard.classList.add('hidden');
        }
    }

    if (devTabBtn) {
        if (isDev) {
            devTabBtn.classList.remove('hidden');
            devTabBtn.classList.add('inline-flex');
        } else {
            devTabBtn.classList.add('hidden');
            devTabBtn.classList.remove('inline-flex');
        }
    }

    // Dynamic Header Target Port & Socket Endpoint
    const targetCode = document.getElementById('header-target-val');
    const socketCode = document.getElementById('header-socket-val');
    const tabSocket = document.getElementById('tab-socket-endpoint');

    const runningPort = (window.SANPROX_PORT && window.SANPROX_PORT.trim() !== "")
        ? window.SANPROX_PORT
        : (window.location.port ? ':' + window.location.port : ':80');
    const runningSocket = (window.SANPROX_DOCKER_SOCKET && window.SANPROX_DOCKER_SOCKET.trim() !== "")
        ? window.SANPROX_DOCKER_SOCKET
        : "/var/run/docker.sock";

    if (targetCode) {
        targetCode.textContent = runningPort;
    }
    if (socketCode) {
        socketCode.textContent = runningSocket;
    }
    if (tabSocket) {
        tabSocket.textContent = runningSocket;
    }
}

async function runTrafficSeeder(count = 50, concurrency = 10) {
    const btn = document.getElementById('btn-seed-traffic');
    const text = document.getElementById('btn-seed-text');
    const statusBox = document.getElementById('dev-status-output');
    const statusText = document.getElementById('dev-status-text');
    const statusTime = document.getElementById('dev-status-time');

    if (btn) btn.disabled = true;
    if (text) text.textContent = 'Seeding...';

    if (statusBox) statusBox.classList.remove('hidden');
    if (statusText) {
        statusText.className = 'font-medium text-amber-400';
        statusText.textContent = `Dispatching ${count} requests across ${concurrency} workers...`;
    }
    if (statusTime) statusTime.textContent = new Date().toLocaleTimeString();

    try {
        const res = await fetch(`/api/dev/seed?count=${count}&concurrency=${concurrency}`, { method: 'POST' });
        const data = await res.json();

        if (res.ok) {
            if (statusText) {
                const hostInfo = (data.sampled_hosts && data.sampled_hosts.length > 0) ? ` [Hosts: ${data.sampled_hosts.join(', ')}]` : '';
                statusText.className = 'font-medium text-emerald-400';
                statusText.textContent = `Seeded ${data.total_sent} requests in ${data.duration_ms}ms (${data.throughput_rps} req/s)${hostInfo} | 200 OK: ${data.status_200} | 502: ${data.status_502} | 503: ${data.status_503}`;
            }
        } else {
            if (statusText) {
                statusText.className = 'font-medium text-rose-400';
                statusText.textContent = `Seeding failed: ${data.error || res.statusText}`;
            }
        }
    } catch (err) {
        if (statusText) {
            statusText.className = 'font-medium text-rose-400';
            statusText.textContent = `Error: ${err.message}`;
        }
    } finally {
        if (btn) btn.disabled = false;
        if (text) text.textContent = 'Run Seeder';
    }
}

async function triggerCustomSeed() {
    const count = document.getElementById('seeder-count-input')?.value || 50;
    const conc = document.getElementById('seeder-conc-input')?.value || 10;
    const logBox = document.getElementById('custom-seed-log');
    const logText = document.getElementById('custom-seed-text');
    const btn = document.getElementById('btn-custom-seed');

    if (btn) btn.disabled = true;
    if (logBox) logBox.classList.remove('hidden');
    if (logText) logText.textContent = `Dispatching ${count} requests (${conc} workers)...`;

    try {
        const res = await fetch(`/api/dev/seed?count=${count}&concurrency=${conc}`, { method: 'POST' });
        const data = await res.json();
        if (res.ok) {
            if (logText) {
                const hostInfo = (data.sampled_hosts && data.sampled_hosts.length > 0) ? ` Target hosts: <code class="text-zinc-300 font-mono">${data.sampled_hosts.join(', ')}</code>.` : '';
                logText.innerHTML = `<span class="text-emerald-400 font-semibold">Done:</span> Sent ${data.total_sent} requests in ${data.duration_ms}ms (${data.throughput_rps} req/s).${hostInfo} 200 OK: ${data.status_200}, 502 Bad Gateway: ${data.status_502}, 503 Overloaded: ${data.status_503}.`;
            }
        } else {
            if (logText) logText.innerHTML = `<span class="text-rose-400 font-semibold">Error:</span> ${data.error || res.statusText}`;
        }
    } catch (err) {
        if (logText) logText.innerHTML = `<span class="text-rose-400 font-semibold">Network error:</span> ${err.message}`;
    } finally {
        if (btn) btn.disabled = false;
    }
}

async function runStressQueue(count = 100, concurrency = 30) {
    const btn = document.getElementById('btn-stress-queue');
    const text = document.getElementById('btn-stress-text');
    const statusBox = document.getElementById('dev-status-output');
    const statusText = document.getElementById('dev-status-text');
    const statusTime = document.getElementById('dev-status-time');

    if (btn) btn.disabled = true;
    if (text) text.textContent = 'Saturating...';

    if (statusBox) statusBox.classList.remove('hidden');
    if (statusText) {
        statusText.className = 'font-medium text-amber-400';
        statusText.textContent = `Submitting ${count} concurrent requests to saturate queue...`;
    }
    if (statusTime) statusTime.textContent = new Date().toLocaleTimeString();

    try {
        const res = await fetch(`/api/dev/stress?count=${count}&concurrency=${concurrency}&hold_ms=120`, { method: 'POST' });
        const data = await res.json();

        if (res.ok) {
            if (statusText) {
                statusText.className = 'font-medium text-emerald-400';
                statusText.textContent = `Stress burst complete: ${data.total_sent} requests in ${data.duration_ms}ms | 503 Overloaded: ${data.status_503} | 502: ${data.status_502}`;
            }
        } else {
            if (statusText) {
                statusText.className = 'font-medium text-rose-400';
                statusText.textContent = `Stress test failed: ${data.error || res.statusText}`;
            }
        }
    } catch (err) {
        if (statusText) {
            statusText.className = 'font-medium text-rose-400';
            statusText.textContent = `Error: ${err.message}`;
        }
    } finally {
        if (btn) btn.disabled = false;
        if (text) text.textContent = 'Saturate Queue';
    }
}

async function resetMetrics() {
    const statusBox = document.getElementById('dev-status-output');
    const statusText = document.getElementById('dev-status-text');
    const statusTime = document.getElementById('dev-status-time');

    try {
        const res = await fetch('/api/dev/reset-metrics', { method: 'POST' });
        const data = await res.json();
        if (statusBox) statusBox.classList.remove('hidden');
        if (statusText) {
            statusText.className = 'font-medium text-emerald-400';
            statusText.textContent = data.message || 'Telemetry metrics reset to 0';
        }
        if (statusTime) statusTime.textContent = new Date().toLocaleTimeString();

        const mTotal = document.getElementById('metric-total-requests');
        const mConc = document.getElementById('metric-active-concurrency');
        const mQueued = document.getElementById('metric-queued-requests');
        if (mTotal) mTotal.textContent = '0';
        if (mConc) mConc.textContent = '0';
        if (mQueued) mQueued.textContent = '0';
    } catch (err) {
        alert(`Failed to reset metrics: ${err.message}`);
    }
}

let debugStateVisible = false;
async function toggleDebugState() {
    const box = document.getElementById('debug-state-box');
    const jsonPre = document.getElementById('debug-state-json');

    if (debugStateVisible) {
        if (box) box.classList.add('hidden');
        debugStateVisible = false;
        return;
    }

    try {
        const res = await fetch('/api/dev/debug-state');
        const data = await res.json();
        if (jsonPre) jsonPre.textContent = JSON.stringify(data, null, 2);
        if (box) box.classList.remove('hidden');
        debugStateVisible = true;
    } catch (err) {
        alert(`Failed to fetch debug state: ${err.message}`);
    }
}

// Auto-initialize environment styling
if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initEnvironmentUI);
} else {
    initEnvironmentUI();
}

// Expose globally for HTML event handlers
window.initEnvironmentUI = initEnvironmentUI;
window.runTrafficSeeder = runTrafficSeeder;
window.triggerCustomSeed = triggerCustomSeed;
window.runStressQueue = runStressQueue;
window.resetMetrics = resetMetrics;
window.toggleDebugState = toggleDebugState;
