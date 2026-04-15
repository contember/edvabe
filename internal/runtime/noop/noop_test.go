package noop

import (
	"context"
	"testing"

	"github.com/contember/edvabe/internal/runtime"
)

func TestCreateInspectDestroy(t *testing.T) {
	ctx := context.Background()
	r := New()

	h, err := r.Create(ctx, runtime.CreateRequest{
		SandboxID: "isb_test1",
		Image:     "edvabe/base:latest",
		AgentPort: 49983,
		EnvVars:   map[string]string{"FOO": "bar"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if h == nil {
		t.Fatal("Create returned nil handle")
	}
	if h.ContainerID == "" {
		t.Error("handle.ContainerID should not be empty")
	}
	if h.AgentHost == "" {
		t.Error("handle.AgentHost should not be empty")
	}
	if h.AgentPort != 49983 {
		t.Errorf("handle.AgentPort = %d, want 49983", h.AgentPort)
	}
	if h.CreatedAt.IsZero() {
		t.Error("handle.CreatedAt should be set")
	}

	host, port, err := r.AgentEndpoint("isb_test1")
	if err != nil {
		t.Fatalf("AgentEndpoint: %v", err)
	}
	if host != h.AgentHost || port != h.AgentPort {
		t.Errorf("AgentEndpoint = %s:%d, want %s:%d", host, port, h.AgentHost, h.AgentPort)
	}

	stats, err := r.Stats(ctx, "isb_test1")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats == nil {
		t.Error("Stats should return non-nil")
	}

	if err := r.Destroy(ctx, "isb_test1"); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	if _, _, err := r.AgentEndpoint("isb_test1"); err == nil {
		t.Error("AgentEndpoint should fail after Destroy")
	}
	if err := r.Destroy(ctx, "isb_test1"); err == nil {
		t.Error("second Destroy should fail")
	}
}

func TestCreateRequiresID(t *testing.T) {
	r := New()
	if _, err := r.Create(context.Background(), runtime.CreateRequest{}); err == nil {
		t.Error("Create with empty SandboxID should fail")
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	ctx := context.Background()
	r := New()
	if _, err := r.Create(ctx, runtime.CreateRequest{SandboxID: "dup"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if _, err := r.Create(ctx, runtime.CreateRequest{SandboxID: "dup"}); err == nil {
		t.Error("duplicate Create should fail")
	}
}

func TestPauseUnpause(t *testing.T) {
	ctx := context.Background()
	r := New()
	if _, err := r.Create(ctx, runtime.CreateRequest{SandboxID: "isb_p"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.IsPaused("isb_p") {
		t.Error("new sandbox should not be paused")
	}
	if err := r.Pause(ctx, "isb_p"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if !r.IsPaused("isb_p") {
		t.Error("sandbox should be paused after Pause")
	}
	if err := r.Unpause(ctx, "isb_p"); err != nil {
		t.Fatalf("Unpause: %v", err)
	}
	if r.IsPaused("isb_p") {
		t.Error("sandbox should not be paused after Unpause")
	}
	if err := r.Pause(ctx, "missing"); err == nil {
		t.Error("Pause on missing sandbox should fail")
	}
}

func TestCommitAndBuildImage(t *testing.T) {
	ctx := context.Background()
	r := New()
	if _, err := r.Create(ctx, runtime.CreateRequest{SandboxID: "isb_c"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.Commit(ctx, "isb_c", "edvabe/snap:v1"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !r.HasImage("edvabe/snap:v1") {
		t.Error("HasImage should be true after Commit")
	}
	if err := r.BuildImage(ctx, runtime.BuildRequest{Tag: "edvabe/built:v1", ContextDir: "/tmp"}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	if !r.HasImage("edvabe/built:v1") {
		t.Error("HasImage should be true after BuildImage")
	}
	if err := r.BuildImage(ctx, runtime.BuildRequest{ContextDir: "/tmp"}); err == nil {
		t.Error("BuildImage without Tag should fail")
	}
}

func TestName(t *testing.T) {
	if got := New().Name(); got != "noop" {
		t.Errorf("Name() = %q, want \"noop\"", got)
	}
}
