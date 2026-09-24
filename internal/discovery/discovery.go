package discovery

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// ServiceTarget represents a discovered downstream backend.
type ServiceTarget struct {
	TargetURL      *url.URL
	Labels         map[string]string
	CreatedAt      time.Time
	DiscoveryError string // non-empty when the container could not be reached
	ID             string
	Name           string
	Host           string
	HostRule       string
	Port           int
	Healthy        bool
	Reachable      bool // false when TCP probe fails
	Enabled        bool // user opt-in: set by label or future UI toggle
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

// Scan performs a single inspection of ALL active Docker containers.
//
// Every running container is catalogued regardless of labels. Containers with
// traffic-proxy.enable=true or a traffic-proxy.rule label are marked Enabled=true.
// Unlabelled containers default to Enabled=false (ready for the UI toggle).
//
// A non-blocking 2-second TCP probe tests reachability. Unreachable containers
// are included in the catalogue with Reachable=false and DiscoveryError set —
// they are never silently dropped.
func (d *DockerProvider) Scan(ctx context.Context) error {
	containers, err := d.cli.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return err
	}

	targets := make([]ServiceTarget, 0, len(containers))
	for _, c := range containers {
		var containerName string
		if len(c.Names) > 0 {
			containerName = c.Names[0]
		}
		cleanName := containerName
		if len(cleanName) > 0 && cleanName[0] == '/' {
			cleanName = cleanName[1:]
		}

		// --- Derive host rule -------------------------------------------------
		// Prefer the explicit traffic-proxy.rule label; fall back to container name.
		rule := c.Labels["traffic-proxy.rule"]
		if rule == "" {
			rule = cleanName
		}

		// Enabled when explicitly opted-in via label or a rule label is present.
		_, hasRule := c.Labels["traffic-proxy.rule"]
		enabled := c.Labels["traffic-proxy.enable"] == "true" || hasRule

		// --- Resolve container IP ---------------------------------------------
		var targetIP string
		if c.NetworkSettings != nil {
			for _, netSettings := range c.NetworkSettings.Networks {
				if netSettings != nil && netSettings.IPAddress != "" {
					targetIP = netSettings.IPAddress
					break
				}
			}
		}
		// Fallback: use container name as DNS alias on custom bridge networks.
		if targetIP == "" {
			targetIP = cleanName
		}

		// --- Resolve port -----------------------------------------------------
		portStr := c.Labels["traffic-proxy.port"]
		var targetPort int
		if portStr != "" {
			targetPort, _ = strconv.Atoi(portStr)
		} else if len(c.Ports) > 0 {
			targetPort = int(c.Ports[0].PrivatePort)
		} else {
			targetPort = 80
		}

		// --- Build target URL -------------------------------------------------
		rawURL := fmt.Sprintf("http://%s:%d", targetIP, targetPort)
		targetURL, parseErr := url.Parse(rawURL)
		if parseErr != nil {
			targets = append(targets, ServiceTarget{
				ID:             c.ID,
				Name:           containerName,
				Host:           targetIP,
				Port:           targetPort,
				HostRule:       rule,
				Labels:         c.Labels,
				Healthy:        false,
				Reachable:      false,
				Enabled:        enabled,
				CreatedAt:      time.Unix(c.Created, 0),
				DiscoveryError: fmt.Sprintf("invalid target URL %q: %v", rawURL, parseErr),
			})
			continue
		}

		// --- TCP reachability probe -------------------------------------------
		reachable, probeErr := probeReachable(ctx, targetIP, targetPort)

		target := ServiceTarget{
			ID:        c.ID,
			Name:      containerName,
			Host:      targetIP,
			Port:      targetPort,
			HostRule:  rule,
			TargetURL: targetURL,
			Labels:    c.Labels,
			Healthy:   c.State == "running" && reachable,
			Reachable: reachable,
			Enabled:   enabled,
			CreatedAt: time.Unix(c.Created, 0),
		}
		if probeErr != nil {
			target.DiscoveryError = fmt.Sprintf("unreachable (%s:%d): %v", targetIP, targetPort, probeErr)
		}

		targets = append(targets, target)
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

// probeReachable dials the container endpoint over TCP with a 2-second deadline.
// Returns (true, nil) on success or (false, err) on failure.
func probeReachable(ctx context.Context, host string, port int) (bool, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return false, err
	}
	_ = conn.Close()
	return true, nil
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
