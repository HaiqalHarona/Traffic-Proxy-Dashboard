package proxy

import (
	"hash/fnv"
	"math/rand"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
)

// Backend represents a single proxy upstream target with active connection tracking.
type Backend struct {
	URL         *url.URL
	Proxy       *httputil.ReverseProxy
	ActiveConns atomic.Int64
	Healthy     bool
}

// Balancer defines the interface for selecting a backend from a pool.
type Balancer interface {
	Pick(backends []*Backend, clientIP string) *Backend
}

// RoundRobin distributes requests cyclically across backends using an atomic counter.
type RoundRobin struct {
	index atomic.Uint64
}

// Pick selects the next backend in cyclic order.
func (b *RoundRobin) Pick(backends []*Backend, _ string) *Backend {
	n := len(backends)
	if n == 0 {
		return nil
	}
	idx := b.index.Add(1) - 1
	return backends[idx%uint64(n)]
}

// LeastConn selects the backend with the minimum number of active in-flight connections.
type LeastConn struct{}

// Pick selects the backend with the lowest active connection count.
func (b *LeastConn) Pick(backends []*Backend, _ string) *Backend {
	if len(backends) == 0 {
		return nil
	}
	best := backends[0]
	minConns := best.ActiveConns.Load()

	for i := 1; i < len(backends); i++ {
		candidate := backends[i]
		conns := candidate.ActiveConns.Load()
		if conns < minConns {
			minConns = conns
			best = candidate
		}
	}
	return best
}

// Random selects a backend uniformly at random.
type Random struct{}

// Pick selects a backend using uniform pseudo-random distribution.
func (b *Random) Pick(backends []*Backend, _ string) *Backend {
	n := len(backends)
	if n == 0 {
		return nil
	}
	return backends[rand.Intn(n)]
}

// IPHash routes requests deterministically based on FNV-1a hash of client IP for sticky routing.
type IPHash struct{}

// Pick selects a backend using FNV-1a hash modulo the number of backends.
func (b *IPHash) Pick(backends []*Backend, clientIP string) *Backend {
	n := len(backends)
	if n == 0 {
		return nil
	}
	if clientIP == "" {
		return backends[0]
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(clientIP))
	idx := int(h.Sum32() % uint32(n))
	return backends[idx]
}

// NewBalancer creates a Balancer based on the algorithm name.
// Supported values: "round-robin", "least-conn", "random", "ip-hash".
// Defaults to RoundRobin if empty or unknown.
func NewBalancer(algorithm string) Balancer {
	switch strings.ToLower(strings.TrimSpace(algorithm)) {
	case "least-conn", "leastconn", "lc":
		return &LeastConn{}
	case "random", "rand":
		return &Random{}
	case "ip-hash", "iphash", "hash":
		return &IPHash{}
	case "round-robin", "roundrobin", "rr":
		return &RoundRobin{}
	default:
		return &RoundRobin{}
	}
}
