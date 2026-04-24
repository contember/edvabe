package docker

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/contember/edvabe/internal/runtime"
)

// ListManaged enumerates containers labeled edvabe.managed=true (including
// paused and stopped) and returns a normalized view. Individual
// inspect failures are swallowed — the caller is rehydrating on
// startup, one bad container shouldn't abort the whole sweep.
// Containers in transitional states (dead / removing / created but
// never started) are filtered out so the manager only sees entries it
// can act on.
func (r *Runtime) ListManaged(ctx context.Context) ([]runtime.ManagedContainer, error) {
	filters := make(client.Filters).Add("label", LabelManaged+"=true")
	result, err := r.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: filters,
	})
	if err != nil {
		return nil, fmt.Errorf("docker runtime: list managed: %w", err)
	}

	log := slog.Default().With("component", "docker.ListManaged")
	out := make([]runtime.ManagedContainer, 0, len(result.Items))
	for _, summary := range result.Items {
		sandboxID := summary.Labels[LabelSandboxID]
		if sandboxID == "" {
			log.Warn("skipping managed container without sandbox-id label", "container_id", summary.ID)
			continue
		}

		inspect, err := r.cli.ContainerInspect(ctx, summary.ID, client.ContainerInspectOptions{})
		if err != nil {
			log.Warn("skipping managed container: inspect failed", "sandbox_id", sandboxID, "container_id", summary.ID, "err", err)
			continue
		}
		mc, ok := r.buildManagedContainer(sandboxID, inspect.Container)
		if !ok {
			status := ""
			if inspect.Container.State != nil {
				status = string(inspect.Container.State.Status)
			}
			log.Warn("skipping managed container in transitional state", "sandbox_id", sandboxID, "state", status)
			continue
		}
		out = append(out, mc)
	}
	return out, nil
}

func (r *Runtime) buildManagedContainer(sandboxID string, c container.InspectResponse) (runtime.ManagedContainer, bool) {
	state, ok := normalizeState(c.State)
	if !ok {
		return runtime.ManagedContainer{}, false
	}

	var host string
	if state != runtime.ContainerStateStopped {
		if h, err := extractBridgeIP(c, r.network); err == nil {
			host = h
		}
	}

	if host != "" {
		r.mu.Lock()
		r.endpoints[sandboxID] = endpoint{host: host, port: defaultAgentPort}
		r.mu.Unlock()
	}

	envs := parseEnvList(c.Config.Env)
	createdAt, _ := time.Parse(time.RFC3339Nano, c.Created)

	cpu := 0
	mem := 0
	if c.HostConfig != nil {
		if n := c.HostConfig.NanoCPUs; n > 0 {
			cpu = int(n / 1_000_000_000)
		}
		if m := c.HostConfig.Memory; m > 0 {
			mem = int(m / (1024 * 1024))
		}
	}

	labels := make(map[string]string, len(c.Config.Labels))
	for k, v := range c.Config.Labels {
		labels[k] = v
	}

	return runtime.ManagedContainer{
		SandboxID:   sandboxID,
		ContainerID: c.ID,
		Image:       c.Config.Image,
		Labels:      labels,
		EnvVars:     envs,
		State:       state,
		CPUCount:    cpu,
		MemoryMB:    mem,
		CreatedAt:   createdAt,
		AgentHost:   host,
		AgentPort:   defaultAgentPort,
	}, true
}

func normalizeState(s *container.State) (runtime.ContainerState, bool) {
	if s == nil {
		return "", false
	}
	switch string(s.Status) {
	case "running":
		return runtime.ContainerStateRunning, true
	case "paused":
		return runtime.ContainerStatePaused, true
	case "exited":
		return runtime.ContainerStateStopped, true
	default:
		return "", false
	}
}

func parseEnvList(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		out[kv[:i]] = kv[i+1:]
	}
	return out
}
