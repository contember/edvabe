package sandbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/contember/edvabe/internal/agent"
	"github.com/contember/edvabe/internal/runtime"
)

// Sentinel errors so handlers can discriminate without string matching.
var (
	// ErrNotFound is returned when a lookup targets a sandbox ID the
	// Manager has no record of.
	ErrNotFound = errors.New("sandbox: not found")
	// ErrExpired is returned when a sandbox is still in the map but its
	// ExpiresAt has lapsed — the next EnforceTimeouts pass will reap it.
	ErrExpired = errors.New("sandbox: expired")
)

// Clock is a tiny injection point so tests can drive EnforceTimeouts
// deterministically without wall-clock sleeping. Production uses
// realClock which defers to time.Now.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

const (
	// DefaultImage is the image tag used when CreateOptions.TemplateID
	// is empty. Phase 1 resolves every templateID to this.
	DefaultImage = "edvabe/base:latest"
	// DefaultDomain is the host:port edvabe reports back in the
	// Sandbox.domain field so SDKs route data-plane calls back to us.
	DefaultDomain = "localhost:3000"
	// DefaultTimeout is applied when CreateOptions.Timeout <= 0.
	DefaultTimeout = 5 * time.Minute
	// WatchdogInterval is the ticker cadence of Run's EnforceTimeouts
	// loop. Chosen to be much smaller than the smallest realistic
	// timeout while still being cheap.
	WatchdogInterval = 1 * time.Second
	// defaultUser and defaultWorkdir match the E2B SDK expectations —
	// see docs/05-architecture.md and the task 6 /init smoke test.
	defaultUser    = "user"
	defaultWorkdir = "/home/user"
)

// TemplateResolver maps a client-facing template identifier (alias or
// UUID) onto a concrete image tag plus the template's startCmd /
// readyCmd. The sandbox manager consults it at Create time — in
// Phase 1 this returns the base image unconditionally; Phase 3
// supplies an adapter backed by the template store so user templates
// resolve transparently. Returning ErrTemplateNotFound falls back to
// the base image for backward compatibility with Phase 1 callers.
type TemplateResolver interface {
	Resolve(idOrAlias string) (TemplateResolution, error)
}

// TemplateResolution is the resolver's output.
type TemplateResolution struct {
	ImageTag string
	StartCmd string
	ReadyCmd string
}

// ErrTemplateNotFound signals the resolver has no record of the given
// template. The manager treats this as "use the base image" so
// Phase 1 sandbox IDs like "base" and empty strings keep working.
var ErrTemplateNotFound = errors.New("sandbox: template not found")

// Manager holds the in-memory sandbox registry and drives create /
// destroy / timeout enforcement. It owns no HTTP machinery — callers in
// internal/api consume it through its exported methods.
type Manager struct {
	rt        runtime.Runtime
	ap        agent.AgentProvider
	clock     Clock
	baseImage string
	domain    string
	resolver  TemplateResolver

	mu        sync.RWMutex
	sandboxes map[string]*Sandbox
}

// Options configures NewManager.
type Options struct {
	Runtime   runtime.Runtime
	Agent     agent.AgentProvider
	Clock     Clock
	BaseImage string
	Domain    string
	// Resolver maps templateID → (image, startCmd, readyCmd). Optional:
	// when nil, every create resolves to BaseImage (Phase 1 behaviour).
	Resolver TemplateResolver
}

// NewManager constructs a Manager. Runtime and Agent are required.
func NewManager(opts Options) (*Manager, error) {
	if opts.Runtime == nil {
		return nil, fmt.Errorf("sandbox: NewManager: Runtime is required")
	}
	if opts.Agent == nil {
		return nil, fmt.Errorf("sandbox: NewManager: Agent is required")
	}
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	if opts.BaseImage == "" {
		opts.BaseImage = DefaultImage
	}
	if opts.Domain == "" {
		opts.Domain = DefaultDomain
	}
	return &Manager{
		rt:        opts.Runtime,
		ap:        opts.Agent,
		clock:     opts.Clock,
		baseImage: opts.BaseImage,
		domain:    opts.Domain,
		resolver:  opts.Resolver,
		sandboxes: make(map[string]*Sandbox),
	}, nil
}

// Domain is the host:port edvabe reports in Sandbox responses.
func (m *Manager) Domain() string { return m.domain }

// CreateOptions is the subset of the NewSandbox request body the Manager
// cares about. The control-plane handler translates HTTP JSON into this.
type CreateOptions struct {
	TemplateID string
	Alias      string
	Metadata   map[string]string
	EnvVars    map[string]string
	Timeout    time.Duration
	// OnTimeout controls what EnforceTimeouts does when this sandbox
	// expires. Empty string defaults to OnTimeoutKill (Phase 1 behaviour).
	OnTimeout OnTimeoutMode
}

// Create mints a fresh sandbox, starts its container via the runtime,
// pings and initializes the in-sandbox agent, and registers the result.
// On any mid-flight failure the container is force-removed so nothing
// leaks to the runtime.
func (m *Manager) Create(ctx context.Context, opts CreateOptions) (*Sandbox, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.OnTimeout == "" {
		opts.OnTimeout = OnTimeoutKill
	}
	templateID := opts.TemplateID
	if templateID == "" {
		templateID = "base"
	}

	resolution := m.resolveTemplate(templateID)

	id := NewSandboxID()
	envdToken := NewEnvdToken()
	trafficToken := NewTrafficToken()

	handle, err := m.rt.Create(ctx, runtime.CreateRequest{
		SandboxID:  id,
		Image:      resolution.ImageTag,
		EnvVars:    opts.EnvVars,
		Metadata:   opts.Metadata,
		Timeout:    opts.Timeout,
		AgentPort:  m.ap.Port(),
		AgentToken: envdToken,
		StartCmd:   resolution.StartCmd,
		ReadyCmd:   resolution.ReadyCmd,
	})
	if err != nil {
		return nil, fmt.Errorf("sandbox: create %q: runtime: %w", id, err)
	}

	endpoint := fmt.Sprintf("http://%s:%d", handle.AgentHost, handle.AgentPort)
	if err := m.ap.Ping(ctx, endpoint); err != nil {
		_ = m.rt.Destroy(ctx, id)
		return nil, fmt.Errorf("sandbox: create %q: agent ping: %w", id, err)
	}
	if err := m.ap.InitAgent(ctx, endpoint, agent.InitConfig{
		AccessToken:    envdToken,
		EnvVars:        opts.EnvVars,
		DefaultUser:    defaultUser,
		DefaultWorkdir: defaultWorkdir,
	}); err != nil {
		_ = m.rt.Destroy(ctx, id)
		return nil, fmt.Errorf("sandbox: create %q: agent init: %w", id, err)
	}

	// readyCmd is a template-defined command that must exit 0 before
	// the sandbox is considered fully booted. The fast path (no
	// readyCmd) returns immediately — WaitReady short-circuits on the
	// empty string and never touches envd.
	if resolution.ReadyCmd != "" {
		readyCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		err := m.ap.WaitReady(readyCtx, endpoint, resolution.ReadyCmd, envdToken)
		cancel()
		if err != nil {
			_ = m.rt.Destroy(ctx, id)
			return nil, fmt.Errorf("sandbox: create %q: ready probe: %w", id, err)
		}
	}

	now := m.clock.Now()
	s := &Sandbox{
		ID:           id,
		TemplateID:   templateID,
		Alias:        opts.Alias,
		ContainerID:  handle.ContainerID,
		AgentHost:    handle.AgentHost,
		AgentPort:    handle.AgentPort,
		EnvdToken:    envdToken,
		TrafficToken: trafficToken,
		State:        StateRunning,
		OnTimeout:    opts.OnTimeout,
		Metadata:     cloneMap(opts.Metadata),
		EnvVars:      cloneMap(opts.EnvVars),
		CreatedAt:    now,
		ExpiresAt:    now.Add(opts.Timeout),
	}

	m.mu.Lock()
	m.sandboxes[id] = s
	m.mu.Unlock()

	return s, nil
}

// resolveTemplate consults the injected resolver (if any) and falls
// back to the base image whenever the resolver has no record of the
// given ID. This keeps Phase 1 callers (`templateID: "base"`) working
// even while Phase 3 lets new callers hand in arbitrary user-template
// aliases.
func (m *Manager) resolveTemplate(templateID string) TemplateResolution {
	if m.resolver != nil {
		if r, err := m.resolver.Resolve(templateID); err == nil {
			if r.ImageTag == "" {
				r.ImageTag = m.baseImage
			}
			return r
		}
	}
	return TemplateResolution{ImageTag: m.baseImage}
}

// Get returns the sandbox by ID or ErrNotFound.
func (m *Manager) Get(id string) (*Sandbox, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sandboxes[id]
	if !ok {
		return nil, ErrNotFound
	}
	return s, nil
}

// List returns a snapshot slice of all registered sandboxes. Order is
// unspecified — callers that need stable ordering should sort.
func (m *Manager) List() []*Sandbox {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Sandbox, 0, len(m.sandboxes))
	for _, s := range m.sandboxes {
		out = append(out, s)
	}
	return out
}

// Destroy removes the sandbox from the registry and stops its container.
// Registry is the source of truth: if runtime.Destroy fails after the
// sandbox is removed from the map, the error propagates but the
// Manager's state is still coherent. Stray containers can be reaped
// later via the edvabe.sandbox.id label.
func (m *Manager) Destroy(ctx context.Context, id string) error {
	m.mu.Lock()
	_, ok := m.sandboxes[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	delete(m.sandboxes, id)
	m.mu.Unlock()

	if err := m.rt.Destroy(ctx, id); err != nil {
		return fmt.Errorf("sandbox: destroy %q: %w", id, err)
	}
	return nil
}

// SetTimeout resets the sandbox TTL from the current clock. Returns
// ErrNotFound if the sandbox is unknown or ErrExpired if it already
// lapsed (typically meaning EnforceTimeouts hasn't reaped it yet).
func (m *Manager) SetTimeout(id string, timeout time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sandboxes[id]
	if !ok {
		return ErrNotFound
	}
	now := m.clock.Now()
	if !s.ExpiresAt.After(now) {
		return ErrExpired
	}
	s.ExpiresAt = now.Add(timeout)
	return nil
}

// Connect renews the TTL on a live sandbox and returns the current
// snapshot. A paused sandbox is unpaused through the runtime as part
// of the call so data-plane traffic can resume immediately.
func (m *Manager) Connect(ctx context.Context, id string, timeout time.Duration) (*Sandbox, error) {
	m.mu.Lock()
	s, ok := m.sandboxes[id]
	if !ok {
		m.mu.Unlock()
		return nil, ErrNotFound
	}
	now := m.clock.Now()
	if !s.ExpiresAt.After(now) {
		m.mu.Unlock()
		return nil, ErrExpired
	}
	wasPaused := s.State == StatePaused
	s.ExpiresAt = now.Add(timeout)
	m.mu.Unlock()

	if wasPaused {
		if err := m.rt.Unpause(ctx, id); err != nil {
			return nil, fmt.Errorf("sandbox: connect %q: unpause: %w", id, err)
		}
		m.mu.Lock()
		s.State = StateRunning
		m.mu.Unlock()
	}

	return s, nil
}

// Pause freezes the container via runtime.Pause and flips the
// sandbox's State to paused. The sandbox stays in the registry — its
// TTL still ticks and data-plane traffic is routed to the same
// endpoint once Connect unpauses it.
func (m *Manager) Pause(ctx context.Context, id string) error {
	m.mu.Lock()
	s, ok := m.sandboxes[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if s.State == StatePaused {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	if err := m.rt.Pause(ctx, id); err != nil {
		return fmt.Errorf("sandbox: pause %q: %w", id, err)
	}
	m.mu.Lock()
	s.State = StatePaused
	m.mu.Unlock()
	return nil
}

// SnapshotInfo is the return shape for Snapshot — the caller needs the
// tag to reference later and the point-in-time the snapshot was taken
// at for audit/logging.
type SnapshotInfo struct {
	Name      string
	ImageTag  string
	CreatedAt time.Time
}

// Snapshot captures a container's writable filesystem layer as a new
// image tag via runtime.Commit. It is NOT a memory snapshot — running
// processes are not preserved. Pausing the sandbox first gives a
// consistent filesystem view but is not required.
func (m *Manager) Snapshot(ctx context.Context, id, name string) (*SnapshotInfo, error) {
	m.mu.RLock()
	_, ok := m.sandboxes[id]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	if name == "" {
		name = "snap-" + m.clock.Now().UTC().Format("20060102-150405")
	}
	tag := fmt.Sprintf("edvabe/snapshot-%s:%s", id, name)
	if err := m.rt.Commit(ctx, id, tag); err != nil {
		return nil, fmt.Errorf("sandbox: snapshot %q: %w", id, err)
	}
	return &SnapshotInfo{
		Name:      name,
		ImageTag:  tag,
		CreatedAt: m.clock.Now(),
	}, nil
}

// EnforceTimeouts walks the registry for sandboxes whose ExpiresAt has
// lapsed. Sandboxes with OnTimeoutKill are dropped from the registry
// and their container is destroyed; sandboxes with OnTimeoutPause are
// frozen via runtime.Pause and kept in the registry for a later
// /connect to unpause. Already-paused sandboxes are skipped — once a
// sandbox is paused its TTL no longer ticks. Returns the IDs of every
// sandbox touched by this sweep (killed + paused) so callers can log
// them. Container actions are best-effort — individual runtime failures
// don't stop the sweep.
func (m *Manager) EnforceTimeouts(ctx context.Context) []string {
	m.mu.Lock()
	now := m.clock.Now()
	type expiredEntry struct {
		id  string
		sbx *Sandbox
	}
	var toKill []string
	var toPause []expiredEntry
	for id, s := range m.sandboxes {
		if s.State == StatePaused {
			continue
		}
		if !s.ExpiresAt.After(now) {
			if s.OnTimeout == OnTimeoutPause {
				toPause = append(toPause, expiredEntry{id: id, sbx: s})
			} else {
				toKill = append(toKill, id)
				delete(m.sandboxes, id)
			}
		}
	}
	m.mu.Unlock()

	for _, id := range toKill {
		_ = m.rt.Destroy(ctx, id)
	}

	touched := toKill
	for _, e := range toPause {
		if err := m.rt.Pause(ctx, e.id); err != nil {
			// Pause failed — fall back to destroy so we don't leave a
			// half-state sandbox behind.
			m.mu.Lock()
			delete(m.sandboxes, e.id)
			m.mu.Unlock()
			_ = m.rt.Destroy(ctx, e.id)
			touched = append(touched, e.id)
			continue
		}
		m.mu.Lock()
		e.sbx.State = StatePaused
		m.mu.Unlock()
		touched = append(touched, e.id)
	}
	return touched
}

// Run drives a ticker-based timeout watchdog until ctx is cancelled.
// Intended to be launched as a goroutine from the serve subcommand.
// Pass 0 for interval to use WatchdogInterval.
func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = WatchdogInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.EnforceTimeouts(ctx)
		}
	}
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
