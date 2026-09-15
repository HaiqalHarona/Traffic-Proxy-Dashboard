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
			ID:      "c-missing-rule",
			Names:   []string{"/app-missing-rule"},
			State:   "running",
			Created: 1700000004,
			Labels: map[string]string{
				"traffic-proxy.enable": "true",
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
	if err := provider.Scan(ctx); err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	services, err := provider.Services()
	if err != nil {
		t.Fatalf("Services failed: %v", err)
	}
	if len(services) != 3 {
		t.Fatalf("Expected 3 discovered services, got %d", len(services))
	}

	// Verify target with port label
	s0 := services[0]
	if s0.HostRule != "app.local" || s0.Port != 8080 || s0.Host != "172.18.0.2" || !s0.Healthy {
		t.Fatalf("Unexpected service[0]: %+v", s0)
	}

	// Verify target with exposed port & fallback name
	s1 := services[1]
	if s1.HostRule != "api.local" || s1.Port != 9000 || s1.Host != "/app-exposed-port" || !s1.Healthy {
		t.Fatalf("Unexpected service[1]: %+v", s1)
	}

	// Verify target with fallback port 80 & exited state
	s2 := services[2]
	if s2.HostRule != "default.local" || s2.Port != 80 || s2.Healthy {
		t.Fatalf("Unexpected service[2]: %+v", s2)
	}

	// Test Start with cancel
	startCtx, startCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer startCancel()

	startErr := provider.Start(startCtx)
	if startErr != context.DeadlineExceeded && startErr != context.Canceled {
		t.Fatalf("Expected context cancellation error, got %v", startErr)
	}
}
