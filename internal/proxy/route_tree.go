package proxy

import (
	"errors"
	"path"
	"sort"
	"strings"
)

// RouteEntry represents a routing rule with target backend pool and rewrite preferences.
type RouteEntry struct {
	Host        string
	Path        string // canonical prefix, e.g. "/api/v1"
	StripPrefix bool
	Priority    int
	Pool        *BackendPool
}

// SegmentNode represents a node in the segment-based prefix trie.
type SegmentNode struct {
	part     string
	entry    *RouteEntry
	children []*SegmentNode
}

// HostTrie manages prefix routes for a single normalized host.
type HostTrie struct {
	root   *SegmentNode
	routes []*RouteEntry
}

// RouteTable maps normalized hostnames to their respective HostTrie.
type RouteTable struct {
	hosts map[string]*HostTrie
}

// ValidatePath validates that a path rule meets edge gateway routing standards:
// must be non-empty, start with '/', contain no whitespace, and contain no '..' path traversal sequences.
func ValidatePath(p string) error {
	if p == "" {
		return errors.New("path must not be empty")
	}
	if !strings.HasPrefix(p, "/") {
		return errors.New("path must start with '/'")
	}
	if strings.ContainsAny(p, " \t\r\n") {
		return errors.New("path must not contain whitespace")
	}
	if strings.Contains(p, "..") {
		return errors.New("path must not contain '..'")
	}
	return nil
}

// splitSegments splits a canonical path into slash-separated segments, ignoring empty parts.
func splitSegments(p string) []string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (n *SegmentNode) findChild(part string) *SegmentNode {
	for _, child := range n.children {
		if child.part == part {
			return child
		}
	}
	return nil
}

func (n *SegmentNode) getOrCreateChild(part string) *SegmentNode {
	if child := n.findChild(part); child != nil {
		return child
	}
	child := &SegmentNode{part: part}
	n.children = append(n.children, child)
	return child
}

// NewHostTrie initializes an empty prefix trie for a single host.
func NewHostTrie() *HostTrie {
	return &HostTrie{
		root: &SegmentNode{},
	}
}

// Insert adds or updates a RouteEntry in the host's prefix trie.
func (ht *HostTrie) Insert(entry *RouteEntry) error {
	if entry == nil {
		return errors.New("cannot insert nil route entry")
	}
	if err := ValidatePath(entry.Path); err != nil {
		return err
	}

	canonical := path.Clean(entry.Path)
	if canonical == "" {
		canonical = "/"
	}
	entry.Path = canonical

	segments := splitSegments(canonical)
	curr := ht.root
	for _, seg := range segments {
		curr = curr.getOrCreateChild(seg)
	}
	curr.entry = entry

	updated := false
	for i, existing := range ht.routes {
		if existing.Path == entry.Path {
			ht.routes[i] = entry
			updated = true
			break
		}
	}
	if !updated {
		ht.routes = append(ht.routes, entry)
	}

	return nil
}

// Match performs segment-based prefix matching for the requested path.
// Precedence: Highest Priority integer wins; on tie, Longest Prefix Match (LPM) wins.
// Segment boundaries are strictly isolated (e.g. /api matches /api and /api/v1, but not /apidocs).
func (ht *HostTrie) Match(reqPath string) *RouteEntry {
	if ht == nil || ht.root == nil {
		return nil
	}

	segments := splitSegments(reqPath)
	var candidates []*RouteEntry

	curr := ht.root
	if curr.entry != nil {
		candidates = append(candidates, curr.entry)
	}

	for _, seg := range segments {
		child := curr.findChild(seg)
		if child == nil {
			break
		}
		curr = child
		if curr.entry != nil {
			candidates = append(candidates, curr.entry)
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	best := candidates[0]
	for i := 1; i < len(candidates); i++ {
		c := candidates[i]
		if c.Priority > best.Priority {
			best = c
		} else if c.Priority == best.Priority {
			if len(c.Path) > len(best.Path) {
				best = c
			}
		}
	}

	return best
}

// Routes returns a copy of all registered RouteEntries for this host.
func (ht *HostTrie) Routes() []*RouteEntry {
	if ht == nil {
		return nil
	}
	copied := make([]*RouteEntry, len(ht.routes))
	copy(copied, ht.routes)
	return copied
}

// NewRouteTable initializes an empty RouteTable.
func NewRouteTable() *RouteTable {
	return &RouteTable{
		hosts: make(map[string]*HostTrie),
	}
}

// GetHost returns the HostTrie for the given host, or nil if not found.
func (rt *RouteTable) GetHost(host string) *HostTrie {
	if rt == nil || rt.hosts == nil {
		return nil
	}
	return rt.hosts[NormalizeHost(host)]
}

// Insert registers a RouteEntry into the RouteTable under its host.
func (rt *RouteTable) Insert(entry *RouteEntry) error {
	if entry == nil {
		return errors.New("cannot insert nil route entry")
	}
	cleanHost := NormalizeHost(entry.Host)
	ht, exists := rt.hosts[cleanHost]
	if !exists || ht == nil {
		ht = NewHostTrie()
		rt.hosts[cleanHost] = ht
	}
	return ht.Insert(entry)
}

// Match finds the best matching RouteEntry for the given host and path.
func (rt *RouteTable) Match(host, reqPath string) *RouteEntry {
	ht := rt.GetHost(host)
	if ht == nil {
		return nil
	}
	return ht.Match(reqPath)
}

// Hosts returns a sorted slice of registered normalized hosts.
func (rt *RouteTable) Hosts() []string {
	if rt == nil || rt.hosts == nil {
		return nil
	}
	hosts := make([]string, 0, len(rt.hosts))
	for h := range rt.hosts {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	return hosts
}
