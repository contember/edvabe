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

func TestPauseUnpauseCommitStubs(t *testing.T) {
	// Stubs return a not-implemented error without touching the Docker
	// daemon. Construct a bare Runtime so the test doesn't need Docker.
	r := &Runtime{}
	ctx := t.Context()
	if err := r.Pause(ctx, "isb_x"); err == nil {
		t.Error("Pause stub should return an error")
	}
	if err := r.Unpause(ctx, "isb_x"); err == nil {
		t.Error("Unpause stub should return an error")
	}
	if err := r.Commit(ctx, "isb_x", "edvabe/snap:v1"); err == nil {
		t.Error("Commit stub should return an error")
	}
}
