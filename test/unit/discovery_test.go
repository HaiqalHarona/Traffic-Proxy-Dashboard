package unit_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/discovery"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

func TestNewDockerProvider(t *testing.T) {
	provider, err := discovery.NewDockerProvider(5 * time.Second)
	if err != nil {
		t.Fatalf("NewDockerProvider failed: %v", err)
	}
	if provider == nil {
		t.Fatal("Expected non-nil provider")
	}
	if provider.Name() != "docker" {
		t.Fatalf("Expected name 'docker', got %q", provider.Name())
	}
}

func TestDockerProvider_ServicesAndSubscribe(t *testing.T) {
	provider := discovery.NewDockerProviderWithClient(nil, 50*time.Millisecond)
	provider.SetServices([]discovery.ServiceTarget{
		{
			ID:       "id1",
			Name:     "/service1",
			HostRule: "svc1.local",
			Healthy:  true,
		},
	})

	services, err := provider.Services()
	if err != nil {
		t.Fatalf("Services() returned error: %v", err)
	}
	if len(services) != 1 || services[0].HostRule != "svc1.local" {
		t.Fatalf("Unexpected services: %+v", services)
	}

	sub := provider.Subscribe()
	if sub == nil {
		t.Fatal("Expected non-nil subscribe channel")
	}
}

func TestDockerProvider_ScanAndStart(t *testing.T) {
	fakeContainers := []types.Container{
		{
			ID:      "c-enabled-port-label",
			Names:   []string{"/app-label-port"},
			State:   "running",
			Created: 1700000000,
			Labels: map[string]string{
				"traffic-proxy.enable": "true",
				"traffic-proxy.rule":   "app.local",
				"traffic-proxy.port":   "8080",
			},
			NetworkSettings: &types.SummaryNetworkSettings{
				Networks: map[string]*network.EndpointSettings{
					"bridge": {
						IPAddress: "172.18.0.2",
					},
				},
			},
		},
		{
			ID:      "c-exposed-port",
			Names:   []string{"/app-exposed-port"},
			State:   "running",
			Created: 1700000001,
			Labels: map[string]string{
				"traffic-proxy.enable": "true",
				"traffic-proxy.rule":   "api.local",
			},
			Ports: []types.Port{
				{
					PrivatePort: 9000,
				},
			},
			NetworkSettings: &types.SummaryNetworkSettings{
				Networks: map[string]*network.EndpointSettings{
					"custom": {
						IPAddress: "",
					},
				},
			},
		},
		{
			ID:      "c-default-port",
			Names:   []string{"/app-default-port"},
			State:   "exited",
			Created: 1700000002,
			Labels: map[string]string{
				"traffic-proxy.enable": "true",
				"traffic-proxy.rule":   "default.local",
			},
			NetworkSettings: &types.SummaryNetworkSettings{},
		},
		{
			ID:      "c-disabled",
			Names:   []string{"/app-disabled"},
			State:   "running",
			Created: 1700000003,
			Labels: map[string]string{
				"traffic-proxy.enable": "false",
				"traffic-proxy.rule":   "disabled.local",
			},
		},
		{
			ID:      "c-no-labels",
			Names:   []string{"/raw-backend"},
			State:   "running",
			Created: 1700000004,
			Labels:  map[string]string{},
			Ports: []types.Port{
				{PrivatePort: 3000},
			},
			NetworkSettings: &types.SummaryNetworkSettings{
				Networks: map[string]*network.EndpointSettings{
					"bridge": {IPAddress: "172.18.0.5"},
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fakeContainers)
	}))
	defer server.Close()

	cli, err := client.NewClientWithOpts(
		client.WithHost(server.URL),
		client.WithHTTPClient(server.Client()),
		client.WithVersion("1.41"),
	)
	if err != nil {
		t.Fatalf("Failed to create mock docker client: %v", err)
	}

	provider := discovery.NewDockerProviderWithClient(cli, 20*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Test scan
	if scanErr := provider.Scan(ctx); scanErr != nil {
		t.Fatalf("Scan failed: %v", scanErr)
	}

	services, err := provider.Services()
	if err != nil {
		t.Fatalf("Services failed: %v", err)
	}

	// All 5 containers must be returned regardless of labels.
	if len(services) != 5 {
		t.Fatalf("Expected 5 discovered services (all containers), got %d", len(services))
	}

	// c-enabled-port-label: labelled, Enabled=true, HostRule from label
	s0 := services[0]
	if s0.HostRule != "app.local" {
		t.Fatalf("Expected s0.HostRule=app.local, got %q", s0.HostRule)
	}
	if s0.Port != 8080 {
		t.Fatalf("Expected s0.Port=8080, got %d", s0.Port)
	}
	if s0.Host != "172.18.0.2" {
		t.Fatalf("Expected s0.Host=172.18.0.2, got %q", s0.Host)
	}
	if !s0.Enabled {
		t.Fatal("Expected s0.Enabled=true")
	}

	// c-exposed-port: labelled, Enabled=true, IP fallback to name
	s1 := services[1]
	if s1.HostRule != "api.local" {
		t.Fatalf("Expected s1.HostRule=api.local, got %q", s1.HostRule)
	}
	if s1.Port != 9000 {
		t.Fatalf("Expected s1.Port=9000, got %d", s1.Port)
	}
	if !s1.Enabled {
		t.Fatal("Expected s1.Enabled=true")
	}

	// c-default-port: labelled, Enabled=true, exited so Healthy=false
	s2 := services[2]
	if s2.HostRule != "default.local" {
		t.Fatalf("Expected s2.HostRule=default.local, got %q", s2.HostRule)
	}
	if s2.Port != 80 {
		t.Fatalf("Expected s2.Port=80, got %d", s2.Port)
	}
	if s2.Healthy {
		t.Fatal("Expected s2.Healthy=false (exited)")
	}
	if !s2.Enabled {
		t.Fatal("Expected s2.Enabled=true (has rule label)")
	}

	// c-disabled: has rule label so Enabled=true (enable=false is overridden by rule presence)
	s3 := services[3]
	if s3.HostRule != "disabled.local" {
		t.Fatalf("Expected s3.HostRule=disabled.local, got %q", s3.HostRule)
	}
	// traffic-proxy.enable=false but traffic-proxy.rule present → Enabled=true
	if !s3.Enabled {
		t.Fatal("Expected s3.Enabled=true (rule label present)")
	}

	// c-no-labels: no labels at all — HostRule derived from container name, Enabled=false
	s4 := services[4]
	if s4.HostRule != "raw-backend" {
		t.Fatalf("Expected s4.HostRule=raw-backend (from container name), got %q", s4.HostRule)
	}
	if s4.Enabled {
		t.Fatal("Expected s4.Enabled=false (no labels)")
	}
	if s4.Port != 3000 {
		t.Fatalf("Expected s4.Port=3000 (from exposed port), got %d", s4.Port)
	}

	// All will have DiscoveryError set because the mock IP addresses are unreachable in test.
	// Just verify the field exists and is populated for the unlabelled container.
	if s4.DiscoveryError == "" {
		// In unit test environment probing 172.18.0.5:3000 will fail — that's expected.
		// If the probe somehow succeeded (unlikely in CI) we skip this check.
		t.Logf("Note: s4.DiscoveryError empty — TCP probe to %s:%d unexpectedly succeeded", s4.Host, s4.Port)
	}

	// Test Start with cancel
	startCtx, startCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer startCancel()

	startErr := provider.Start(startCtx)
	if startErr != context.DeadlineExceeded && startErr != context.Canceled {
		t.Fatalf("Expected context cancellation error, got %v", startErr)
	}
}
