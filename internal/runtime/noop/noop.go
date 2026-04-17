// Package noop is an in-memory Runtime used by unit tests of higher
// layers (sandbox manager, control plane). It does not actually start
// anything — it just tracks sandbox IDs and serves back the handles it
// was given.
package noop

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/contember/edvabe/internal/runtime"
)

type entry struct {
	handle   *runtime.SandboxHandle
	image    string
	envs     map[string]string
	startCmd string
	readyCmd string
	paused   bool
	stopped  bool
}

type Runtime struct {
	mu        sync.RWMutex
	sandboxes map[string]*entry
	images    map[string]bool
}

func New() *Runtime {
	return &Runtime{
		sandboxes: make(map[string]*entry),
		images:    make(map[string]bool),
	}
}

func (r *Runtime) Name() string { return "noop" }

func (r *Runtime) Create(ctx context.Context, req runtime.CreateRequest) (*runtime.SandboxHandle, error) {
	if req.SandboxID == "" {
		return nil, fmt.Errorf("noop: SandboxID is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sandboxes[req.SandboxID]; exists {
		return nil, fmt.Errorf("noop: sandbox %q already exists", req.SandboxID)
	}
	port := req.AgentPort
	if port == 0 {
		port = 49983
	}
	h := &runtime.SandboxHandle{
		ContainerID: "noop-" + req.SandboxID,
		AgentHost:   "127.0.0.1",
		AgentPort:   port,
		CreatedAt:   time.Now(),
	}
	r.sandboxes[req.SandboxID] = &entry{
		handle:   h,
		image:    req.Image,
		envs:     copyStrMap(req.EnvVars),
		startCmd: req.StartCmd,
		readyCmd: req.ReadyCmd,
	}
	return h, nil
}

// Image returns the image tag the named sandbox was launched with,
// or "" if the sandbox is unknown. Exposed for tests.
func (r *Runtime) Image(sandboxID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.sandboxes[sandboxID]; ok {
		return e.image
	}
	return ""
}

// StartCmd / ReadyCmd return the per-sandbox template commands that
// were forwarded through CreateRequest. Exposed for tests that
// exercise the template resolver path.
func (r *Runtime) StartCmd(sandboxID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.sandboxes[sandboxID]; ok {
		return e.startCmd
	}
	return ""
}

func (r *Runtime) ReadyCmd(sandboxID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.sandboxes[sandboxID]; ok {
		return e.readyCmd
	}
	return ""
}

func (r *Runtime) Destroy(ctx context.Context, sandboxID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sandboxes[sandboxID]; !ok {
		return fmt.Errorf("noop: sandbox %q not found", sandboxID)
	}
	delete(r.sandboxes, sandboxID)
	return nil
}

func (r *Runtime) Pause(ctx context.Context, sandboxID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("noop: sandbox %q not found", sandboxID)
	}
	e.paused = true
	return nil
}

func (r *Runtime) Unpause(ctx context.Context, sandboxID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("noop: sandbox %q not found", sandboxID)
	}
	e.paused = false
	return nil
}

func (r *Runtime) Stop(ctx context.Context, sandboxID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("noop: sandbox %q not found", sandboxID)
	}
	e.stopped = true
	e.paused = false
	return nil
}

func (r *Runtime) Start(ctx context.Context, sandboxID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("noop: sandbox %q not found", sandboxID)
	}
	e.stopped = false
	return nil
}

func (r *Runtime) Commit(ctx context.Context, sandboxID, imageTag string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sandboxes[sandboxID]; !ok {
		return fmt.Errorf("noop: sandbox %q not found", sandboxID)
	}
	r.images[imageTag] = true
	return nil
}

func (r *Runtime) Stats(ctx context.Context, sandboxID string) (*runtime.Stats, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.sandboxes[sandboxID]; !ok {
		return nil, fmt.Errorf("noop: sandbox %q not found", sandboxID)
	}
	return &runtime.Stats{}, nil
}

func (r *Runtime) BuildImage(ctx context.Context, req runtime.BuildRequest) error {
	if req.Tag == "" {
		return fmt.Errorf("noop: BuildRequest.Tag is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.images[req.Tag] = true
	return nil
}

func (r *Runtime) AgentEndpoint(sandboxID string) (host string, port int, err error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.sandboxes[sandboxID]
	if !ok {
		return "", 0, fmt.Errorf("noop: sandbox %q not found", sandboxID)
	}
	return e.handle.AgentHost, e.handle.AgentPort, nil
}

// HasImage reports whether an image tag has been registered via BuildImage
// or Commit. Exposed for tests that want to assert build plumbing.
func (r *Runtime) HasImage(tag string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.images[tag]
}

// IsPaused is exposed for tests that exercise Pause/Unpause plumbing.
func (r *Runtime) IsPaused(sandboxID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.sandboxes[sandboxID]
	return ok && e.paused
}

// IsStopped is exposed for tests that exercise Stop/Start plumbing.
func (r *Runtime) IsStopped(sandboxID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.sandboxes[sandboxID]
	return ok && e.stopped
}

func copyStrMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

var _ runtime.Runtime = (*Runtime)(nil)
