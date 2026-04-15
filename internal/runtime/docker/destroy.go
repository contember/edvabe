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

// Pause is a Phase 4 feature. Stubbed out in Phase 1.
func (r *Runtime) Pause(ctx context.Context, sandboxID string) error {
	return fmt.Errorf("docker runtime: Pause not implemented (phase 4)")
}

// Unpause is a Phase 4 feature. Stubbed out in Phase 1.
func (r *Runtime) Unpause(ctx context.Context, sandboxID string) error {
	return fmt.Errorf("docker runtime: Unpause not implemented (phase 4)")
}
