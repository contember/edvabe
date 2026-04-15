package template

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	t time.Time
}

func (c *fakeClock) Now() time.Time { return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}

func newStore(t *testing.T) (*Store, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)}
	s, err := NewStore(Options{Clock: clock})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s, clock
}

func TestStoreCreateAndGet(t *testing.T) {
	s, _ := newStore(t)
	tpl, err := s.Create(CreateOptions{Name: "probe", MemoryMB: 1024})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tpl.ID == "" {
		t.Fatal("expected non-empty template ID")
	}
	if tpl.Alias != "probe" {
		t.Fatalf("expected alias to default to name, got %q", tpl.Alias)
	}
	got, err := s.Get(tpl.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "probe" || got.MemoryMB != 1024 {
		t.Fatalf("unexpected round-trip: %+v", got)
	}
	// Mutating the returned copy must not leak into the store.
	got.Name = "mutated"
	fresh, _ := s.Get(tpl.ID)
	if fresh.Name != "probe" {
		t.Fatalf("store leaked internal state: %+v", fresh)
	}
}

func TestStoreResolveAlias(t *testing.T) {
	s, _ := newStore(t)
	tpl, err := s.Create(CreateOptions{Name: "chrome", Alias: "webmaster-chrome"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ResolveAlias("webmaster-chrome")
	if err != nil {
		t.Fatalf("ResolveAlias: %v", err)
	}
	if got.ID != tpl.ID {
		t.Fatalf("alias resolution mismatch")
	}
	if _, err := s.ResolveAlias("does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreResolveNameOrID(t *testing.T) {
	s, _ := newStore(t)
	tpl, err := s.Create(CreateOptions{Name: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ResolveNameOrID(tpl.ID); err != nil || got.ID != tpl.ID {
		t.Fatalf("lookup by ID failed: %v %+v", err, got)
	}
	if got, err := s.ResolveNameOrID("alpha"); err != nil || got.ID != tpl.ID {
		t.Fatalf("lookup by alias failed: %v %+v", err, got)
	}
	if _, err := s.ResolveNameOrID(""); !errors.Is(err, ErrNotFound) {
		t.Fatal("empty lookup should return ErrNotFound")
	}
}

func TestStoreAliasConflict(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.Create(CreateOptions{Name: "dup"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(CreateOptions{Name: "dup"}); !errors.Is(err, ErrAliasTaken) {
		t.Fatalf("expected ErrAliasTaken, got %v", err)
	}
}

func TestStoreListOrdering(t *testing.T) {
	s, clock := newStore(t)
	_, _ = s.Create(CreateOptions{Name: "first"})
	clock.advance(time.Second)
	_, _ = s.Create(CreateOptions{Name: "second"})
	clock.advance(time.Second)
	_, _ = s.Create(CreateOptions{Name: "third"})

	list := s.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 templates, got %d", len(list))
	}
	if list[0].Name != "first" || list[1].Name != "second" || list[2].Name != "third" {
		t.Fatalf("unexpected order: %+v", list)
	}
}

func TestStoreDelete(t *testing.T) {
	s, _ := newStore(t)
	tpl, _ := s.Create(CreateOptions{Name: "gone"})
	if err := s.Delete(tpl.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(tpl.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("template should be gone")
	}
	if _, err := s.ResolveAlias("gone"); !errors.Is(err, ErrNotFound) {
		t.Fatal("alias should be released on delete")
	}
	// Creating a new template with the freed alias should succeed.
	if _, err := s.Create(CreateOptions{Name: "gone"}); err != nil {
		t.Fatalf("reusing freed alias failed: %v", err)
	}
}

func TestStoreDeleteNotFound(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Delete("tpl_does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreUpdateMetaReleasesOldAlias(t *testing.T) {
	s, _ := newStore(t)
	tpl, _ := s.Create(CreateOptions{Name: "old"})
	if _, err := s.UpdateMeta(tpl.ID, func(t *Template) { t.Alias = "new" }); err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}
	// Old alias freed.
	if _, err := s.Create(CreateOptions{Name: "old"}); err != nil {
		t.Fatalf("old alias should have been released: %v", err)
	}
	// New alias held.
	if _, err := s.ResolveAlias("new"); err != nil {
		t.Fatalf("new alias not registered: %v", err)
	}
}

func TestStoreUpdateMetaAliasConflict(t *testing.T) {
	s, _ := newStore(t)
	aTpl, _ := s.Create(CreateOptions{Name: "a"})
	_, _ = s.Create(CreateOptions{Name: "b"})
	_, err := s.UpdateMeta(aTpl.ID, func(t *Template) { t.Alias = "b" })
	if !errors.Is(err, ErrAliasTaken) {
		t.Fatalf("expected ErrAliasTaken, got %v", err)
	}
	// Original alias must still point at aTpl.
	got, _ := s.ResolveAlias("a")
	if got == nil || got.ID != aTpl.ID {
		t.Fatalf("original alias not preserved on conflict")
	}
}

func TestStoreAppendAndUpdateBuild(t *testing.T) {
	s, clock := newStore(t)
	tpl, _ := s.Create(CreateOptions{Name: "built"})
	b, err := s.AppendBuild(tpl.ID, Build{ID: "bld_1", Status: BuildStatusWaiting, StartedAt: clock.Now()})
	if err != nil {
		t.Fatalf("AppendBuild: %v", err)
	}
	if b.ID != "bld_1" || b.Status != BuildStatusWaiting {
		t.Fatalf("unexpected build: %+v", b)
	}

	if err := s.UpdateBuild(tpl.ID, "bld_1", func(b *Build) {
		b.Status = BuildStatusReady
	}); err != nil {
		t.Fatalf("UpdateBuild: %v", err)
	}
	fresh, _ := s.Get(tpl.ID)
	if len(fresh.Builds) != 1 || fresh.Builds[0].Status != BuildStatusReady {
		t.Fatalf("build update did not stick: %+v", fresh.Builds)
	}

	if lr := fresh.LatestReady(); lr == nil || lr.ID != "bld_1" {
		t.Fatalf("LatestReady mismatch: %+v", lr)
	}
}

func TestStorePersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "templates.json")
	clock := &fakeClock{t: time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)}

	s1, err := NewStore(Options{Path: path, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	tpl, _ := s1.Create(CreateOptions{Name: "persisted", Tags: []string{"v1", "stable"}, MemoryMB: 2048})
	_, _ = s1.AppendBuild(tpl.ID, Build{
		ID:        "bld_a",
		Status:    BuildStatusReady,
		StartedAt: clock.Now(),
	})

	s2, err := NewStore(Options{Path: path, Clock: clock})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := s2.Get(tpl.ID)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.Name != "persisted" || got.MemoryMB != 2048 {
		t.Fatalf("fields lost: %+v", got)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "v1" {
		t.Fatalf("tags lost: %+v", got.Tags)
	}
	if len(got.Builds) != 1 || got.Builds[0].ID != "bld_a" {
		t.Fatalf("builds lost: %+v", got.Builds)
	}
	// Alias lookup must work through the reload.
	if _, err := s2.ResolveAlias("persisted"); err != nil {
		t.Fatalf("alias lost after reload: %v", err)
	}
}

func TestStorePersistenceMissingFile(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(Options{Path: filepath.Join(dir, "nope.json")})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if len(s.List()) != 0 {
		t.Fatal("missing file should yield empty store")
	}
}

func TestStoreConcurrentReads(t *testing.T) {
	s, _ := newStore(t)
	tpl, _ := s.Create(CreateOptions{Name: "hot"})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if got, err := s.Get(tpl.ID); err != nil || got.ID != tpl.ID {
					t.Errorf("concurrent Get failed: %v %+v", err, got)
					return
				}
			}
		}()
	}
	wg.Wait()
}
