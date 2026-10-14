package proxy

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
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

var pathBufPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

// Router manages target reverse proxies and active traffic control.
type Router struct {
	sem     *semaphore.Weighted
	metrics *metrics.Collector
	routes  atomic.Pointer[RouteTable]
	// Improvement 4: per-URL backend cache so existing transports/proxies are
	// reused across UpdateBackends calls when the endpoint URL hasn't changed.
	backendCache map[string]*Backend // keyed by targetURL.String()
	mu           sync.RWMutex        // protects backendCache and route mutations
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

	r := &Router{
		sem:          semaphore.NewWeighted(cfg.MaxConcurrentRequests),
		metrics:      collector,
		backendCache: make(map[string]*Backend),
		cfg:          cfg,
		trustedNets:  trustedNets,
	}
	r.routes.Store(NewRouteTable())
	return r
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
func (r *Router) pruneBackendCache(table *RouteTable) {
	active := make(map[string]struct{})
	if table != nil {
		for _, ht := range table.hosts {
			for _, entry := range ht.routes {
				for _, b := range entry.Pool.backends {
					active[b.URL.String()] = struct{}{}
				}
			}
		}
	}
	for key := range r.backendCache {
		if _, ok := active[key]; !ok {
			delete(r.backendCache, key)
		}
	}
}

// RegisterBackend registers or appends a target downstream proxy endpoint to a host's root pool.
func (r *Router) RegisterBackend(hostRule string, targetURL *url.URL) {
	r.RegisterRouteBackend(hostRule, "/", false, 0, targetURL)
}

// RegisterRouteBackend registers or appends a target downstream proxy endpoint to a host and path pool.
func (r *Router) RegisterRouteBackend(hostRule, pathRule string, stripPrefix bool, priority int, targetURL *url.URL) {
	r.mu.Lock()
	defer r.mu.Unlock()

	hostRule = NormalizeHost(hostRule)
	if pathRule == "" {
		pathRule = "/"
	}
	canonicalPath := path.Clean(pathRule)
	if canonicalPath == "" {
		canonicalPath = "/"
	}

	backend := r.getOrCreateBackend(targetURL, true)

	// Clone current RouteTable
	current := r.routes.Load()
	newTable := NewRouteTable()
	if current != nil {
		for h, ht := range current.hosts {
			newTrie := NewHostTrie()
			for _, re := range ht.routes {
				newEntry := &RouteEntry{
					Host:        re.Host,
					Path:        re.Path,
					StripPrefix: re.StripPrefix,
					Priority:    re.Priority,
					Pool:        re.Pool,
				}
				_ = newTrie.Insert(newEntry)
			}
			newTable.hosts[h] = newTrie
		}
	}

	ht := newTable.GetHost(hostRule)
	if ht == nil {
		ht = NewHostTrie()
		newTable.hosts[hostRule] = ht
	}

	var existingEntry *RouteEntry
	for _, re := range ht.routes {
		if re.Path == canonicalPath {
			existingEntry = re
			break
		}
	}

	if existingEntry == nil {
		pool := NewBackendPool([]*Backend{backend}, NewBalancer("round-robin"))
		entry := &RouteEntry{
			Host:        hostRule,
			Path:        canonicalPath,
			StripPrefix: stripPrefix,
			Priority:    priority,
			Pool:        pool,
		}
		_ = ht.Insert(entry)
	} else {
		existingEntry.Pool.mu.Lock()
		existingEntry.Pool.backends = append(existingEntry.Pool.backends, backend)
		existingEntry.Pool.healthyBackends = filterHealthy(existingEntry.Pool.backends)
		existingEntry.Pool.mu.Unlock()
	}

	r.routes.Store(newTable)
}

// UpdateBackends synchronizes active host routes and multi-replica pools from discovery targets.
//
// Improvements applied:
//   - Improvement 4: existing Backend instances (and their http.Transport pools) are reused
//     for endpoints whose URL hasn't changed, avoiding keep-alive connection churn every scan.
//   - Improvement 5: healthyBackends pre-filtered slice is rebuilt after pool construction.
//   - Improvement 6: algorithm label is determined by scanning ALL targets for a host/path and using
//     the first non-empty value, so label inconsistency across replicas doesn't cause flapping.
//   - Lock-free atomic route table swapping via atomic.Pointer[RouteTable].
func (r *Router) UpdateBackends(targets []discovery.ServiceTarget) {
	r.mu.Lock()
	defer r.mu.Unlock()

	type routeGroupKey struct {
		host string
		path string
	}

	grouped := make(map[routeGroupKey][]discovery.ServiceTarget)
	for _, t := range targets {
		if t.Healthy && t.Enabled && t.TargetURL != nil {
			host := NormalizeHost(t.HostRule)
			if host != "" {
				p := t.PathRule
				if p == "" {
					p = "/"
				}
				cleanPath := path.Clean(p)
				if cleanPath == "" {
					cleanPath = "/"
				}
				k := routeGroupKey{host: host, path: cleanPath}
				grouped[k] = append(grouped[k], t)
			}
		}
	}

	newTable := NewRouteTable()

	// Organize grouped targets by host
	hostGroups := make(map[string]map[string][]discovery.ServiceTarget)
	for k, hostTargets := range grouped {
		if hostGroups[k.host] == nil {
			hostGroups[k.host] = make(map[string][]discovery.ServiceTarget)
		}
		hostGroups[k.host][k.path] = hostTargets
	}

	for host, pathMap := range hostGroups {
		ht := NewHostTrie()
		for pathRule, pathTargets := range pathMap {
			algorithm := ""
			for _, t := range pathTargets {
				if v := t.Labels["traffic-proxy.balance"]; v != "" {
					algorithm = v
					break
				}
			}

			stripPrefix := pathTargets[0].StripPrefix
			priority := pathTargets[0].Priority

			backends := make([]*Backend, 0, len(pathTargets))
			for _, t := range pathTargets {
				b := r.getOrCreateBackend(t.TargetURL, t.Healthy)
				backends = append(backends, b)
			}

			pool := NewBackendPool(backends, NewBalancer(algorithm))
			entry := &RouteEntry{
				Host:        host,
				Path:        pathRule,
				StripPrefix: stripPrefix,
				Priority:    priority,
				Pool:        pool,
			}
			_ = ht.Insert(entry)
		}
		newTable.hosts[host] = ht
	}

	r.routes.Store(newTable)
	r.pruneBackendCache(newTable)
}

// Backends returns a list of registered backend host rules.
func (r *Router) Backends() []string {
	table := r.routes.Load()
	if table == nil {
		return nil
	}
	return table.Hosts()
}

// Pool returns the root BackendPool associated with the host rule, or nil.
func (r *Router) Pool(hostRule string) *BackendPool {
	return r.RoutePool(hostRule, "/")
}

// RoutePool returns the BackendPool associated with a specific host rule and path, or nil.
func (r *Router) RoutePool(hostRule, pathRule string) *BackendPool {
	table := r.routes.Load()
	if table == nil {
		return nil
	}
	ht := table.GetHost(hostRule)
	if ht == nil {
		return nil
	}
	if pathRule == "" {
		pathRule = "/"
	}
	canonical := path.Clean(pathRule)
	if canonical == "" {
		canonical = "/"
	}
	for _, entry := range ht.routes {
		if entry.Path == canonical {
			return entry.Pool
		}
	}
	if len(ht.routes) > 0 {
		return ht.routes[0].Pool
	}
	return nil
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

	table := r.routes.Load()
	if table == nil {
		http.Error(w, "Gateway Error: No matching backend route", http.StatusBadGateway)
		return
	}

	hostTrie := table.GetHost(cleanHost)
	if hostTrie == nil {
		http.Error(w, "Gateway Error: No matching backend route", http.StatusBadGateway)
		return
	}

	entry := hostTrie.Match(req.URL.Path)
	if entry == nil {
		http.Error(w, "Path Not Found: No matching route prefix", http.StatusNotFound)
		return
	}

	if entry.StripPrefix {
		buf := pathBufPool.Get().(*bytes.Buffer)
		buf.Reset()
		stripped := strings.TrimPrefix(req.URL.Path, entry.Path)
		if stripped == "" || !strings.HasPrefix(stripped, "/") {
			buf.WriteByte('/')
			buf.WriteString(strings.TrimPrefix(stripped, "/"))
		} else {
			buf.WriteString(stripped)
		}
		req.URL.Path = buf.String()

		if req.URL.RawPath != "" {
			buf.Reset()
			rawStripped := strings.TrimPrefix(req.URL.RawPath, entry.Path)
			if rawStripped == "" || !strings.HasPrefix(rawStripped, "/") {
				buf.WriteByte('/')
				buf.WriteString(strings.TrimPrefix(rawStripped, "/"))
			} else {
				buf.WriteString(rawStripped)
			}
			req.URL.RawPath = buf.String()
		}
		pathBufPool.Put(buf)
	}

	req.Header.Set("X-Forwarded-Prefix", entry.Path)

	clientIP := r.clientIPFromRequest(req)

	backend := entry.Pool.Pick(clientIP)
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
