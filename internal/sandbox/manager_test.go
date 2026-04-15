package sandbox

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/contember/edvabe/internal/agent"
	"github.com/contember/edvabe/internal/runtime"
	"github.com/contember/edvabe/internal/runtime/noop"
)

// fakeClock is a goroutine-safe manual clock for driving the timeout
// watchdog without sleeping. Tests advance the clock explicitly and
// then call EnforceTimeouts to observe reaping.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{t: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// stubAgent is a no-op AgentProvider for tests. It records how many
// times each callback fired so tests can assert the manager actually
// handshook the agent.
type stubAgent struct {
	port        int
	pings       int
	inits       int
	readyCalls  int
	readyCmds   []string
	readyErr    error
}

func (s *stubAgent) Name() string    { return "stub" }
func (s *stubAgent) Version() string { return "0.5.7" }
func (s *stubAgent) Port() int       { return s.port }

func (s *stubAgent) EnsureImage(_ context.Context, _ runtime.Runtime, _ string) error {
	return nil
}

func (s *stubAgent) InitAgent(_ context.Context, _ string, _ agent.InitConfig) error {
	s.inits++
	return nil
}

func (s *stubAgent) Ping(_ context.Context, _ string) error {
	s.pings++
	return nil
}

func (s *stubAgent) WaitReady(_ context.Context, _ string, cmd string) error {
	s.readyCalls++
	s.readyCmds = append(s.readyCmds, cmd)
	return s.readyErr
}

func newTestManager(t *testing.T, clk *fakeClock) (*Manager, *stubAgent) {
	t.Helper()
	ap := &stubAgent{port: 49983}
	m, err := NewManager(Options{
		Runtime: noop.New(),
		Agent:   ap,
		Clock:   clk,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m, ap
}

// newTestManagerWithRuntime mirrors newTestManager but also exposes the
// noop runtime so tests that care about Pause/Unpause/Commit side
// effects can peek at IsPaused / HasImage.
func newTestManagerWithRuntime(t *testing.T, clk *fakeClock) (*Manager, *noop.Runtime) {
	t.Helper()
	rt := noop.New()
	m, err := NewManager(Options{
		Runtime: rt,
		Agent:   &stubAgent{port: 49983},
		Clock:   clk,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m, rt
}

func TestNewManagerRequiresRuntime(t *testing.T) {
	if _, err := NewManager(Options{Agent: &stubAgent{}}); err == nil {
		t.Error("NewManager without Runtime should fail")
	}
}

func TestNewManagerRequiresAgent(t *testing.T) {
	if _, err := NewManager(Options{Runtime: noop.New()}); err == nil {
		t.Error("NewManager without Agent should fail")
	}
}

func TestCreateGetList(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, ap := newTestManager(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{
		Metadata: map[string]string{"owner": "alice"},
		EnvVars:  map[string]string{"FOO": "bar"},
		Timeout:  60 * time.Second,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(s.ID, "isb_") {
		t.Errorf("sandbox ID %q missing isb_ prefix", s.ID)
	}
	if !strings.HasPrefix(s.EnvdToken, "ea_") {
		t.Errorf("envd token %q missing ea_ prefix", s.EnvdToken)
	}
	if !strings.HasPrefix(s.TrafficToken, "ta_") {
		t.Errorf("traffic token %q missing ta_ prefix", s.TrafficToken)
	}
	if s.State != StateRunning {
		t.Errorf("state = %v, want running", s.State)
	}
	if s.TemplateID != "base" {
		t.Errorf("TemplateID = %q, want base", s.TemplateID)
	}
	if s.AgentPort != 49983 {
		t.Errorf("AgentPort = %d, want 49983", s.AgentPort)
	}
	if s.Metadata["owner"] != "alice" {
		t.Errorf("metadata not carried: %v", s.Metadata)
	}
	if s.EnvVars["FOO"] != "bar" {
		t.Errorf("envvars not carried: %v", s.EnvVars)
	}
	if !s.CreatedAt.Equal(clk.Now()) {
		t.Errorf("CreatedAt = %v, want %v", s.CreatedAt, clk.Now())
	}
	if !s.ExpiresAt.Equal(clk.Now().Add(60 * time.Second)) {
		t.Errorf("ExpiresAt = %v, want +60s", s.ExpiresAt)
	}
	if ap.pings != 1 {
		t.Errorf("agent.Ping called %d times, want 1", ap.pings)
	}
	if ap.inits != 1 {
		t.Errorf("agent.InitAgent called %d times, want 1", ap.inits)
	}

	got, err := m.Get(s.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != s.ID {
		t.Errorf("Get returned different sandbox: %s vs %s", got.ID, s.ID)
	}

	list := m.List()
	if len(list) != 1 {
		t.Errorf("List len = %d, want 1", len(list))
	}

	if _, err := m.Get("isb_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}
}

func TestCreateClonesInputMaps(t *testing.T) {
	clk := newFakeClock(time.Now())
	m, _ := newTestManager(t, clk)
	meta := map[string]string{"k": "v"}
	env := map[string]string{"A": "B"}

	s, err := m.Create(context.Background(), CreateOptions{
		Metadata: meta,
		EnvVars:  env,
		Timeout:  time.Minute,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	meta["k"] = "mutated"
	env["A"] = "mutated"
	if s.Metadata["k"] != "v" {
		t.Errorf("Metadata leaked input mutation: %v", s.Metadata)
	}
	if s.EnvVars["A"] != "B" {
		t.Errorf("EnvVars leaked input mutation: %v", s.EnvVars)
	}
}

func TestDestroy(t *testing.T) {
	clk := newFakeClock(time.Now())
	m, _ := newTestManager(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: 60 * time.Second})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := m.Destroy(ctx, s.ID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if list := m.List(); len(list) != 0 {
		t.Errorf("List after Destroy = %d, want 0", len(list))
	}
	if _, err := m.Get(s.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Destroy = %v, want ErrNotFound", err)
	}
	if err := m.Destroy(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Destroy = %v, want ErrNotFound", err)
	}
}

func TestEnforceTimeoutsReapsExpired(t *testing.T) {
	start := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	clk := newFakeClock(start)
	m, _ := newTestManager(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	clk.Advance(29 * time.Second)
	if killed := m.EnforceTimeouts(ctx); len(killed) != 0 {
		t.Errorf("early enforce killed %v", killed)
	}

	clk.Advance(2 * time.Second)
	killed := m.EnforceTimeouts(ctx)
	if len(killed) != 1 || killed[0] != s.ID {
		t.Errorf("enforce killed = %v, want [%s]", killed, s.ID)
	}
	if _, err := m.Get(s.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after reap = %v, want ErrNotFound", err)
	}
}

func TestSetTimeoutExtendsLife(t *testing.T) {
	start := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	clk := newFakeClock(start)
	m, _ := newTestManager(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	clk.Advance(20 * time.Second)
	if err := m.SetTimeout(s.ID, 60*time.Second); err != nil {
		t.Fatalf("SetTimeout: %v", err)
	}

	// New expiry is now + 60s = 20 + 60 = 80s from start.
	clk.Advance(59 * time.Second)
	if killed := m.EnforceTimeouts(ctx); len(killed) != 0 {
		t.Errorf("reaped too early: %v", killed)
	}
	clk.Advance(2 * time.Second)
	if killed := m.EnforceTimeouts(ctx); len(killed) != 1 {
		t.Errorf("did not reap after extended timeout: %v", killed)
	}
}

func TestSetTimeoutOnMissing(t *testing.T) {
	m, _ := newTestManager(t, newFakeClock(time.Now()))
	if err := m.SetTimeout("isb_missing", 30*time.Second); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetTimeout missing = %v, want ErrNotFound", err)
	}
}

func TestSetTimeoutOnExpired(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, _ := newTestManager(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	clk.Advance(31 * time.Second)
	if err := m.SetTimeout(s.ID, 60*time.Second); !errors.Is(err, ErrExpired) {
		t.Errorf("SetTimeout expired = %v, want ErrExpired", err)
	}
}

func TestConnectExtendsTimeout(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, _ := newTestManager(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	clk.Advance(20 * time.Second)
	got, err := m.Connect(ctx, s.ID, 60*time.Second)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if got.ID != s.ID {
		t.Errorf("Connect returned %q, want %q", got.ID, s.ID)
	}
	if !got.ExpiresAt.Equal(clk.Now().Add(60 * time.Second)) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, clk.Now().Add(60*time.Second))
	}
}

func TestConnectExpired(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, _ := newTestManager(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	clk.Advance(31 * time.Second)
	if _, err := m.Connect(ctx, s.ID, 60*time.Second); !errors.Is(err, ErrExpired) {
		t.Errorf("Connect expired = %v, want ErrExpired", err)
	}
}

func TestConnectMissing(t *testing.T) {
	m, _ := newTestManager(t, newFakeClock(time.Now()))
	if _, err := m.Connect(context.Background(), "isb_missing", 30*time.Second); !errors.Is(err, ErrNotFound) {
		t.Errorf("Connect missing = %v, want ErrNotFound", err)
	}
}

func TestCreateAppliesDefaultTimeout(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, _ := newTestManager(t, clk)
	s, err := m.Create(context.Background(), CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := clk.Now().Add(DefaultTimeout)
	if !s.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v (DefaultTimeout)", s.ExpiresAt, want)
	}
}

func TestEnforceTimeoutsReapsMultiple(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, _ := newTestManager(t, clk)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := m.Create(ctx, CreateOptions{Timeout: 10 * time.Second}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	clk.Advance(15 * time.Second)
	killed := m.EnforceTimeouts(ctx)
	if len(killed) != 3 {
		t.Errorf("killed %d, want 3", len(killed))
	}
	if list := m.List(); len(list) != 0 {
		t.Errorf("list after sweep = %d, want 0", len(list))
	}
}

func TestIDGenProducesPrefixedUniqueIDs(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 1000; i++ {
		id := NewSandboxID()
		if !strings.HasPrefix(id, "isb_") {
			t.Fatalf("id %q missing prefix", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id %s on iter %d", id, i)
		}
		seen[id] = struct{}{}
	}
}

func TestTokenPrefixes(t *testing.T) {
	if !strings.HasPrefix(NewEnvdToken(), "ea_") {
		t.Error("envd token missing ea_ prefix")
	}
	if !strings.HasPrefix(NewTrafficToken(), "ta_") {
		t.Error("traffic token missing ta_ prefix")
	}
}

// fakeResolver is a test double for TemplateResolver.
type fakeResolver struct {
	resolutions map[string]TemplateResolution
}

func (f *fakeResolver) Resolve(idOrAlias string) (TemplateResolution, error) {
	if r, ok := f.resolutions[idOrAlias]; ok {
		return r, nil
	}
	return TemplateResolution{}, ErrTemplateNotFound
}

func newManagerWithResolver(t *testing.T, clk *fakeClock, resolver TemplateResolver) (*Manager, *noop.Runtime) {
	t.Helper()
	rt := noop.New()
	m, err := NewManager(Options{
		Runtime:  rt,
		Agent:    &stubAgent{port: 49983},
		Clock:    clk,
		Resolver: resolver,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m, rt
}

func TestCreateUsesResolver(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	resolver := &fakeResolver{
		resolutions: map[string]TemplateResolution{
			"webmaster-chrome": {
				ImageTag: "edvabe/user-tpl_wmx:latest",
				StartCmd: "/home/user/.chrome-start.sh",
				ReadyCmd: "ss -tuln | grep :9222",
			},
		},
	}
	m, rt := newManagerWithResolver(t, clk, resolver)

	sbx, err := m.Create(context.Background(), CreateOptions{TemplateID: "webmaster-chrome"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := rt.Image(sbx.ID); got != "edvabe/user-tpl_wmx:latest" {
		t.Fatalf("runtime received image %q", got)
	}
	if got := rt.StartCmd(sbx.ID); got != "/home/user/.chrome-start.sh" {
		t.Fatalf("StartCmd not forwarded: %q", got)
	}
	if got := rt.ReadyCmd(sbx.ID); got != "ss -tuln | grep :9222" {
		t.Fatalf("ReadyCmd not forwarded: %q", got)
	}
}

func TestCreateFallsBackToBaseWhenResolverMisses(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	resolver := &fakeResolver{} // empty map — every lookup misses
	m, rt := newManagerWithResolver(t, clk, resolver)

	sbx, err := m.Create(context.Background(), CreateOptions{TemplateID: "nonexistent"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := rt.Image(sbx.ID); got != DefaultImage {
		t.Fatalf("fallback image should be %q, got %q", DefaultImage, got)
	}
	if rt.StartCmd(sbx.ID) != "" {
		t.Fatal("StartCmd should be empty for fallback")
	}
}

func TestCreateFallsBackWhenImageTagEmpty(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	resolver := &fakeResolver{
		resolutions: map[string]TemplateResolution{
			"pending": {ImageTag: "", StartCmd: "echo hi"},
		},
	}
	m, rt := newManagerWithResolver(t, clk, resolver)

	sbx, err := m.Create(context.Background(), CreateOptions{TemplateID: "pending"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Empty imageTag → manager should substitute DefaultImage while
	// still forwarding the (non-empty) start command.
	if got := rt.Image(sbx.ID); got != DefaultImage {
		t.Fatalf("empty imageTag should trigger base fallback, got %q", got)
	}
	if rt.StartCmd(sbx.ID) != "echo hi" {
		t.Fatalf("StartCmd lost during fallback: %q", rt.StartCmd(sbx.ID))
	}
}

func TestCreateNoResolverKeepsPhase1Behaviour(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, rt := newManagerWithResolver(t, clk, nil)

	sbx, err := m.Create(context.Background(), CreateOptions{TemplateID: "anything"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := rt.Image(sbx.ID); got != DefaultImage {
		t.Fatalf("no resolver should use DefaultImage, got %q", got)
	}
}

// newManagerWithAgent mirrors newManagerWithResolver but returns the
// underlying stubAgent so tests can assert on WaitReady invocation
// counts and inject a readyErr.
func newManagerWithAgent(t *testing.T, clk *fakeClock, resolver TemplateResolver, ap *stubAgent) (*Manager, *noop.Runtime) {
	t.Helper()
	rt := noop.New()
	m, err := NewManager(Options{
		Runtime:  rt,
		Agent:    ap,
		Clock:    clk,
		Resolver: resolver,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m, rt
}

func TestCreateRunsReadyProbeWhenTemplateHasReadyCmd(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	resolver := &fakeResolver{
		resolutions: map[string]TemplateResolution{
			"chrome": {
				ImageTag: "edvabe/user-chrome:latest",
				ReadyCmd: "curl -f http://localhost:9222/json/version",
			},
		},
	}
	ap := &stubAgent{port: 49983}
	m, _ := newManagerWithAgent(t, clk, resolver, ap)

	if _, err := m.Create(context.Background(), CreateOptions{TemplateID: "chrome"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ap.readyCalls != 1 {
		t.Errorf("readyCalls = %d, want 1", ap.readyCalls)
	}
	if len(ap.readyCmds) != 1 || ap.readyCmds[0] != "curl -f http://localhost:9222/json/version" {
		t.Errorf("readyCmds = %v", ap.readyCmds)
	}
}

func TestCreateSkipsReadyProbeWhenTemplateHasNoReadyCmd(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	ap := &stubAgent{port: 49983}
	m, _ := newManagerWithAgent(t, clk, nil, ap)

	if _, err := m.Create(context.Background(), CreateOptions{TemplateID: "base"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ap.readyCalls != 0 {
		t.Errorf("readyCalls = %d, want 0 (fast path)", ap.readyCalls)
	}
}

func TestPauseFlipsStateAndCallsRuntime(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, rt := newTestManagerWithRuntime(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := m.Pause(ctx, s.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if !rt.IsPaused(s.ID) {
		t.Error("runtime did not see Pause")
	}
	if got, _ := m.Get(s.ID); got.State != StatePaused {
		t.Errorf("State = %q, want paused", got.State)
	}
	// Idempotent.
	if err := m.Pause(ctx, s.ID); err != nil {
		t.Errorf("repeat Pause: %v", err)
	}
}

func TestPauseUnknownSandbox(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, _ := newTestManager(t, clk)
	if err := m.Pause(context.Background(), "isb_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Pause(missing) = %v, want ErrNotFound", err)
	}
}

func TestConnectUnpausesPausedSandbox(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, rt := newTestManagerWithRuntime(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Pause(ctx, s.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if !rt.IsPaused(s.ID) {
		t.Fatal("runtime not paused after Pause")
	}

	resumed, err := m.Connect(ctx, s.ID, 2*time.Minute)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if rt.IsPaused(s.ID) {
		t.Error("runtime still paused after Connect")
	}
	if resumed.State != StateRunning {
		t.Errorf("state after Connect = %q, want running", resumed.State)
	}
}

func TestSnapshotCommitsRuntimeImage(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, rt := newTestManagerWithRuntime(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	info, err := m.Snapshot(ctx, s.ID, "v1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	wantTag := "edvabe/snapshot-" + s.ID + ":v1"
	if info.ImageTag != wantTag {
		t.Errorf("ImageTag = %q, want %q", info.ImageTag, wantTag)
	}
	if info.Name != "v1" {
		t.Errorf("Name = %q, want v1", info.Name)
	}
	if !rt.HasImage(wantTag) {
		t.Errorf("runtime image %q not committed", wantTag)
	}
}

func TestSnapshotGeneratesNameWhenEmpty(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 30, 45, 0, time.UTC))
	m, _ := newTestManager(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	info, err := m.Snapshot(ctx, s.ID, "")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !strings.HasPrefix(info.Name, "snap-") {
		t.Errorf("Name = %q, want snap-<timestamp> prefix", info.Name)
	}
}

func TestSnapshotUnknownSandbox(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, _ := newTestManager(t, clk)
	if _, err := m.Snapshot(context.Background(), "isb_missing", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("Snapshot(missing) = %v, want ErrNotFound", err)
	}
}

func TestCreateDestroysContainerWhenReadyProbeFails(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	resolver := &fakeResolver{
		resolutions: map[string]TemplateResolution{
			"chrome": {
				ImageTag: "edvabe/user-chrome:latest",
				ReadyCmd: "never-returns-0",
			},
		},
	}
	ap := &stubAgent{port: 49983, readyErr: errors.New("timed out waiting")}
	m, rt := newManagerWithAgent(t, clk, resolver, ap)

	_, err := m.Create(context.Background(), CreateOptions{TemplateID: "chrome"})
	if err == nil {
		t.Fatal("Create should have failed")
	}
	if !strings.Contains(err.Error(), "ready probe") {
		t.Errorf("error = %v, want to contain 'ready probe'", err)
	}
	if ap.readyCalls != 1 {
		t.Errorf("readyCalls = %d, want 1", ap.readyCalls)
	}
	// The sandbox must have been torn down — the noop runtime should
	// have no record of it (Image returns "" for unknown IDs).
	if len(m.List()) != 0 {
		t.Errorf("manager still holds %d sandboxes after probe failure", len(m.List()))
	}
	_ = rt
}
