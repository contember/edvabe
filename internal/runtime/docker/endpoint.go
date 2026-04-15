package docker

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// AgentEndpoint returns the host:port the reverse proxy should forward
// envd traffic to for a given sandbox. First consults the in-memory
// cache (populated by Create), then falls back to a live ContainerInspect
// so the call still works if edvabe restarts mid-sandbox.
func (r *Runtime) AgentEndpoint(sandboxID string) (host string, port int, err error) {
	if sandboxID == "" {
		return "", 0, fmt.Errorf("docker runtime: AgentEndpoint: sandboxID is required")
	}

	r.mu.RLock()
	ep, ok := r.endpoints[sandboxID]
	r.mu.RUnlock()
	if ok {
		return ep.host, ep.port, nil
	}

	inspect, err := r.cli.ContainerInspect(context.Background(), sandboxID, client.ContainerInspectOptions{})
	if err != nil {
		return "", 0, fmt.Errorf("docker runtime: inspect %q: %w", sandboxID, err)
	}
	ip, err := extractBridgeIP(inspect.Container)
	if err != nil {
		return "", 0, fmt.Errorf("docker runtime: resolve bridge IP for %q: %w", sandboxID, err)
	}
	return ip, defaultAgentPort, nil
}

// extractBridgeIP returns the first usable IPv4 address from the
// container's network settings. Prefers the default "bridge" network,
// falls back to any other attached network with a valid address.
func extractBridgeIP(inspect container.InspectResponse) (string, error) {
	settings := inspect.NetworkSettings
	if settings == nil {
		return "", fmt.Errorf("container has no network settings")
	}
	if bridge, ok := settings.Networks["bridge"]; ok && bridge != nil && bridge.IPAddress.IsValid() {
		return bridge.IPAddress.String(), nil
	}
	for _, net := range settings.Networks {
		if net != nil && net.IPAddress.IsValid() {
			return net.IPAddress.String(), nil
		}
	}
	return "", fmt.Errorf("container has no IP address assigned")
}
