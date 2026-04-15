package template

import (
	"errors"
	"testing"
	"time"

	"github.com/contember/edvabe/internal/sandbox"
)

func TestResolverFallsBackToBase(t *testing.T) {
	s, _ := NewStore(Options{})
	r := NewSandboxResolver(s)
	if _, err := r.Resolve("base"); !errors.Is(err, sandbox.ErrTemplateNotFound) {
		t.Fatalf("expected ErrTemplateNotFound, got %v", err)
	}
}

func TestResolverFindsByID(t *testing.T) {
	s, _ := NewStore(Options{})
	tpl, _ := s.Create(CreateOptions{Name: "probe"})
	_, _ = s.UpdateMeta(tpl.ID, func(t *Template) {
		t.StartCmd = "sleep infinity"
		t.ReadyCmd = "true"
		t.ImageTag = "edvabe/user-" + tpl.ID + ":latest"
	})
	_, _ = s.AppendBuild(tpl.ID, Build{ID: "bld_1", Status: BuildStatusReady, StartedAt: time.Now()})

	r := NewSandboxResolver(s)
	got, err := r.Resolve(tpl.ID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ImageTag != "edvabe/user-"+tpl.ID+":latest" {
		t.Fatalf("unexpected image: %s", got.ImageTag)
	}
	if got.StartCmd != "sleep infinity" || got.ReadyCmd != "true" {
		t.Fatalf("start/ready not propagated: %+v", got)
	}
}

func TestResolverFindsByAlias(t *testing.T) {
	s, _ := NewStore(Options{})
	tpl, _ := s.Create(CreateOptions{Name: "webmaster-chrome"})
	_, _ = s.UpdateMeta(tpl.ID, func(t *Template) {
		t.ImageTag = "edvabe/user-" + tpl.ID + ":latest"
	})

	r := NewSandboxResolver(s)
	got, err := r.Resolve("webmaster-chrome")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ImageTag == "" {
		t.Fatal("alias resolution returned empty image tag")
	}
}

func TestResolverEmptyImageTagSignalsFallback(t *testing.T) {
	// Template exists but has no imageTag yet (no build has started).
	// The resolver should still return a (successful) resolution so
	// the sandbox manager can apply its base-image fallback for the
	// missing image.
	s, _ := NewStore(Options{})
	tpl, _ := s.Create(CreateOptions{Name: "pending"})

	r := NewSandboxResolver(s)
	got, err := r.Resolve(tpl.ID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ImageTag != "" {
		t.Fatalf("expected empty ImageTag to trigger fallback, got %q", got.ImageTag)
	}
}
