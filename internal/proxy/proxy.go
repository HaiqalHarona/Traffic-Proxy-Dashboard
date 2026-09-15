package proxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/metrics"
	"golang.org/x/sync/semaphore"
)

var ErrQueueFull = errors.New("request queue capacity exceeded")

// Config configures traffic control and concurrency bounds.
type Config struct {
	MaxConcurrentRequests int64
	QueueTimeout          time.Duration
}

// Router manages target reverse proxies and active traffic control.
type Router struct {
	sem      *semaphore.Weighted
	metrics  *metrics.Collector
	backends map[string]*httputil.ReverseProxy
	mu       sync.RWMutex
	cfg      Config
}

func NewRouter(cfg Config, collector *metrics.Collector) *Router {
	if cfg.MaxConcurrentRequests <= 0 {
		cfg.MaxConcurrentRequests = 1000
	}
	if cfg.QueueTimeout <= 0 {
		cfg.QueueTimeout = 5 * time.Second
	}

	return &Router{
		cfg:      cfg,
		sem:      semaphore.NewWeighted(cfg.MaxConcurrentRequests),
		metrics:  collector,
		backends: make(map[string]*httputil.ReverseProxy),
	}
}

// RegisterBackend registers or updates a target downstream proxy endpoint.
func (r *Router) RegisterBackend(hostRule string, targetURL *url.URL) {
	r.mu.Lock()
	defer r.mu.Unlock()

	hostRule = NormalizeHost(hostRule)
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.Transport = &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	r.backends[hostRule] = proxy
}

// UpdateBackends synchronizes active host routes.
func (r *Router) UpdateBackends(routes map[string]*url.URL) {
	r.mu.Lock()
	defer r.mu.Unlock()

	newBackends := make(map[string]*httputil.ReverseProxy, len(routes))
	for hostRule, targetURL := range routes {
		cleanHost := NormalizeHost(hostRule)
		proxy := httputil.NewSingleHostReverseProxy(targetURL)
		proxy.Transport = &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		}
		newBackends[cleanHost] = proxy
	}

	r.backends = newBackends
}

// ServeHTTP handles request queuing, concurrency control, and proxy forwarding.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.metrics.TotalRequests.Add(1)

	ctx, cancel := context.WithTimeout(req.Context(), r.cfg.QueueTimeout)
	defer cancel()

	r.metrics.QueuedRequests.Add(1)
	err := r.sem.Acquire(ctx, 1)
	r.metrics.QueuedRequests.Add(-1)

	if err != nil {
		http.Error(w, "Service Overloaded: Request queue timeout", http.StatusServiceUnavailable)
		return
	}
	defer r.sem.Release(1)

	r.metrics.ActiveConcurrency.Add(1)
	defer r.metrics.ActiveConcurrency.Add(-1)

	cleanHost := NormalizeHost(req.Host)

	r.mu.RLock()
	proxy, exists := r.backends[cleanHost]
	r.mu.RUnlock()

	if !exists {
		http.Error(w, "Gateway Error: No matching backend route", http.StatusBadGateway)
		return
	}

	proxy.ServeHTTP(w, req)
}

// NormalizeHost extracts and lowercases the host name without port.
func NormalizeHost(host string) string {
	if strings.Contains(host, ":") {
		h, _, err := net.SplitHostPort(host)
		if err == nil {
			return strings.ToLower(h)
		}
	}
	return strings.ToLower(host)
}
