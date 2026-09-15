package discovery

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// ServiceTarget represents a discovered downstream backend.
type ServiceTarget struct {
	TargetURL *url.URL
	Labels    map[string]string
	CreatedAt time.Time
	ID        string
	Name      string
	Host      string
	HostRule  string
	Port      int
	Healthy   bool
}

// Provider abstracts service discovery backends (Docker, Swarm, Nomad, Kubernetes, Gossip).
type Provider interface {
	Name() string
	Start(ctx context.Context) error
	Services() ([]ServiceTarget, error)
	Subscribe() <-chan []ServiceTarget
}

// DockerProvider discovers backends from local Docker daemon using container labels.
type DockerProvider struct {
	cli          *client.Client
	services     []ServiceTarget
	eventsChan   chan []ServiceTarget
	mu           sync.RWMutex
	pollInterval time.Duration
}

func NewDockerProvider(interval time.Duration) (*DockerProvider, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	return NewDockerProviderWithClient(cli, interval), nil
}

// NewDockerProviderWithClient creates a DockerProvider with an explicit docker client.
func NewDockerProviderWithClient(cli *client.Client, interval time.Duration) *DockerProvider {
	return &DockerProvider{
		cli:          cli,
		services:     make([]ServiceTarget, 0),
		eventsChan:   make(chan []ServiceTarget, 100),
		pollInterval: interval,
	}
}

// SetServices updates the in-memory targets slice.
func (d *DockerProvider) SetServices(services []ServiceTarget) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.services = services
}

func (d *DockerProvider) Name() string {
	return "docker"
}

func (d *DockerProvider) Start(ctx context.Context) error {
	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()

	// Initial scan
	_ = d.Scan(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			_ = d.Scan(ctx)
		}
	}
}

// Scan performs a single inspection of active Docker containers.
func (d *DockerProvider) Scan(ctx context.Context) error {
	containers, err := d.cli.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return err
	}

	targets := make([]ServiceTarget, 0, len(containers))
	for _, c := range containers {
		if enabled, ok := c.Labels["traffic-proxy.enable"]; !ok || enabled != "true" {
			continue
		}

		rule, ok := c.Labels["traffic-proxy.rule"]
		if !ok || rule == "" {
			continue
		}

		// Resolve container IP address
		var targetIP string
		for _, net := range c.NetworkSettings.Networks {
			if net.IPAddress != "" {
				targetIP = net.IPAddress
				break
			}
		}

		if targetIP == "" {
			targetIP = c.Names[0] // fallback to container name if on custom bridge
		}

		// Resolve container Port
		portStr := c.Labels["traffic-proxy.port"]
		var targetPort int
		if portStr != "" {
			targetPort, _ = strconv.Atoi(portStr)
		} else if len(c.Ports) > 0 {
			targetPort = int(c.Ports[0].PrivatePort)
		} else {
			targetPort = 80 // Default HTTP port fallback
		}

		rawURL := fmt.Sprintf("http://%s:%d", targetIP, targetPort)
		targetURL, err := url.Parse(rawURL)
		if err != nil {
			continue
		}

		targets = append(targets, ServiceTarget{
			ID:        c.ID,
			Name:      c.Names[0],
			Host:      targetIP,
			Port:      targetPort,
			HostRule:  rule,
			TargetURL: targetURL,
			Labels:    c.Labels,
			Healthy:   c.State == "running",
			CreatedAt: time.Unix(c.Created, 0),
		})
	}

	d.mu.Lock()
	d.services = targets
	d.mu.Unlock()

	select {
	case d.eventsChan <- targets:
	default:
	}

	return nil
}

func (d *DockerProvider) Services() ([]ServiceTarget, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	copied := make([]ServiceTarget, len(d.services))
	copy(copied, d.services)
	return copied, nil
}

func (d *DockerProvider) Subscribe() <-chan []ServiceTarget {
	return d.eventsChan
}
