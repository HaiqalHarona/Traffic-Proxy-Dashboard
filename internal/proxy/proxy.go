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
	// TrustedProxyCIDRs lists CIDR blocks whose X-Forwarded-For/X-Real-IP headers
	// are trusted for real client IP extraction (e.g. "10.0.0.0/8", "127.0.0.1/32").
	// When nil/empty, only RemoteAddr is used.
	TrustedProxyCIDRs []string
}

// BackendPool manages a replica group of backend endpoints with a load balancing algorithm.
//
// Improvement 5: the pool maintains a pre-filtered healthy slice that is rebuilt
// only when the set of backends changes, eliminating per-request heap allocations
// in the hot Pick path.
type BackendPool struct {
	backends        []*Backend
	healthyBackends []*Backend // pre-filtered; rebuilt on mutation
	balancer        Balancer
	mu              sync.RWMutex
}

// NewBackendPool initializes a BackendPool with backends and a load balancing algorithm.
func NewBackendPool(backends []*Backend, balancer Balancer) *BackendPool {
	if balancer == nil {
		balancer = NewBalancer("round-robin")
	}
	p := &BackendPool{
		backends: backends,
		balancer: balancer,
	}
	p.healthyBackends = filterHealthy(backends)
	return p
}

// filterHealthy returns a new slice containing only backends marked Healthy.
func filterHealthy(backends []*Backend) []*Backend {
	out := make([]*Backend, 0, len(backends))
	for _, b := range backends {
		if b.Healthy {
			out = append(out, b)
		}
	}
	return out
}

// Pick selects a healthy backend from the pool according to the configured load balancing strategy.
// Uses the pre-filtered healthy slice — no per-request allocation.
func (p *BackendPool) Pick(clientIP string) *Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.healthyBackends) == 0 {
		return nil
	}
	return p.balancer.Pick(p.healthyBackends, clientIP)
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
	// Improvement 4: per-URL backend cache so existing transports/proxies are
	// reused across UpdateBackends calls when the endpoint URL hasn't changed.
	backendCache map[string]*Backend // keyed by targetURL.String()
	mu           sync.RWMutex
	cfg          Config
	trustedNets  []*net.IPNet // parsed from cfg.TrustedProxyCIDRs
}

func NewRouter(cfg Config, collector *metrics.Collector) *Router {
	if cfg.MaxConcurrentRequests <= 0 {
		cfg.MaxConcurrentRequests = 1000
	}
	if cfg.QueueTimeout <= 0 {
		cfg.QueueTimeout = 5 * time.Second
	}

	trustedNets := parseCIDRs(cfg.TrustedProxyCIDRs)

	return &Router{
		sem:          semaphore.NewWeighted(cfg.MaxConcurrentRequests),
		metrics:      collector,
		pools:        make(map[string]*BackendPool),
		backendCache: make(map[string]*Backend),
		cfg:          cfg,
		trustedNets:  trustedNets,
	}
}

// parseCIDRs converts a list of CIDR strings into parsed *net.IPNet values, skipping malformed ones.
func parseCIDRs(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil {
			out = append(out, ipNet)
		}
	}
	return out
}

// isTrustedProxy returns true when the given IP belongs to one of the trusted CIDR ranges.
func (r *Router) isTrustedProxy(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, ipNet := range r.trustedNets {
		if ipNet.Contains(parsed) {
			return true
		}
	}
	return false
}

// clientIPFromRequest extracts the real client IP.
//
// Improvement 7b: when the direct peer (RemoteAddr) belongs to a trusted proxy
// CIDR, X-Real-IP and X-Forwarded-For headers are consulted in that order.
// This ensures IPHash sticky routing works correctly behind edge load balancers.
func (r *Router) clientIPFromRequest(req *http.Request) string {
	remoteAddr := req.RemoteAddr
	peerIP, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		peerIP = remoteAddr
	}

	if r.isTrustedProxy(peerIP) {
		// Try X-Real-IP first (single authoritative IP).
		if xri := strings.TrimSpace(req.Header.Get("X-Real-IP")); xri != "" {
			if ip := net.ParseIP(xri); ip != nil {
				return ip.String()
			}
		}
		// Fall back to the leftmost (originating) IP in X-Forwarded-For.
		if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.SplitN(xff, ",", 2)
			if candidate := strings.TrimSpace(parts[0]); candidate != "" {
				if ip := net.ParseIP(candidate); ip != nil {
					return ip.String()
				}
			}
		}
	}

	return peerIP
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

// getOrCreateBackend looks up an existing backend from the cache by URL, or creates a new one.
// Must be called with r.mu held (write).
func (r *Router) getOrCreateBackend(targetURL *url.URL, healthy bool) *Backend {
	key := targetURL.String()
	if existing, ok := r.backendCache[key]; ok {
		// Reuse transport; update health flag.
		existing.Healthy = healthy
		return existing
	}
	b := &Backend{
		URL:     targetURL,
		Proxy:   newReverseProxy(targetURL),
		Healthy: healthy,
	}
	r.backendCache[key] = b
	return b
}

// pruneBackendCache removes entries from the backend cache that are not referenced
// by any current pool. Must be called with r.mu held (write).
func (r *Router) pruneBackendCache() {
	active := make(map[string]struct{})
	for _, pool := range r.pools {
		for _, b := range pool.backends {
			active[b.URL.String()] = struct{}{}
		}
	}
	for key := range r.backendCache {
		if _, ok := active[key]; !ok {
			delete(r.backendCache, key)
		}
	}
}

// RegisterBackend registers or appends a target downstream proxy endpoint to a host's pool.
func (r *Router) RegisterBackend(hostRule string, targetURL *url.URL) {
	r.mu.Lock()
	defer r.mu.Unlock()

	hostRule = NormalizeHost(hostRule)
	backend := r.getOrCreateBackend(targetURL, true)

	pool, exists := r.pools[hostRule]
	if !exists || pool == nil {
		r.pools[hostRule] = NewBackendPool([]*Backend{backend}, NewBalancer("round-robin"))
		return
	}

	pool.mu.Lock()
	pool.backends = append(pool.backends, backend)
	pool.healthyBackends = filterHealthy(pool.backends)
	pool.mu.Unlock()
}

// UpdateBackends synchronizes active host routes and multi-replica pools from discovery targets.
//
// Improvements applied:
//   - Improvement 4: existing Backend instances (and their http.Transport pools) are reused
//     for endpoints whose URL hasn't changed, avoiding keep-alive connection churn every scan.
//   - Improvement 5: healthyBackends pre-filtered slice is rebuilt after pool construction.
//   - Improvement 6: algorithm label is determined by scanning ALL targets for a host and using
//     the first non-empty value, so label inconsistency across replicas doesn't cause flapping.
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
		// Improvement 6: find the first non-empty balance label across ALL replicas.
		algorithm := ""
		for _, t := range hostTargets {
			if v := t.Labels["traffic-proxy.balance"]; v != "" {
				algorithm = v
				break
			}
		}

		backends := make([]*Backend, 0, len(hostTargets))
		for _, t := range hostTargets {
			// Improvement 4: reuse existing backend if URL is unchanged.
			b := r.getOrCreateBackend(t.TargetURL, t.Healthy)
			backends = append(backends, b)
		}
		newPools[host] = NewBackendPool(backends, NewBalancer(algorithm))
	}

	r.pools = newPools
	// Improvement 4: evict stale cache entries for removed endpoints.
	r.pruneBackendCache()
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

	// Improvement 7b: extract real client IP, respecting trusted proxy headers.
	clientIP := r.clientIPFromRequest(req)

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
