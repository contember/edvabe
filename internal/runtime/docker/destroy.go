package docker

import (
	"context"
	"fmt"

	"github.com/moby/moby/client"
)

// Destroy stops and removes the container named after sandboxID. The
// Force flag terminates the process without waiting for a graceful
// shutdown; Phase 1 prioritizes teardown speed over clean exits.
func (r *Runtime) Destroy(ctx context.Context, sandboxID string) error {
	if sandboxID == "" {
		return fmt.Errorf("docker runtime: Destroy: sandboxID is required")
	}
	if _, err := r.cli.ContainerRemove(ctx, sandboxID, client.ContainerRemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("docker runtime: destroy %q: %w", sandboxID, err)
	}
	r.mu.Lock()
	delete(r.endpoints, sandboxID)
	r.mu.Unlock()
	return nil
}

// Pause freezes the container's processes via `docker pause`. The
// container stays resident in memory and keeps its network namespace;
// Unpause thaws it again. This is NOT a memory snapshot — rebooting
// the host drops the state. Callers surface that caveat to users.
func (r *Runtime) Pause(ctx context.Context, sandboxID string) error {
	if sandboxID == "" {
		return fmt.Errorf("docker runtime: Pause: sandboxID is required")
	}
	if _, err := r.cli.ContainerPause(ctx, sandboxID, client.ContainerPauseOptions{}); err != nil {
		return fmt.Errorf("docker runtime: pause %q: %w", sandboxID, err)
	}
	return nil
}

// Unpause thaws a previously-paused container, resuming all of its
// processes from where Pause left them.
func (r *Runtime) Unpause(ctx context.Context, sandboxID string) error {
	if sandboxID == "" {
		return fmt.Errorf("docker runtime: Unpause: sandboxID is required")
	}
	if _, err := r.cli.ContainerUnpause(ctx, sandboxID, client.ContainerUnpauseOptions{}); err != nil {
		return fmt.Errorf("docker runtime: unpause %q: %w", sandboxID, err)
	}
	return nil
}

// Stop halts the container via `docker stop`. Processes receive SIGTERM
// then SIGKILL after Docker's default grace period. Memory is released;
// the writable filesystem layer is preserved so Start can boot it again.
func (r *Runtime) Stop(ctx context.Context, sandboxID string) error {
	if sandboxID == "" {
		return fmt.Errorf("docker runtime: Stop: sandboxID is required")
	}
	if _, err := r.cli.ContainerStop(ctx, sandboxID, client.ContainerStopOptions{}); err != nil {
		return fmt.Errorf("docker runtime: stop %q: %w", sandboxID, err)
	}
	return nil
}

// Start boots a previously stopped container and refreshes the cached
// agent endpoint. Docker may assign a new bridge IP after restart, so
// we re-inspect and overwrite the entry the reverse proxy consults.
func (r *Runtime) Start(ctx context.Context, sandboxID string) error {
	if sandboxID == "" {
		return fmt.Errorf("docker runtime: Start: sandboxID is required")
	}
	if _, err := r.cli.ContainerStart(ctx, sandboxID, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("docker runtime: start %q: %w", sandboxID, err)
	}
	inspect, err := r.cli.ContainerInspect(ctx, sandboxID, client.ContainerInspectOptions{})
	if err != nil {
		return fmt.Errorf("docker runtime: inspect after start %q: %w", sandboxID, err)
	}
	host, err := extractBridgeIP(inspect.Container, r.network)
	if err != nil {
		return fmt.Errorf("docker runtime: resolve bridge IP after start for %q: %w", sandboxID, err)
	}
	r.mu.Lock()
	e := r.endpoints[sandboxID]
	e.host = host
	if e.port == 0 {
		e.port = defaultAgentPort
	}
	r.endpoints[sandboxID] = e
	r.mu.Unlock()
	return nil
}
