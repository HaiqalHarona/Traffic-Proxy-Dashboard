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

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/discovery"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/metrics"
	"golang.org/x/sync/semaphore"
)

var ErrQueueFull = errors.New("request queue capacity exceeded")

// Config configures traffic control and concurrency bounds.
type Config struct {
	MaxConcurrentRequests int64
	QueueTimeout          time.Duration
}

// BackendPool manages a replica group of backend endpoints with a load balancing algorithm.
type BackendPool struct {
	backends []*Backend
	balancer Balancer
	mu       sync.RWMutex
}

// NewBackendPool initializes a BackendPool with backends and a load balancing algorithm.
func NewBackendPool(backends []*Backend, balancer Balancer) *BackendPool {
	if balancer == nil {
		balancer = NewBalancer("round-robin")
	}
	return &BackendPool{
		backends: backends,
		balancer: balancer,
	}
}

// Pick selects a healthy backend from the pool according to the configured load balancing strategy.
func (p *BackendPool) Pick(clientIP string) *Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.backends) == 0 {
		return nil
	}

	healthy := make([]*Backend, 0, len(p.backends))
	for _, b := range p.backends {
		if b.Healthy {
			healthy = append(healthy, b)
		}
	}
	if len(healthy) == 0 {
		return nil
	}

	return p.balancer.Pick(healthy, clientIP)
}

// Backends returns a snapshot slice of all backends in the pool.
func (p *BackendPool) Backends() []*Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()
	copied := make([]*Backend, len(p.backends))
	copy(copied, p.backends)
	return copied
}

// Router manages target reverse proxies and active traffic control.
type Router struct {
	sem     *semaphore.Weighted
	metrics *metrics.Collector
	pools   map[string]*BackendPool
	mu      sync.RWMutex
	cfg     Config
}

func NewRouter(cfg Config, collector *metrics.Collector) *Router {
	if cfg.MaxConcurrentRequests <= 0 {
		cfg.MaxConcurrentRequests = 1000
	}
	if cfg.QueueTimeout <= 0 {
		cfg.QueueTimeout = 5 * time.Second
	}

	return &Router{
		sem:     semaphore.NewWeighted(cfg.MaxConcurrentRequests),
		metrics: collector,
		pools:   make(map[string]*BackendPool),
		cfg:     cfg,
	}
}

func newReverseProxy(targetURL *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.Transport = &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
	return proxy
}

// RegisterBackend registers or appends a target downstream proxy endpoint to a host's pool.
func (r *Router) RegisterBackend(hostRule string, targetURL *url.URL) {
	r.mu.Lock()
	defer r.mu.Unlock()

	hostRule = NormalizeHost(hostRule)
	backend := &Backend{
		URL:     targetURL,
		Proxy:   newReverseProxy(targetURL),
		Healthy: true,
	}

	pool, exists := r.pools[hostRule]
	if !exists || pool == nil {
		r.pools[hostRule] = NewBackendPool([]*Backend{backend}, NewBalancer("round-robin"))
		return
	}

	pool.mu.Lock()
	pool.backends = append(pool.backends, backend)
	pool.mu.Unlock()
}

// UpdateBackends synchronizes active host routes and multi-replica pools from discovery targets.
func (r *Router) UpdateBackends(targets []discovery.ServiceTarget) {
	r.mu.Lock()
	defer r.mu.Unlock()

	grouped := make(map[string][]discovery.ServiceTarget)
	for _, t := range targets {
		if t.Healthy && t.Enabled && t.TargetURL != nil {
			host := NormalizeHost(t.HostRule)
			if host != "" {
				grouped[host] = append(grouped[host], t)
			}
		}
	}

	newPools := make(map[string]*BackendPool, len(grouped))
	for host, hostTargets := range grouped {
		algorithm := hostTargets[0].Labels["traffic-proxy.balance"]
		backends := make([]*Backend, 0, len(hostTargets))
		for _, t := range hostTargets {
			backends = append(backends, &Backend{
				URL:     t.TargetURL,
				Proxy:   newReverseProxy(t.TargetURL),
				Healthy: t.Healthy,
			})
		}
		newPools[host] = NewBackendPool(backends, NewBalancer(algorithm))
	}

	r.pools = newPools
}

// Backends returns a list of registered backend host rules.
func (r *Router) Backends() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	hosts := make([]string, 0, len(r.pools))
	for h := range r.pools {
		hosts = append(hosts, h)
	}
	return hosts
}

// Pool returns the BackendPool associated with the host rule, or nil.
func (r *Router) Pool(hostRule string) *BackendPool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pools[NormalizeHost(hostRule)]
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
	pool, exists := r.pools[cleanHost]
	r.mu.RUnlock()

	if !exists || pool == nil {
		http.Error(w, "Gateway Error: No matching backend route", http.StatusBadGateway)
		return
	}

	clientIP := req.RemoteAddr
	if host, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		clientIP = host
	}

	backend := pool.Pick(clientIP)
	if backend == nil {
		http.Error(w, "Gateway Error: No healthy backend available", http.StatusBadGateway)
		return
	}

	backend.ActiveConns.Add(1)
	defer backend.ActiveConns.Add(-1)

	backend.Proxy.ServeHTTP(w, req)
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
