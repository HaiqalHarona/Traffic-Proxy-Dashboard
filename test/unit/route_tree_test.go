package unit_test

import (
	"testing"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/proxy"
)

func TestValidatePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"root", "/", false},
		{"single segment", "/api", false},
		{"multi segment", "/api/v1/users", false},
		{"trailing slash", "/api/", false},
		{"empty path", "", true},
		{"missing leading slash", "api/v1", true},
		{"path traversal double dot", "/api/../admin", true},
		{"path traversal at start", "/../etc/passwd", true},
		{"whitespace space", "/api /v1", true},
		{"whitespace tab", "/api\tv1", true},
		{"whitespace newline", "/api\nv1", true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := proxy.ValidatePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidatePath(%q) err = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestHostTrie_SegmentBoundarySafety(t *testing.T) {
	t.Parallel()

	ht := proxy.NewHostTrie()
	entry := &proxy.RouteEntry{
		Host: "example.local",
		Path: "/api",
	}
	if err := ht.Insert(entry); err != nil {
		t.Fatalf("Failed to insert route: %v", err)
	}

	// Should match /api, /api/, and /api/v1
	if m := ht.Match("/api"); m == nil || m.Path != "/api" {
		t.Fatalf("Expected match for /api, got %+v", m)
	}
	if m := ht.Match("/api/"); m == nil || m.Path != "/api" {
		t.Fatalf("Expected match for /api/, got %+v", m)
	}
	if m := ht.Match("/api/v1"); m == nil || m.Path != "/api" {
		t.Fatalf("Expected match for /api/v1, got %+v", m)
	}

	// Boundary isolation: should NOT match /apidocs or /api-admin
	if m := ht.Match("/apidocs"); m != nil {
		t.Fatalf("Expected nil match for /apidocs (segment boundary isolation), got %+v", m)
	}
	if m := ht.Match("/api-admin"); m != nil {
		t.Fatalf("Expected nil match for /api-admin (segment boundary isolation), got %+v", m)
	}
	if m := ht.Match("/"); m != nil {
		t.Fatalf("Expected nil match for / since root was not registered, got %+v", m)
	}
}

func TestHostTrie_LPM(t *testing.T) {
	t.Parallel()

	ht := proxy.NewHostTrie()
	routes := []*proxy.RouteEntry{
		{Host: "example.local", Path: "/", Priority: 0},
		{Host: "example.local", Path: "/api", Priority: 0},
		{Host: "example.local", Path: "/api/v1", Priority: 0},
		{Host: "example.local", Path: "/api/v1/auth", Priority: 0},
	}
	for _, r := range routes {
		if err := ht.Insert(r); err != nil {
			t.Fatalf("Failed to insert route %s: %v", r.Path, err)
		}
	}

	tests := []struct {
		reqPath  string
		wantPath string
	}{
		{"/api/v1/auth/login", "/api/v1/auth"},
		{"/api/v1/users", "/api/v1"},
		{"/api/v2/products", "/api"},
		{"/about", "/"},
		{"/", "/"},
	}

	for _, tt := range tests {
		m := ht.Match(tt.reqPath)
		if m == nil {
			t.Fatalf("Match(%q) returned nil; want %s", tt.reqPath, tt.wantPath)
		}
		if m.Path != tt.wantPath {
			t.Fatalf("Match(%q) = %s; want %s", tt.reqPath, m.Path, tt.wantPath)
		}
	}
}

func TestHostTrie_PriorityPrecedence(t *testing.T) {
	t.Parallel()

	ht := proxy.NewHostTrie()
	// /api has higher priority (10) than deeper prefix /api/v1 (priority 5)
	if err := ht.Insert(&proxy.RouteEntry{Host: "example.local", Path: "/api", Priority: 10}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := ht.Insert(&proxy.RouteEntry{Host: "example.local", Path: "/api/v1", Priority: 5}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Priority 10 wins over LPM
	m := ht.Match("/api/v1/users")
	if m == nil || m.Path != "/api" {
		t.Fatalf("Expected higher priority route /api (Priority 10) to win, got %+v", m)
	}

	// Update /api/v1 with Priority 20 -> should now win
	if err := ht.Insert(&proxy.RouteEntry{Host: "example.local", Path: "/api/v1", Priority: 20}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	m2 := ht.Match("/api/v1/users")
	if m2 == nil || m2.Path != "/api/v1" {
		t.Fatalf("Expected route /api/v1 (Priority 20) to win, got %+v", m2)
	}
}

func TestRouteTable_HostsAndLookup(t *testing.T) {
	t.Parallel()

	rt := proxy.NewRouteTable()
	_ = rt.Insert(&proxy.RouteEntry{Host: "b.local", Path: "/api"})
	_ = rt.Insert(&proxy.RouteEntry{Host: "a.local", Path: "/"})
	_ = rt.Insert(&proxy.RouteEntry{Host: "c.local:8080", Path: "/v1"})

	hosts := rt.Hosts()
	if len(hosts) != 3 {
		t.Fatalf("Expected 3 hosts, got %d", len(hosts))
	}
	if hosts[0] != "a.local" || hosts[1] != "b.local" || hosts[2] != "c.local" {
		t.Fatalf("Expected sorted hosts [a.local, b.local, c.local], got %v", hosts)
	}

	m := rt.Match("b.local", "/api/test")
	if m == nil || m.Path != "/api" {
		t.Fatalf("Expected match /api on b.local, got %+v", m)
	}

	mNil := rt.Match("unknown.local", "/api")
	if mNil != nil {
		t.Fatalf("Expected nil match for unknown host, got %+v", mNil)
	}
}
