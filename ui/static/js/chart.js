// SanProx - Real-time Chart.js Telemetry Graph

let chartRetryCount = 0;

function initTrafficChart() {
    const canvas = document.getElementById('trafficChart');
    if (!canvas) return null;

    if (typeof Chart === 'undefined') {
        if (chartRetryCount < 15) {
            chartRetryCount++;
            setTimeout(initTrafficChart, 200);
        } else {
            console.warn("Chart.js library is not available from CDN. Telemetry graph will be deferred.");
        }
        return null;
    }

    if (window.trafficChart) {
        return window.trafficChart;
    }

    const ctx = canvas.getContext('2d');
    const chart = new Chart(ctx, {
        type: 'line',
        data: {
            labels: ['12:00', '12:05', '12:10', '12:15', '12:20', '12:25', '12:30', '12:35', '12:40', '12:45', '12:50', '12:55', '13:00', '13:05', '13:10', '13:15', '13:20', '13:25', '13:30', '13:35'],
            datasets: [
                {
                    label: 'Active Concurrency',
                    data: [14, 18, 15, 22, 35, 42, 38, 28, 20, 16, 24, 29, 27, 21, 17, 14, 19, 23, 18, 14],
                    borderColor: '#10b981',
                    backgroundColor: 'rgba(16, 185, 129, 0.08)',
                    fill: true,
                    borderWidth: 1.5,
                    pointRadius: 2,
                    tension: 0.2
                },
                {
                    label: 'Queued Requests',
                    data: [0, 0, 0, 1, 3, 5, 2, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 1, 0, 0],
                    borderColor: '#f59e0b',
                    backgroundColor: 'rgba(245, 158, 11, 0.08)',
                    fill: true,
                    borderWidth: 1.5,
                    pointRadius: 2,
                    tension: 0.2
                }
            ]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            animation: false,
            interaction: { intersect: false, mode: 'index' },
            scales: {
                x: {
                    grid: { color: '#27272a' },
                    ticks: { color: '#71717a', font: { family: 'ui-monospace, monospace', size: 10 } }
                },
                y: {
                    beginAtZero: true,
                    suggestedMin: 0,
                    suggestedMax: 10,
                    grid: { color: '#27272a' },
                    ticks: { color: '#71717a', font: { family: 'ui-monospace, monospace', size: 10 }, stepSize: 2 }
                }
            },
            plugins: {
                legend: { display: false },
                tooltip: {
                    backgroundColor: '#18181b',
                    titleColor: '#f4f4f5',
                    bodyColor: '#e4e4e7',
                    borderColor: '#3f3f46',
                    borderWidth: 1,
                    titleFont: { family: 'ui-monospace, monospace', size: 11 },
                    bodyFont: { family: 'ui-monospace, monospace', size: 11 }
                }
            }
        }
    });

    window.trafficChart = chart;
    return chart;
}

function appendTelemetryData(activeConc, queuedReqs, timeLabel) {
    if (!window.trafficChart) {
        initTrafficChart();
    }
    const chart = window.trafficChart;
    if (!chart) return;

    if (!timeLabel) {
        timeLabel = new Date().toLocaleTimeString();
    }

    if (chart.data.labels.length > 25) {
        chart.data.labels.shift();
        chart.data.datasets[0].data.shift();
        chart.data.datasets[1].data.shift();
    }

    chart.data.labels.push(timeLabel);
    chart.data.datasets[0].data.push(activeConc || 0);
    chart.data.datasets[1].data.push(queuedReqs || 0);
    chart.update('none');
}

// Robust auto-initialization (runs immediately if DOM is ready, otherwise on DOMContentLoaded)
function ensureChartInitialized() {
    if (!window.trafficChart) {
        initTrafficChart();
    }
}

if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', ensureChartInitialized);
} else {
    ensureChartInitialized();
}

// Expose globally
window.initTrafficChart = initTrafficChart;
window.appendTelemetryData = appendTelemetryData;
