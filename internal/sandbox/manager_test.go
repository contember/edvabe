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

func (s *stubAgent) WaitReady(_ context.Context, _, cmd, _ string) error {
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

func TestEnforceTimeoutsPausesWhenOnTimeoutPause(t *testing.T) {
	start := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	clk := newFakeClock(start)
	m, rt := newTestManagerWithRuntime(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{
		Timeout:   30 * time.Second,
		OnTimeout: OnTimeoutPause,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	clk.Advance(29 * time.Second)
	if touched := m.EnforceTimeouts(ctx); len(touched) != 0 {
		t.Errorf("early enforce touched %v", touched)
	}

	clk.Advance(2 * time.Second)
	touched := m.EnforceTimeouts(ctx)
	if len(touched) != 1 || touched[0] != s.ID {
		t.Fatalf("enforce touched = %v, want [%s]", touched, s.ID)
	}
	if !rt.IsPaused(s.ID) {
		t.Error("runtime did not see Pause on timeout")
	}
	got, err := m.Get(s.ID)
	if err != nil {
		t.Fatalf("Get after pause-on-timeout: %v", err)
	}
	if got.State != StatePaused {
		t.Errorf("State = %q, want paused", got.State)
	}
	// Second sweep must not re-pause an already-paused sandbox.
	if again := m.EnforceTimeouts(ctx); len(again) != 0 {
		t.Errorf("second sweep touched %v, want none", again)
	}
}

func TestEnforceTimeoutsKillsWhenOnTimeoutKill(t *testing.T) {
	start := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	clk := newFakeClock(start)
	m, rt := newTestManagerWithRuntime(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{
		Timeout:   30 * time.Second,
		OnTimeout: OnTimeoutKill,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	clk.Advance(31 * time.Second)
	touched := m.EnforceTimeouts(ctx)
	if len(touched) != 1 || touched[0] != s.ID {
		t.Fatalf("enforce touched = %v, want [%s]", touched, s.ID)
	}
	if rt.IsPaused(s.ID) {
		t.Error("kill branch should not pause the runtime container")
	}
	if _, err := m.Get(s.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after kill = %v, want ErrNotFound", err)
	}
}

func TestCreateDefaultsOnTimeoutToKill(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, _ := newTestManager(t, clk)
	s, err := m.Create(context.Background(), CreateOptions{Timeout: time.Minute})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if s.OnTimeout != OnTimeoutKill {
		t.Errorf("OnTimeout = %q, want %q", s.OnTimeout, OnTimeoutKill)
	}
}

func TestEnforceTimeoutsPauseFailureFallsBackToDestroy(t *testing.T) {
	start := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	clk := newFakeClock(start)
	rt := &failingPauseRuntime{Runtime: noop.New()}
	m, err := NewManager(Options{
		Runtime: rt,
		Agent:   &stubAgent{port: 49983},
		Clock:   clk,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{
		Timeout:   30 * time.Second,
		OnTimeout: OnTimeoutPause,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	clk.Advance(31 * time.Second)
	touched := m.EnforceTimeouts(ctx)
	if len(touched) != 1 || touched[0] != s.ID {
		t.Fatalf("enforce touched = %v, want [%s]", touched, s.ID)
	}
	// Sandbox should be gone from the registry since the fallback
	// dropped it after runtime.Pause failed.
	if _, err := m.Get(s.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after pause-failure fallback = %v, want ErrNotFound", err)
	}
}

// failingPauseRuntime wraps a noop runtime and rejects Pause calls.
// Used to exercise EnforceTimeouts' fallback-to-destroy path.
type failingPauseRuntime struct {
	*noop.Runtime
}

func (f *failingPauseRuntime) Pause(ctx context.Context, sandboxID string) error {
	return errors.New("pause kaputt")
}

// newTestManagerWithPolicy mirrors newTestManagerWithRuntime but lets
// the test override the pause policy knobs. A zero value in opts is
// left as-is so defaults still apply — override with a sentinel like
// time.Nanosecond / 1 to exercise aggressive demotion / GC.
func newTestManagerWithPolicy(t *testing.T, clk *fakeClock, freeze time.Duration, maxFrozen int, stoppedGC time.Duration) (*Manager, *noop.Runtime, *stubAgent) {
	t.Helper()
	rt := noop.New()
	ap := &stubAgent{port: 49983}
	m, err := NewManager(Options{
		Runtime:        rt,
		Agent:          ap,
		Clock:          clk,
		FreezeDuration: freeze,
		MaxFrozen:      maxFrozen,
		StoppedGCAfter: stoppedGC,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m, rt, ap
}

func TestPauseRecordsModeAndPausedAt(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	clk := newFakeClock(now)
	m, _ := newTestManagerWithRuntime(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: time.Hour})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	clk.Advance(5 * time.Second)
	if err := m.Pause(ctx, s.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	got, _ := m.Get(s.ID)
	if got.PauseMode != PauseFrozen {
		t.Errorf("PauseMode = %q, want %q", got.PauseMode, PauseFrozen)
	}
	if !got.PausedAt.Equal(clk.Now()) {
		t.Errorf("PausedAt = %v, want %v", got.PausedAt, clk.Now())
	}
}

func TestEnforceTimeoutsDemotesFrozenAfterFreezeDuration(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	clk := newFakeClock(now)
	// Aggressive freeze duration so the clock advance below triggers demote.
	m, rt, _ := newTestManagerWithPolicy(t, clk, time.Minute, 10, time.Hour)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Pause(ctx, s.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if !rt.IsPaused(s.ID) || rt.IsStopped(s.ID) {
		t.Fatalf("pre-demote: IsPaused=%v IsStopped=%v", rt.IsPaused(s.ID), rt.IsStopped(s.ID))
	}

	clk.Advance(30 * time.Second)
	if touched := m.EnforceTimeouts(ctx); len(touched) != 0 {
		t.Errorf("early sweep touched %v", touched)
	}

	clk.Advance(31 * time.Second)
	touched := m.EnforceTimeouts(ctx)
	if len(touched) != 1 || touched[0] != s.ID {
		t.Fatalf("demote sweep touched %v, want [%s]", touched, s.ID)
	}
	if !rt.IsStopped(s.ID) {
		t.Error("runtime should be stopped after demote")
	}
	got, _ := m.Get(s.ID)
	if got.PauseMode != PauseStopped {
		t.Errorf("PauseMode = %q, want %q", got.PauseMode, PauseStopped)
	}
	if !got.PausedAt.Equal(clk.Now()) {
		t.Errorf("PausedAt after demote = %v, want %v", got.PausedAt, clk.Now())
	}
}

func TestEnforceTimeoutsEvictsOldestWhenMaxFrozenExceeded(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	clk := newFakeClock(now)
	// FreezeDuration huge so only the LRU cap triggers demotion.
	m, rt, _ := newTestManagerWithPolicy(t, clk, 30*24*time.Hour, 2, time.Hour)
	ctx := context.Background()

	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		s, err := m.Create(ctx, CreateOptions{Timeout: 24 * time.Hour})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		clk.Advance(10 * time.Second) // so PausedAt values differ
		if err := m.Pause(ctx, s.ID); err != nil {
			t.Fatalf("Pause %d: %v", i, err)
		}
		ids = append(ids, s.ID)
	}

	touched := m.EnforceTimeouts(ctx)
	if len(touched) != 1 || touched[0] != ids[0] {
		t.Fatalf("eviction touched %v, want [%s] (oldest)", touched, ids[0])
	}
	if !rt.IsStopped(ids[0]) {
		t.Error("oldest frozen should be demoted to stopped")
	}
	if rt.IsStopped(ids[1]) || rt.IsStopped(ids[2]) {
		t.Error("within-cap frozen should still be frozen")
	}
}

func TestEnforceTimeoutsGCsStoppedAfterGCDuration(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	clk := newFakeClock(now)
	m, rt, _ := newTestManagerWithPolicy(t, clk, time.Second, 10, 2*time.Second)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Pause(ctx, s.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	// Demote: advance past FreezeDuration.
	clk.Advance(2 * time.Second)
	if touched := m.EnforceTimeouts(ctx); len(touched) != 1 {
		t.Fatalf("demote sweep touched %v, want 1", touched)
	}
	if !rt.IsStopped(s.ID) {
		t.Fatal("runtime should be stopped after demote")
	}
	// Not yet GC'd.
	clk.Advance(time.Second)
	if touched := m.EnforceTimeouts(ctx); len(touched) != 0 {
		t.Errorf("early GC touched %v", touched)
	}
	if _, err := m.Get(s.ID); err != nil {
		t.Errorf("Get before GC = %v, want nil", err)
	}
	// GC: advance past StoppedGCAfter from the demote moment.
	clk.Advance(2 * time.Second)
	touched := m.EnforceTimeouts(ctx)
	if len(touched) != 1 || touched[0] != s.ID {
		t.Fatalf("GC sweep touched %v, want [%s]", touched, s.ID)
	}
	if _, err := m.Get(s.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after GC = %v, want ErrNotFound", err)
	}
}

func TestConnectResumesStoppedSandbox(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	clk := newFakeClock(now)
	m, rt, ap := newTestManagerWithPolicy(t, clk, time.Second, 10, time.Hour)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	initialPings := ap.pings
	initialInits := ap.inits

	if err := m.Pause(ctx, s.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	clk.Advance(2 * time.Second)
	m.EnforceTimeouts(ctx)
	if !rt.IsStopped(s.ID) {
		t.Fatal("precondition: sandbox should be stopped")
	}

	resumed, err := m.Connect(ctx, s.ID, 24*time.Hour)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if resumed.State != StateRunning {
		t.Errorf("State = %q, want running", resumed.State)
	}
	if resumed.PauseMode != "" {
		t.Errorf("PauseMode = %q, want empty", resumed.PauseMode)
	}
	if rt.IsStopped(s.ID) {
		t.Error("runtime still stopped after Connect")
	}
	if ap.pings <= initialPings {
		t.Errorf("agent.Ping not called on resume: %d -> %d", initialPings, ap.pings)
	}
	if ap.inits <= initialInits {
		t.Errorf("agent.InitAgent not called on resume: %d -> %d", initialInits, ap.inits)
	}
}

func TestPausePolicyReflectsOptions(t *testing.T) {
	clk := newFakeClock(time.Now())
	m, _, _ := newTestManagerWithPolicy(t, clk, 2*time.Hour, 7, 48*time.Hour)
	p := m.PausePolicy()
	if p.FreezeDuration != 2*time.Hour {
		t.Errorf("FreezeDuration = %v, want 2h", p.FreezeDuration)
	}
	if p.MaxFrozen != 7 {
		t.Errorf("MaxFrozen = %d, want 7", p.MaxFrozen)
	}
	if p.StoppedGCAfter != 48*time.Hour {
		t.Errorf("StoppedGCAfter = %v, want 48h", p.StoppedGCAfter)
	}
}

func TestPausePolicyDefaults(t *testing.T) {
	clk := newFakeClock(time.Now())
	m, _ := newTestManager(t, clk)
	p := m.PausePolicy()
	if p.FreezeDuration != DefaultFreezeDuration {
		t.Errorf("FreezeDuration = %v, want default", p.FreezeDuration)
	}
	if p.MaxFrozen != DefaultMaxFrozen {
		t.Errorf("MaxFrozen = %d, want default", p.MaxFrozen)
	}
	if p.StoppedGCAfter != DefaultStoppedGCAfter {
		t.Errorf("StoppedGCAfter = %v, want default", p.StoppedGCAfter)
	}
}

func TestCreateForwardsTemplateResources(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	resolver := &fakeResolver{
		resolutions: map[string]TemplateResolution{
			"heavy": {
				ImageTag: "edvabe/heavy:latest",
				CPUCount: 4,
				MemoryMB: 2048,
			},
		},
	}
	m, rt := newManagerWithResolver(t, clk, resolver)

	sbx, err := m.Create(context.Background(), CreateOptions{TemplateID: "heavy"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rt.CPUCount(sbx.ID) != 4 {
		t.Errorf("runtime CPU = %d, want 4", rt.CPUCount(sbx.ID))
	}
	if rt.MemoryMB(sbx.ID) != 2048 {
		t.Errorf("runtime mem = %d, want 2048", rt.MemoryMB(sbx.ID))
	}
	if sbx.CPUCount != 4 || sbx.MemoryMB != 2048 {
		t.Errorf("sandbox record cpu=%d mem=%d, want 4/2048", sbx.CPUCount, sbx.MemoryMB)
	}
}

func TestCreateOverrideBeatsTemplate(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	resolver := &fakeResolver{
		resolutions: map[string]TemplateResolution{
			"base": {ImageTag: "edvabe/base:latest", CPUCount: 2, MemoryMB: 1024},
		},
	}
	m, rt := newManagerWithResolver(t, clk, resolver)

	sbx, err := m.Create(context.Background(), CreateOptions{
		TemplateID: "base",
		CPUCount:   8,
		MemoryMB:   4096,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rt.CPUCount(sbx.ID) != 8 || rt.MemoryMB(sbx.ID) != 4096 {
		t.Errorf("override not applied: cpu=%d mem=%d", rt.CPUCount(sbx.ID), rt.MemoryMB(sbx.ID))
	}
}

func TestCreateNegativeOverrideMeansUnlimited(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	resolver := &fakeResolver{
		resolutions: map[string]TemplateResolution{
			"capped": {ImageTag: "edvabe/capped:latest", CPUCount: 2, MemoryMB: 512},
		},
	}
	m, rt := newManagerWithResolver(t, clk, resolver)

	sbx, err := m.Create(context.Background(), CreateOptions{
		TemplateID: "capped",
		CPUCount:   -1,
		MemoryMB:   -1,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rt.CPUCount(sbx.ID) != 0 || rt.MemoryMB(sbx.ID) != 0 {
		t.Errorf("negative override should zero limits: cpu=%d mem=%d", rt.CPUCount(sbx.ID), rt.MemoryMB(sbx.ID))
	}
}

func TestCreateNoResourcesMeansUnlimited(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, rt := newManagerWithResolver(t, clk, nil)

	sbx, err := m.Create(context.Background(), CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rt.CPUCount(sbx.ID) != 0 || rt.MemoryMB(sbx.ID) != 0 {
		t.Errorf("no limits requested: cpu=%d mem=%d, want 0/0", rt.CPUCount(sbx.ID), rt.MemoryMB(sbx.ID))
	}
	if sbx.CPUCount != 0 || sbx.MemoryMB != 0 {
		t.Errorf("sandbox record should carry zeros, got cpu=%d mem=%d", sbx.CPUCount, sbx.MemoryMB)
	}
}

func TestStopForcesRunningIntoStopped(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, rt := newTestManagerWithRuntime(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: time.Hour})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Stop(ctx, s.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	got, _ := m.Get(s.ID)
	if got.State != StatePaused || got.PauseMode != PauseStopped {
		t.Errorf("state=%q mode=%q, want paused/stopped", got.State, got.PauseMode)
	}
	if !rt.IsStopped(s.ID) {
		t.Error("runtime should be stopped")
	}
	if rt.IsPaused(s.ID) {
		t.Error("runtime should not be frozen")
	}
}

func TestStopDemotesFrozenToStopped(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, rt := newTestManagerWithRuntime(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: time.Hour})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Pause(ctx, s.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if err := m.Stop(ctx, s.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	got, _ := m.Get(s.ID)
	if got.PauseMode != PauseStopped {
		t.Errorf("PauseMode = %q, want %q", got.PauseMode, PauseStopped)
	}
	if !rt.IsStopped(s.ID) || rt.IsPaused(s.ID) {
		t.Errorf("runtime: IsStopped=%v IsPaused=%v, want true/false", rt.IsStopped(s.ID), rt.IsPaused(s.ID))
	}
}

func TestStopOnStoppedIsNoop(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, _ := newTestManagerWithRuntime(t, clk)
	ctx := context.Background()
	s, err := m.Create(ctx, CreateOptions{Timeout: time.Hour})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Stop(ctx, s.ID); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	pausedAt := s.PausedAt
	clk.Advance(5 * time.Minute)
	if err := m.Stop(ctx, s.ID); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	got, _ := m.Get(s.ID)
	if !got.PausedAt.Equal(pausedAt) {
		t.Errorf("PausedAt changed on no-op Stop: %v vs %v", got.PausedAt, pausedAt)
	}
}

func TestResumePreservesTTL(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, rt := newTestManagerWithRuntime(t, clk)
	ctx := context.Background()

	s, err := m.Create(ctx, CreateOptions{Timeout: time.Hour})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	originalExpiry := s.ExpiresAt
	if err := m.Pause(ctx, s.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	clk.Advance(10 * time.Minute)
	if err := m.Resume(ctx, s.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got, _ := m.Get(s.ID)
	if got.State != StateRunning {
		t.Errorf("State = %q, want running", got.State)
	}
	if !got.ExpiresAt.Equal(originalExpiry) {
		t.Errorf("ExpiresAt changed: %v vs %v (Resume should preserve TTL)", got.ExpiresAt, originalExpiry)
	}
	if rt.IsPaused(s.ID) {
		t.Error("runtime still paused after Resume")
	}
}

func TestResumeFromStoppedRerunsInitAgent(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, rt, ap := newTestManagerWithPolicy(t, clk, time.Hour, 10, time.Hour)
	ctx := context.Background()
	s, err := m.Create(ctx, CreateOptions{Timeout: time.Hour})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Stop(ctx, s.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	inits := ap.inits
	if err := m.Resume(ctx, s.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if ap.inits <= inits {
		t.Errorf("InitAgent not called on resume-from-stopped: %d -> %d", inits, ap.inits)
	}
	if rt.IsStopped(s.ID) {
		t.Error("runtime still stopped after Resume")
	}
}

func TestStopUnknown(t *testing.T) {
	m, _ := newTestManager(t, newFakeClock(time.Now()))
	if err := m.Stop(context.Background(), "isb_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Stop missing = %v, want ErrNotFound", err)
	}
}

func TestResumeUnknown(t *testing.T) {
	m, _ := newTestManager(t, newFakeClock(time.Now()))
	if err := m.Resume(context.Background(), "isb_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Resume missing = %v, want ErrNotFound", err)
	}
}

func TestResumeRunningIsNoop(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	m, _ := newTestManagerWithRuntime(t, clk)
	ctx := context.Background()
	s, err := m.Create(ctx, CreateOptions{Timeout: time.Hour})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Resume(ctx, s.ID); err != nil {
		t.Errorf("Resume on running: %v", err)
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
