// Package runtime defines the pluggable sandbox runtime contract.
//
// A Runtime is responsible for the *where* of a sandbox — placing it in a
// container (or VM, later), wiring its network, and telling the reverse
// proxy how to reach the in-sandbox agent. Data-plane operations (exec,
// files, watchers) are handled by the agent inside the sandbox, not here.
package runtime

import (
	"context"
	"time"
)

// Runtime is the interface every sandbox backend implements.
//
// Phase 1 has one implementation: Docker (internal/runtime/docker). A noop
// impl lives in internal/runtime/noop for unit-testing higher layers.
type Runtime interface {
	// Name is used for logging and the --runtime= flag.
	Name() string

	// Create starts a new sandbox and returns a handle for the manager.
	Create(ctx context.Context, req CreateRequest) (*SandboxHandle, error)

	// Destroy stops and removes the sandbox.
	Destroy(ctx context.Context, sandboxID string) error

	// Pause freezes processes inside the sandbox (Phase 4).
	Pause(ctx context.Context, sandboxID string) error

	// Unpause thaws a previously-paused sandbox (Phase 4).
	Unpause(ctx context.Context, sandboxID string) error

	// Commit persists the sandbox filesystem as a new template image (Phase 4).
	Commit(ctx context.Context, sandboxID, imageTag string) error

	// Stats reports resource usage for metrics endpoints.
	Stats(ctx context.Context, sandboxID string) (*Stats, error)

	// BuildImage builds a template image from a build context (Phase 4
	// for user templates; used in Phase 1 by the upstream agent provider
	// to bake the base image).
	BuildImage(ctx context.Context, req BuildRequest) error

	// AgentEndpoint tells the reverse proxy where this sandbox's agent
	// listens. Returns host and port reachable from the edvabe process.
	AgentEndpoint(sandboxID string) (host string, port int, err error)
}

// CreateRequest is the input to Runtime.Create.
type CreateRequest struct {
	SandboxID  string
	Image      string
	EnvVars    map[string]string
	Metadata   map[string]string
	Timeout    time.Duration
	AgentPort  int
	AgentToken string
	BindMounts map[string]string
}

// SandboxHandle is the runtime's view of a created sandbox.
type SandboxHandle struct {
	ContainerID string
	AgentHost   string
	AgentPort   int
	CreatedAt   time.Time
}

// Stats is resource usage for a running sandbox.
type Stats struct {
	CPUUsedPercent float64
	MemoryUsedMB   int64
	MemoryLimitMB  int64
	DiskUsedMB     int64
}

// BuildRequest is the input to Runtime.BuildImage. ContextDir is a path
// on the host containing the Dockerfile and any referenced files.
type BuildRequest struct {
	Tag        string
	ContextDir string
	Dockerfile string
	BuildArgs  map[string]string
	Labels     map[string]string
}
