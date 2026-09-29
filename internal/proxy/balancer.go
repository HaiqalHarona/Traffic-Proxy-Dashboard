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
// Improvement: when multiple backends share the minimum, a round-robin offset is used
// to spread sequential traffic evenly rather than always picking index 0.
type LeastConn struct {
	tieBreaker atomic.Uint64
}

// Pick selects the backend with the lowest active connection count.
// On ties it uses an atomic round-robin offset to prevent all sequential
// requests landing on backends[0] when the pool is idle.
func (b *LeastConn) Pick(backends []*Backend, _ string) *Backend {
	if len(backends) == 0 {
		return nil
	}
	minConns := backends[0].ActiveConns.Load()
	for i := 1; i < len(backends); i++ {
		if c := backends[i].ActiveConns.Load(); c < minConns {
			minConns = c
		}
	}

	// Collect all backends that share the minimum connection count.
	tied := backends[:0:0] // zero-len, same underlying type
	for _, b := range backends {
		if b.ActiveConns.Load() == minConns {
			tied = append(tied, b)
		}
	}
	if len(tied) == 1 {
		return tied[0]
	}
	// Break ties with a round-robin offset.
	idx := b.tieBreaker.Add(1) - 1
	return tied[idx%uint64(len(tied))]
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
