package builder

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/contember/edvabe/internal/template"
)

// ErrBuildNotFound is returned by Status/Logs when the given buildID
// is unknown to the manager.
var ErrBuildNotFound = errors.New("builder: build not found")

// Executor is the abstraction the Manager uses to actually run a
// build. Separating it from the Manager keeps the state machine pure
// Go and unit-testable without Docker — tests pass a fake executor
// that emits canned log lines and completes or fails on cue.
type Executor interface {
	// Run executes one build. Implementations stream log lines into
	// sink as they occur. Returns nil on success (the image is ready
	// at ResultImageTag), or an error describing the failure.
	Run(ctx context.Context, spec ExecutorSpec, sink LogSink) error
}

// ExecutorSpec is everything an Executor needs to run one build. The
// Manager composes it from the BuildSpec sent by the SDK plus the
// resolved template metadata.
type ExecutorSpec struct {
	TemplateID   string
	BuildID      string
	ResultImage  string
	Spec         template.BuildSpec
	ParentImage  string // resolved tag when Spec.FromTemplate is set
}

// LogSink is what the Executor writes log lines to. The Manager's
// implementation appends to the per-build ring buffer.
type LogSink interface {
	Append(LogEntry)
}

// ManagerOptions configures NewManager.
type ManagerOptions struct {
	Executor Executor
	// ResultImageFormat is the format string used to derive the final
	// image tag from the templateID. Defaults to
	// "edvabe/user-%s:latest".
	ResultImageFormat string
	// LogCapacity caps each build's ring buffer. Default 5000.
	LogCapacity int
	// Clock is injected for deterministic timestamps in tests.
	Clock func() time.Time
}

// Manager drives the async template build lifecycle: enqueue →
// building → ready|error, with per-build log ring buffers. Operates
// on top of a pluggable Executor so tests can drive it without
// touching Docker.
type Manager struct {
	executor          Executor
	resultImageFormat string
	logCapacity       int
	clock             func() time.Time

	mu     sync.RWMutex
	builds map[string]*runningBuild
}

type runningBuild struct {
	templateID string
	buildID    string
	status     template.BuildStatus
	reason     string
	resultTag  string
	startedAt  time.Time
	finishedAt *time.Time
	logs       *ringBuffer
	cancel     context.CancelFunc
	done       chan struct{}
}

// NewManager constructs a Manager. Executor is required.
func NewManager(opts ManagerOptions) (*Manager, error) {
	if opts.Executor == nil {
		return nil, errors.New("builder: NewManager: Executor is required")
	}
	if opts.ResultImageFormat == "" {
		opts.ResultImageFormat = "edvabe/user-%s:latest"
	}
	if opts.LogCapacity <= 0 {
		opts.LogCapacity = 5000
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	return &Manager{
		executor:          opts.Executor,
		resultImageFormat: opts.ResultImageFormat,
		logCapacity:       opts.LogCapacity,
		clock:             opts.Clock,
		builds:            make(map[string]*runningBuild),
	}, nil
}

// EnqueueSpec carries the build parameters in from the HTTP handler.
// ParentImage is pre-resolved by the caller when Spec.FromTemplate is
// set; the Manager itself does not touch the template store.
type EnqueueSpec struct {
	TemplateID  string
	BuildID     string
	Spec        template.BuildSpec
	ParentImage string
}

// Enqueue registers a new build, starts its goroutine, and returns
// immediately. The build transitions synchronously through
// waiting → building before Enqueue returns, so a status poll
// immediately after will see "building" (not "waiting"). Duplicate
// enqueues for the same buildID are rejected.
func (m *Manager) Enqueue(ctx context.Context, req EnqueueSpec) error {
	m.mu.Lock()
	if _, exists := m.builds[req.BuildID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("builder: enqueue: build %q already exists", req.BuildID)
	}
	buildCtx, cancel := context.WithCancel(context.Background())
	rb := &runningBuild{
		templateID: req.TemplateID,
		buildID:    req.BuildID,
		status:     template.BuildStatusWaiting,
		resultTag:  fmt.Sprintf(m.resultImageFormat, req.TemplateID),
		startedAt:  m.clock(),
		logs:       newRingBuffer(m.logCapacity),
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	m.builds[req.BuildID] = rb
	m.mu.Unlock()

	go m.run(buildCtx, rb, req)
	return nil
}

func (m *Manager) run(ctx context.Context, rb *runningBuild, req EnqueueSpec) {
	defer close(rb.done)

	m.setStatus(rb, template.BuildStatusBuilding, "")
	rb.logs.Append(LogEntry{
		Timestamp: m.clock(),
		Level:     "info",
		Message:   fmt.Sprintf("Starting build for template %s", req.TemplateID),
	})

	spec := ExecutorSpec{
		TemplateID:  req.TemplateID,
		BuildID:     req.BuildID,
		ResultImage: rb.resultTag,
		Spec:        req.Spec,
		ParentImage: req.ParentImage,
	}
	err := m.executor.Run(ctx, spec, buildSink{rb: rb, clock: m.clock})
	now := m.clock()

	m.mu.Lock()
	defer m.mu.Unlock()
	rb.finishedAt = &now
	if err != nil {
		rb.status = template.BuildStatusError
		rb.reason = err.Error()
		rb.logs.Append(LogEntry{
			Timestamp: now,
			Level:     "error",
			Message:   fmt.Sprintf("Build failed: %s", err.Error()),
		})
		return
	}
	rb.status = template.BuildStatusReady
	rb.logs.Append(LogEntry{
		Timestamp: now,
		Level:     "info",
		Message:   fmt.Sprintf("Build complete: %s", rb.resultTag),
	})
}

// buildSink adapts a runningBuild so the Executor can stream log
// lines straight into the ring buffer without knowing about the
// manager internals.
type buildSink struct {
	rb    *runningBuild
	clock func() time.Time
}

func (b buildSink) Append(entry LogEntry) {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = b.clock()
	}
	if entry.Level == "" {
		entry.Level = "info"
	}
	b.rb.logs.Append(entry)
}

func (m *Manager) setStatus(rb *runningBuild, status template.BuildStatus, reason string) {
	m.mu.Lock()
	rb.status = status
	rb.reason = reason
	m.mu.Unlock()
}

// Status is a snapshot of a build's current state. The shape matches
// the wire response the SDK reads.
type Status struct {
	TemplateID string               `json:"templateID"`
	BuildID    string               `json:"buildID"`
	Status     template.BuildStatus `json:"status"`
	Reason     string               `json:"reason,omitempty"`
	ResultTag  string               `json:"resultTag,omitempty"`
	StartedAt  time.Time            `json:"startedAt"`
	FinishedAt *time.Time           `json:"finishedAt,omitempty"`
}

// Status returns the current state of a build by ID.
func (m *Manager) Status(buildID string) (Status, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rb, ok := m.builds[buildID]
	if !ok {
		return Status{}, ErrBuildNotFound
	}
	return Status{
		TemplateID: rb.templateID,
		BuildID:    rb.buildID,
		Status:     rb.status,
		Reason:     rb.reason,
		ResultTag:  rb.resultTag,
		StartedAt:  rb.startedAt,
		FinishedAt: rb.finishedAt,
	}, nil
}

// Logs reads log entries from a build starting at offset, up to
// limit entries. Returns the entries and the next cursor. Stale
// cursors (pointing at entries already evicted from the ring) are
// snapped forward to the earliest still-held entry.
func (m *Manager) Logs(buildID string, offset int64, limit int) ([]LogEntry, int64, error) {
	m.mu.RLock()
	rb, ok := m.builds[buildID]
	m.mu.RUnlock()
	if !ok {
		return nil, 0, ErrBuildNotFound
	}
	entries, next := rb.logs.Read(offset, limit)
	return entries, next, nil
}

// Wait blocks until the given build finishes (successfully or with
// an error) or ctx is cancelled. Useful in tests and from the
// shutdown path of the serve subcommand.
func (m *Manager) Wait(ctx context.Context, buildID string) error {
	m.mu.RLock()
	rb, ok := m.builds[buildID]
	m.mu.RUnlock()
	if !ok {
		return ErrBuildNotFound
	}
	select {
	case <-rb.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Cancel signals a build's context so the executor can abort. The
// build is marked error with a "cancelled" reason once its goroutine
// observes the signal.
func (m *Manager) Cancel(buildID string) error {
	m.mu.Lock()
	rb, ok := m.builds[buildID]
	m.mu.Unlock()
	if !ok {
		return ErrBuildNotFound
	}
	rb.cancel()
	return nil
}
