// Package agent defines the pluggable contract for whatever speaks the
// envd wire protocol inside a sandbox.
//
// An AgentProvider is responsible for the *what* of a sandbox's
// data plane: ensuring the agent binary is baked into a runnable image,
// handshaking with it after the sandbox starts, and reporting the
// envdVersion the SDK branches on. Phase 1 uses upstream E2B envd
// unchanged (see internal/agent/upstream); future phases may add a
// native Go reimplementation under internal/agent/native.
//
// The AgentProvider never handles HTTP traffic itself — edvabe reverse
// proxies all data-plane requests straight to the agent's listening
// port. Only /init and /health are called by edvabe directly.
package agent

import (
	"context"

	"github.com/contember/edvabe/internal/runtime"
)

// AgentProvider is the interface every in-sandbox agent plugs into.
type AgentProvider interface {
	// Name is used for logging and the --agent= flag.
	Name() string

	// Version is the string returned as `envdVersion` in Sandbox
	// responses. SDKs branch on this; edvabe pins it to "0.5.7" or
	// higher to unlock the newest client code paths.
	Version() string

	// Port is the TCP port the agent listens on inside the sandbox
	// (49983 for upstream envd).
	Port() int

	// EnsureImage makes sure an image tagged `tag` exists on the
	// runtime and contains the agent binary plus its runtime
	// dependencies. Idempotent; safe to call on every `edvabe serve`
	// boot.
	EnsureImage(ctx context.Context, rt runtime.Runtime, tag string) error

	// InitAgent calls the agent's /init endpoint after the sandbox has
	// started, handing it the access token, env vars, and default
	// user/workdir. `endpoint` is a base URL like "http://172.17.0.2:49983".
	InitAgent(ctx context.Context, endpoint string, cfg InitConfig) error

	// Ping probes agent readiness — usually a GET /health with a short
	// retry loop. Returns nil once the agent is accepting traffic.
	Ping(ctx context.Context, endpoint string) error

	// WaitReady runs cmd through the agent's process RPC in a poll
	// loop and returns nil once it exits with status 0. Implementations
	// must honor ctx deadline/cancellation and back off between attempts.
	// When cmd is empty, WaitReady returns immediately — the Phase 1
	// fast path for templates that don't set a readyCmd.
	WaitReady(ctx context.Context, endpoint, cmd string) error
}

// InitConfig is the payload delivered to the agent's /init endpoint.
// Shape mirrors upstream envd's init body (see
// docs/03-api-surface.md § Envd REST).
type InitConfig struct {
	AccessToken    string
	EnvVars        map[string]string
	DefaultUser    string
	DefaultWorkdir string
	VolumeMounts   []VolumeMount
	// HyperloopIP is an optional stub for upstream envd's hyperloop
	// sidecar address. Unused in local dev — kept for wire parity.
	HyperloopIP string
}

// VolumeMount describes a filesystem path inside the sandbox backed by
// an external volume. Phase 1 passes an empty list; Phase 4 will wire
// real volumes through the control plane.
type VolumeMount struct {
	Name      string
	MountPath string
}
