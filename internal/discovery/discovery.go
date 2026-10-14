package discovery

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"golang.org/x/sync/errgroup"
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
	PathRule       string
	StripPrefix    bool
	Priority       int
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

// probeResult carries the TCP reachability outcome for a single container.
type probeResult struct {
	index     int
	reachable bool
	probeErr  error
}

// Scan performs a single inspection of ALL active Docker containers.
//
// Every running container is catalogued regardless of labels. Containers with
// traffic-proxy.enable=true or a traffic-proxy.rule label are marked Enabled=true.
// Unlabelled containers default to Enabled=false (ready for the UI toggle).
//
// TCP probes run concurrently (bounded at 20 workers) with a 2-second deadline
// each. Unreachable containers are included in the catalogue with Reachable=false
// and DiscoveryError set — they are never silently dropped.
func (d *DockerProvider) Scan(ctx context.Context) error {
	containers, err := d.cli.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return err
	}

	// --- Build partial targets without probing yet -----------------------
	type partialTarget struct {
		target    ServiceTarget
		needProbe bool
	}
	partial := make([]partialTarget, 0, len(containers))

	for _, c := range containers {
		var containerName string
		if len(c.Names) > 0 {
			containerName = c.Names[0]
		}
		cleanName := containerName
		if len(cleanName) > 0 && cleanName[0] == '/' {
			cleanName = cleanName[1:]
		}

		// --- Derive host rule -------------------------------------------
		// Prefer the explicit traffic-proxy.rule label; fall back to container name.
		rule := c.Labels["traffic-proxy.rule"]
		if rule == "" {
			rule = cleanName
		}

		// Improvement 3: explicit enable=false always wins, even when a rule label exists.
		_, hasRule := c.Labels["traffic-proxy.rule"]
		explicitFalse := c.Labels["traffic-proxy.enable"] == "false"
		enabled := !explicitFalse && (c.Labels["traffic-proxy.enable"] == "true" || hasRule)

		// --- Resolve container IP (improvement 2: deterministic network selection) --------
		// Sort network names so the resolved IP is stable across scans for
		// multi-network containers, preventing backend flapping.
		var targetIP string
		if c.NetworkSettings != nil && len(c.NetworkSettings.Networks) > 0 {
			netNames := make([]string, 0, len(c.NetworkSettings.Networks))
			for name := range c.NetworkSettings.Networks {
				netNames = append(netNames, name)
			}
			sort.Strings(netNames)
			for _, name := range netNames {
				ns := c.NetworkSettings.Networks[name]
				if ns != nil && ns.IPAddress != "" {
					targetIP = ns.IPAddress
					break
				}
			}
		}
		// Fallback: use container name as DNS alias on custom bridge networks.
		if targetIP == "" {
			targetIP = cleanName
		}

		// --- Resolve port -----------------------------------------------
		portStr := c.Labels["traffic-proxy.port"]
		var targetPort int
		if portStr != "" {
			targetPort, _ = strconv.Atoi(portStr)
		} else if len(c.Ports) > 0 {
			targetPort = int(c.Ports[0].PrivatePort)
		} else {
			targetPort = 80
		}

		// --- Path routing label parsing & validation --------------------
		pathRule := "/"
		var pathErr string
		if rawPath, ok := c.Labels["traffic-proxy.path"]; ok {
			if rawPath == "" {
				pathErr = "invalid traffic-proxy.path: must not be empty"
			} else if !strings.HasPrefix(rawPath, "/") {
				pathErr = "invalid traffic-proxy.path: must start with '/'"
			} else if strings.Contains(rawPath, "..") {
				pathErr = "invalid traffic-proxy.path: must not contain '..'"
			} else if strings.ContainsAny(rawPath, " \t\r\n") {
				pathErr = "invalid traffic-proxy.path: must not contain whitespace"
			} else {
				pathRule = path.Clean(rawPath)
				if pathRule == "" {
					pathRule = "/"
				}
			}
		}

		var stripPrefix bool
		var stripErr string
		if stripPrefixStr, ok := c.Labels["traffic-proxy.strip-prefix"]; ok && stripPrefixStr != "" {
			if stripPrefixStr == "true" {
				stripPrefix = true
			} else if stripPrefixStr == "false" {
				stripPrefix = false
			} else {
				stripErr = fmt.Sprintf("invalid traffic-proxy.strip-prefix: must be 'true' or 'false', got %q", stripPrefixStr)
			}
		}

		var priority int
		var priorityErr string
		if priorityStr, ok := c.Labels["traffic-proxy.priority"]; ok && priorityStr != "" {
			p, err := strconv.Atoi(priorityStr)
			if err != nil {
				priorityErr = fmt.Sprintf("invalid traffic-proxy.priority: must be integer, got %q: %v", priorityStr, err)
			} else {
				priority = p
			}
		}

		var validationErr string
		if pathErr != "" {
			validationErr = pathErr
		} else if stripErr != "" {
			validationErr = stripErr
		} else if priorityErr != "" {
			validationErr = priorityErr
		}

		if validationErr != "" {
			partial = append(partial, partialTarget{
				target: ServiceTarget{
					ID:             c.ID,
					Name:           containerName,
					Host:           targetIP,
					Port:           targetPort,
					HostRule:       rule,
					PathRule:       pathRule,
					StripPrefix:    stripPrefix,
					Priority:       priority,
					Labels:         c.Labels,
					Healthy:        false,
					Reachable:      false,
					Enabled:        enabled,
					CreatedAt:      time.Unix(c.Created, 0),
					DiscoveryError: validationErr,
				},
				needProbe: false,
			})
			continue
		}

		// --- Build target URL -------------------------------------------
		rawURL := fmt.Sprintf("http://%s:%d", targetIP, targetPort)
		targetURL, parseErr := url.Parse(rawURL)
		if parseErr != nil {
			partial = append(partial, partialTarget{
				target: ServiceTarget{
					ID:             c.ID,
					Name:           containerName,
					Host:           targetIP,
					Port:           targetPort,
					HostRule:       rule,
					PathRule:       pathRule,
					StripPrefix:    stripPrefix,
					Priority:       priority,
					Labels:         c.Labels,
					Healthy:        false,
					Reachable:      false,
					Enabled:        enabled,
					CreatedAt:      time.Unix(c.Created, 0),
					DiscoveryError: fmt.Sprintf("invalid target URL %q: %v", rawURL, parseErr),
				},
				needProbe: false,
			})
			continue
		}

		partial = append(partial, partialTarget{
			target: ServiceTarget{
				ID:          c.ID,
				Name:        containerName,
				Host:        targetIP,
				Port:        targetPort,
				HostRule:    rule,
				PathRule:    pathRule,
				StripPrefix: stripPrefix,
				Priority:    priority,
				TargetURL:   targetURL,
				Labels:      c.Labels,
				Healthy:     c.State == "running", // will be ANDed with Reachable after probe
				Reachable:   false,
				Enabled:     enabled,
				CreatedAt:   time.Unix(c.Created, 0),
			},
			needProbe: true,
		})
	}

	// Improvement 1: concurrent TCP probes with a bounded worker pool (max 20).
	results := make([]probeResult, len(partial))
	const maxWorkers = 20

	g, gCtx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, maxWorkers)

	for i, p := range partial {
		if !p.needProbe {
			results[i] = probeResult{index: i, reachable: false}
			continue
		}
		i, p := i, p // capture loop vars
		g.Go(func() error {
			sem <- struct{}{}
			defer func() { <-sem }()

			reachable, probeErr := probeReachable(gCtx, p.target.Host, p.target.Port)
			results[i] = probeResult{index: i, reachable: reachable, probeErr: probeErr}
			return nil
		})
	}
	// We intentionally ignore the group error — individual probe failures are
	// surfaced per-container in DiscoveryError, not as a Scan-level failure.
	_ = g.Wait()

	// Apply probe results back onto partial targets.
	targets := make([]ServiceTarget, 0, len(partial))
	for i, p := range partial {
		t := p.target
		if p.needProbe {
			r := results[i]
			t.Reachable = r.reachable
			t.Healthy = t.Healthy && r.reachable
			if r.probeErr != nil {
				t.DiscoveryError = fmt.Sprintf("unreachable (%s:%d): %v", t.Host, t.Port, r.probeErr)
			}
		}
		targets = append(targets, t)
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
