// Package sandbox owns the in-memory registry of active sandboxes and
// drives their lifecycle: minting IDs and tokens, delegating container
// creation to the runtime, handshaking with the in-sandbox agent, and
// reaping sandboxes whose TTL has lapsed.
//
// The Manager deliberately does not own HTTP — control-plane handlers
// (internal/api/control) call its exported methods, and the reverse
// proxy (internal/api/proxy) consults it for sandbox lookups. Keeping
// HTTP and state separate makes both easier to unit-test.
package sandbox

import "time"

// State is the high-level lifecycle state reported to clients.
type State string

const (
	// StateRunning means the sandbox's container is up and accepting
	// envd traffic. This is the only reachable state in Phase 1.
	StateRunning State = "running"
	// StatePaused is reserved for Phase 4 (docker pause). Not produced
	// by Phase 1 code paths.
	StatePaused State = "paused"
)

// OnTimeoutMode controls what EnforceTimeouts does to a sandbox once
// its ExpiresAt has lapsed. The default (OnTimeoutKill) destroys the
// container; OnTimeoutPause freezes it via runtime.Pause and leaves it
// in the registry for a later /connect to resume. Values are the same
// strings the E2B SDK sends in NewSandbox.lifecycle.onTimeout.
type OnTimeoutMode string

const (
	OnTimeoutKill  OnTimeoutMode = "kill"
	OnTimeoutPause OnTimeoutMode = "pause"
)

// Sandbox is edvabe's view of one active sandbox. Fields mutated over
// the sandbox's lifetime (State, ExpiresAt) are guarded by Manager.mu.
// Callers that receive a *Sandbox from the Manager MUST treat it as
// read-only — use Manager methods to mutate.
type Sandbox struct {
	ID           string
	TemplateID   string
	Alias        string
	ContainerID  string
	AgentHost    string
	AgentPort    int
	EnvdToken    string
	TrafficToken string
	State        State
	OnTimeout    OnTimeoutMode
	Metadata     map[string]string
	EnvVars      map[string]string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}
