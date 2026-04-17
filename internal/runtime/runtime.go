// Package runtime defines the pluggable sandbox runtime contract.
//
// A Runtime is responsible for the *where* of a sandbox — placing it in a
// container (or VM, later), wiring its network, and telling the reverse
// proxy how to reach the in-sandbox agent. Data-plane operations (exec,
// files, watchers) are handled by the agent inside the sandbox, not here.
package runtime

import (
	"context"
	"io"
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

	// Pause freezes processes inside the sandbox (docker pause). Memory
	// is held; resume via Unpause is instant.
	Pause(ctx context.Context, sandboxID string) error

	// Unpause thaws a previously-paused sandbox.
	Unpause(ctx context.Context, sandboxID string) error

	// Stop halts the container (docker stop). Process memory is lost,
	// filesystem is preserved. Used as the demoted parking state for
	// long-paused sandboxes — see sandbox.Manager.FreezeDuration.
	Stop(ctx context.Context, sandboxID string) error

	// Start boots a previously stopped container and re-resolves its
	// agent endpoint (the bridge IP may change). Callers must Ping and
	// re-InitAgent before forwarding data-plane traffic.
	Start(ctx context.Context, sandboxID string) error

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
	// StartCmd is the user-defined command from the template's
	// setStartCmd() that the edvabe-init wrapper runs alongside envd.
	// The runtime injects it as EDVABE_START_CMD in the container's
	// env. Empty disables the side-process.
	StartCmd string
	// ReadyCmd is the command the sandbox manager probes after
	// InitAgent succeeds (via envd's process RPC) to block
	// sandbox-create until the template's user process reports ready.
	// Passed into the container as EDVABE_READY_CMD for diagnostics.
	// Empty skips the probe loop — Phase 1 fast path.
	ReadyCmd string
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
	// LogWriter, if non-nil, receives the daemon's build output line by
	// line as the build progresses (one logical docker step per line,
	// trailing newline stripped). Used by the template builder to
	// stream progress into its per-build ring buffer. Nil discards
	// output, matching the Phase 1 behaviour.
	LogWriter io.Writer
}
