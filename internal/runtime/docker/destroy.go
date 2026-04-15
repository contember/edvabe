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
