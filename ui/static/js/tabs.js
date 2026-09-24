// SanProx - Tab Navigation & Route UI Utilities

function switchTab(tabName) {
    document.querySelectorAll('.tab-content').forEach(el => el.classList.remove('active'));
    document.querySelectorAll('.tab-btn').forEach(el => {
        el.classList.remove('border-emerald-500', 'text-white');
        el.classList.add('border-transparent');
        if (el.id === 'tab-btn-devtools') {
            el.classList.remove('text-amber-300');
            el.classList.add('text-amber-400');
        } else {
            el.classList.remove('text-white');
            el.classList.add('text-zinc-400');
        }
    });

    const targetContent = document.getElementById('tab-' + tabName);
    const targetBtn = document.getElementById('tab-btn-' + tabName);

    if (targetContent) targetContent.classList.add('active');
    if (targetBtn) {
        targetBtn.classList.remove('border-transparent', 'text-zinc-400');
        targetBtn.classList.add('border-emerald-500', 'text-white');
        if (tabName === 'devtools') {
            targetBtn.classList.add('text-amber-300');
        }
    }

    if (tabName === 'overview') {
        if (!window.trafficChart && typeof initTrafficChart === 'function') {
            initTrafficChart();
        }
        if (window.trafficChart) {
            window.trafficChart.resize();
        }
    }
}

function copyCurl(host) {
    const cmd = `curl -H "Host: ${host}" http://localhost/`;
    navigator.clipboard.writeText(cmd);
    alert(`Copied to clipboard: ${cmd}`);
}

function copyCompose() {
    const yaml = `services:
  my-app:
    image: hashicorp/http-echo:0.2.3
    command: ["-text=Hello World", "-listen=:5678"]
    labels:
      - "traffic-proxy.enable=true"
      - "traffic-proxy.rule=hello.local"
      - "traffic-proxy.port=5678"`;
    navigator.clipboard.writeText(yaml);
    alert("Docker Compose snippet copied to clipboard");
}

function refreshMockRoutes() {
    const log = document.getElementById('log-console');
    if (log) {
        const now = new Date().toLocaleTimeString();
        const entry = document.createElement('div');
        entry.innerHTML = `<span class="text-sky-500/80">[SCAN]</span> <span class="text-zinc-500">${now}</span> Manual Docker socket scan triggered. Route table synchronized.`;
        log.appendChild(entry);
        log.scrollTop = log.scrollHeight;
    }
}

// Live Route Filter Search
function initRouteSearch() {
    const searchInput = document.getElementById('route-search');
    const tableBody = document.getElementById('routes-table-body');
    if (searchInput && tableBody) {
        searchInput.addEventListener('input', function (e) {
            const query = e.target.value.toLowerCase().trim();
            const rows = tableBody.querySelectorAll('tr');
            rows.forEach(row => {
                const text = row.textContent.toLowerCase();
                row.style.display = text.includes(query) ? '' : 'none';
            });
        });
    }
}

if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initRouteSearch);
} else {
    initRouteSearch();
}

// Expose globally for inline onclick handlers
window.switchTab = switchTab;
window.copyCurl = copyCurl;
window.copyCompose = copyCompose;
window.refreshMockRoutes = refreshMockRoutes;
