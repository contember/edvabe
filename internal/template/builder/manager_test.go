package builder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/contember/edvabe/internal/template"
)

// fakeExecutor lets tests drive the manager's state machine without
// actually running docker build. Each test constructs one with a
// custom Run function.
type fakeExecutor struct {
	run func(ctx context.Context, spec ExecutorSpec, sink LogSink) error
}

func (f *fakeExecutor) Run(ctx context.Context, spec ExecutorSpec, sink LogSink) error {
	return f.run(ctx, spec, sink)
}

func newTestManager(t *testing.T, exec Executor) *Manager {
	t.Helper()
	m, err := NewManager(ManagerOptions{
		Executor:    exec,
		LogCapacity: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func waitForStatus(t *testing.T, m *Manager, buildID string, want template.BuildStatus, timeout time.Duration) Status {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s, err := m.Status(buildID)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if s.Status == want {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	s, _ := m.Status(buildID)
	t.Fatalf("build %q did not reach %q within %v; current=%q reason=%q", buildID, want, timeout, s.Status, s.Reason)
	return Status{}
}

func TestManagerEnqueueReachesReady(t *testing.T) {
	exec := &fakeExecutor{
		run: func(ctx context.Context, spec ExecutorSpec, sink LogSink) error {
			sink.Append(LogEntry{Level: "info", Message: "step 1"})
			sink.Append(LogEntry{Level: "info", Message: "step 2"})
			return nil
		},
	}
	m := newTestManager(t, exec)
	if err := m.Enqueue(context.Background(), EnqueueSpec{
		TemplateID: "tpl_abc",
		BuildID:    "bld_1",
		Spec:       template.BuildSpec{FromImage: "alpine:latest"},
	}); err != nil {
		t.Fatal(err)
	}
	s := waitForStatus(t, m, "bld_1", template.BuildStatusReady, 2*time.Second)
	if s.FinishedAt == nil {
		t.Fatal("finishedAt not set on ready")
	}
	if s.ResultTag != "edvabe/user-tpl_abc:latest" {
		t.Fatalf("unexpected result tag: %s", s.ResultTag)
	}
	logs, _, err := m.Logs("bld_1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) < 3 {
		// At least: starting msg + 2 step msgs + "build complete" = 4.
		t.Fatalf("expected log entries, got %d", len(logs))
	}
}

func TestManagerEnqueueFailureMarksError(t *testing.T) {
	exec := &fakeExecutor{
		run: func(ctx context.Context, spec ExecutorSpec, sink LogSink) error {
			sink.Append(LogEntry{Level: "error", Message: "oh no"})
			return errors.New("docker build exited 1")
		},
	}
	m := newTestManager(t, exec)
	_ = m.Enqueue(context.Background(), EnqueueSpec{
		TemplateID: "tpl_err",
		BuildID:    "bld_err",
	})
	s := waitForStatus(t, m, "bld_err", template.BuildStatusError, 2*time.Second)
	if s.Reason != "docker build exited 1" {
		t.Fatalf("unexpected reason: %q", s.Reason)
	}
	if s.FinishedAt == nil {
		t.Fatal("finishedAt not set on error")
	}
}

func TestManagerDuplicateEnqueueRejected(t *testing.T) {
	exec := &fakeExecutor{
		run: func(ctx context.Context, spec ExecutorSpec, sink LogSink) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	m := newTestManager(t, exec)
	if err := m.Enqueue(context.Background(), EnqueueSpec{
		TemplateID: "t",
		BuildID:    "bld_dup",
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Enqueue(context.Background(), EnqueueSpec{
		TemplateID: "t",
		BuildID:    "bld_dup",
	}); err == nil {
		t.Fatal("expected duplicate rejection")
	}
	_ = m.Cancel("bld_dup")
}

func TestManagerStatusUnknown(t *testing.T) {
	m := newTestManager(t, &fakeExecutor{})
	if _, err := m.Status("nope"); !errors.Is(err, ErrBuildNotFound) {
		t.Fatalf("expected ErrBuildNotFound, got %v", err)
	}
}

func TestManagerCancelInterruptsRun(t *testing.T) {
	started := make(chan struct{})
	exec := &fakeExecutor{
		run: func(ctx context.Context, spec ExecutorSpec, sink LogSink) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	}
	m := newTestManager(t, exec)
	_ = m.Enqueue(context.Background(), EnqueueSpec{TemplateID: "t", BuildID: "bld_cancel"})
	<-started
	if err := m.Cancel("bld_cancel"); err != nil {
		t.Fatal(err)
	}
	s := waitForStatus(t, m, "bld_cancel", template.BuildStatusError, 2*time.Second)
	if s.Reason == "" {
		t.Fatal("expected non-empty reason on cancel")
	}
}

func TestManagerWait(t *testing.T) {
	exec := &fakeExecutor{
		run: func(ctx context.Context, spec ExecutorSpec, sink LogSink) error {
			time.Sleep(20 * time.Millisecond)
			return nil
		},
	}
	m := newTestManager(t, exec)
	_ = m.Enqueue(context.Background(), EnqueueSpec{TemplateID: "t", BuildID: "bld_wait"})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.Wait(ctx, "bld_wait"); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	s, _ := m.Status("bld_wait")
	if s.Status != template.BuildStatusReady {
		t.Fatalf("after Wait expected ready, got %q", s.Status)
	}
}

func TestManagerLogsIncremental(t *testing.T) {
	// Drive a build that pushes lines on cue via a channel, then poll
	// the logs endpoint incrementally and assert each batch.
	gate := make(chan struct{}, 4)
	release := make(chan struct{})
	exec := &fakeExecutor{
		run: func(ctx context.Context, spec ExecutorSpec, sink LogSink) error {
			for i := 0; i < 4; i++ {
				<-gate
				sink.Append(LogEntry{Level: "info", Message: "chunk"})
			}
			<-release
			return nil
		},
	}
	m := newTestManager(t, exec)
	_ = m.Enqueue(context.Background(), EnqueueSpec{TemplateID: "t", BuildID: "bld_inc"})

	gate <- struct{}{}
	gate <- struct{}{}
	// Poll a few times until we see the 2 chunks settled in the buffer.
	deadline := time.Now().Add(time.Second)
	var firstCursor int64
	for time.Now().Before(deadline) {
		entries, next, err := m.Logs("bld_inc", 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		// Starting line + 2 chunks ≥ 3
		if len(entries) >= 3 {
			firstCursor = next
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if firstCursor == 0 {
		t.Fatal("never saw 2 chunks arrive")
	}

	gate <- struct{}{}
	gate <- struct{}{}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		entries, _, _ := m.Logs("bld_inc", firstCursor, 0)
		if len(entries) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	entries, _, _ := m.Logs("bld_inc", firstCursor, 0)
	if len(entries) < 2 {
		t.Fatalf("incremental read did not see later chunks: %d", len(entries))
	}
	close(release)
	_ = m.Wait(context.Background(), "bld_inc")
}
