package docker

import (
	"testing"
)

func TestDiscoverHostHonorsDockerHostEnv(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://example.invalid:2375")
	host, err := DiscoverHost()
	if err != nil {
		t.Fatalf("DiscoverHost: %v", err)
	}
	if host != "tcp://example.invalid:2375" {
		t.Errorf("DiscoverHost = %q, want tcp://example.invalid:2375", host)
	}
}

func TestPauseUnpauseCommitArgValidation(t *testing.T) {
	// Empty-arg validation runs before any Docker client call, so
	// these assertions exercise the validation path without needing a
	// daemon connection.
	r := &Runtime{}
	ctx := t.Context()
	if err := r.Pause(ctx, ""); err == nil {
		t.Error("Pause(\"\") should be rejected")
	}
	if err := r.Unpause(ctx, ""); err == nil {
		t.Error("Unpause(\"\") should be rejected")
	}
	if err := r.Commit(ctx, "", "edvabe/snap:v1"); err == nil {
		t.Error("Commit(\"\", tag) should be rejected")
	}
	if err := r.Commit(ctx, "isb_x", ""); err == nil {
		t.Error("Commit(id, \"\") should be rejected")
	}
}
