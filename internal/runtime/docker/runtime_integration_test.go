//go:build integration

// Integration tests for the Docker runtime. Run with:
//
//	go test -tags=integration ./internal/runtime/docker/...
//
// Requires a reachable Docker daemon and `edvabe/base:latest` on the
// host (build with `go run ./cmd/edvabe build-image`). The base image
// is used because it has a long-running default CMD (envd); most
// public test images like alpine:latest exit immediately, which
// releases the container's bridge IP and breaks the inspect step.

package docker

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/contember/edvabe/internal/runtime"
)

const testImage = "edvabe/base:latest"

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	r, err := New()
	if err != nil {
		t.Skipf("Docker not available: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := r.cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("Docker ping failed: %v", err)
	}

	if _, err := r.cli.ImageInspect(ctx, testImage); err != nil {
		t.Skipf("%s not present — run `go run ./cmd/edvabe build-image` first", testImage)
	}
	return r
}

func uniqueSandboxID(t *testing.T) string {
	return "isb_test_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "_" + t.Name()
}

func TestDockerRuntimeCreateInspectDestroy(t *testing.T) {
	r := newTestRuntime(t)
	ctx := context.Background()

	sid := uniqueSandboxID(t)
	t.Cleanup(func() { _ = r.Destroy(ctx, sid) })

	h, err := r.Create(ctx, runtime.CreateRequest{
		SandboxID: sid,
		Image:     testImage,
		EnvVars:   map[string]string{"EDVABE_TEST": "1"},
		Metadata:  map[string]string{"owner": "runtime-test"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if h.ContainerID == "" {
		t.Error("handle.ContainerID is empty")
	}
	if h.AgentPort != defaultAgentPort {
		t.Errorf("handle.AgentPort = %d, want %d", h.AgentPort, defaultAgentPort)
	}
	if ip := net.ParseIP(h.AgentHost); ip == nil {
		t.Errorf("handle.AgentHost %q is not a valid IP", h.AgentHost)
	}
	if h.CreatedAt.IsZero() {
		t.Error("handle.CreatedAt is zero")
	}

	host, port, err := r.AgentEndpoint(sid)
	if err != nil {
		t.Fatalf("AgentEndpoint: %v", err)
	}
	if host != h.AgentHost || port != h.AgentPort {
		t.Errorf("AgentEndpoint = %s:%d, want %s:%d", host, port, h.AgentHost, h.AgentPort)
	}

	inspect, err := r.cli.ContainerInspect(ctx, sid, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("ContainerInspect: %v", err)
	}
	if !inspect.Container.State.Running {
		t.Errorf("container state = %q, want running", inspect.Container.State.Status)
	}
	if got := inspect.Container.Config.Labels[LabelSandboxID]; got != sid {
		t.Errorf("label %s = %q, want %q", LabelSandboxID, got, sid)
	}
	if got := inspect.Container.Config.Labels[runtime.LabelMetaPrefix+"owner"]; got != "runtime-test" {
		t.Errorf("metadata label missing: got %q", got)
	}

	stats, err := r.Stats(ctx, sid)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats == nil {
		t.Fatal("Stats returned nil")
	}

	if err := r.Destroy(ctx, sid); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	if _, err := r.cli.ContainerInspect(ctx, sid, client.ContainerInspectOptions{}); err == nil {
		t.Error("ContainerInspect should fail after Destroy")
	}
	if err := r.Destroy(ctx, sid); err == nil {
		t.Error("second Destroy should fail")
	}
}

func TestDockerRuntimePauseUnpauseCommit(t *testing.T) {
	r := newTestRuntime(t)
	ctx := context.Background()

	sid := uniqueSandboxID(t)
	t.Cleanup(func() { _ = r.Destroy(ctx, sid) })

	if _, err := r.Create(ctx, runtime.CreateRequest{
		SandboxID: sid,
		Image:     testImage,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := r.Pause(ctx, sid); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	inspect, err := r.cli.ContainerInspect(ctx, sid, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("ContainerInspect after Pause: %v", err)
	}
	if inspect.Container.State.Status != "paused" {
		t.Errorf("state after Pause = %q, want paused", inspect.Container.State.Status)
	}

	if err := r.Unpause(ctx, sid); err != nil {
		t.Fatalf("Unpause: %v", err)
	}
	inspect, err = r.cli.ContainerInspect(ctx, sid, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("ContainerInspect after Unpause: %v", err)
	}
	if !inspect.Container.State.Running {
		t.Errorf("state after Unpause = %q, want running", inspect.Container.State.Status)
	}

	snapshotTag := "edvabe/snap:" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Cleanup(func() {
		_, _ = r.cli.ImageRemove(ctx, snapshotTag, client.ImageRemoveOptions{Force: true})
	})
	if err := r.Commit(ctx, sid, snapshotTag); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, err := r.cli.ImageInspect(ctx, snapshotTag); err != nil {
		t.Fatalf("ImageInspect %s: %v", snapshotTag, err)
	}
}

func TestDockerRuntimeCreateAppliesResourceLimits(t *testing.T) {
	r := newTestRuntime(t)
	ctx := context.Background()

	sid := uniqueSandboxID(t)
	t.Cleanup(func() { _ = r.Destroy(ctx, sid) })

	if _, err := r.Create(ctx, runtime.CreateRequest{
		SandboxID: sid,
		Image:     testImage,
		CPUCount:  2,
		MemoryMB:  256,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	inspect, err := r.cli.ContainerInspect(ctx, sid, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("ContainerInspect: %v", err)
	}
	wantCPU := int64(2) * 1_000_000_000
	if inspect.Container.HostConfig.NanoCPUs != wantCPU {
		t.Errorf("NanoCPUs = %d, want %d", inspect.Container.HostConfig.NanoCPUs, wantCPU)
	}
	wantMem := int64(256) * 1024 * 1024
	if inspect.Container.HostConfig.Memory != wantMem {
		t.Errorf("Memory = %d, want %d", inspect.Container.HostConfig.Memory, wantMem)
	}
}

func TestDockerRuntimeCreateSkipsZeroResourceLimits(t *testing.T) {
	r := newTestRuntime(t)
	ctx := context.Background()

	sid := uniqueSandboxID(t)
	t.Cleanup(func() { _ = r.Destroy(ctx, sid) })

	if _, err := r.Create(ctx, runtime.CreateRequest{
		SandboxID: sid,
		Image:     testImage,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	inspect, err := r.cli.ContainerInspect(ctx, sid, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("ContainerInspect: %v", err)
	}
	if inspect.Container.HostConfig.NanoCPUs != 0 {
		t.Errorf("NanoCPUs = %d, want 0 (unlimited)", inspect.Container.HostConfig.NanoCPUs)
	}
	if inspect.Container.HostConfig.Memory != 0 {
		t.Errorf("Memory = %d, want 0 (unlimited)", inspect.Container.HostConfig.Memory)
	}
}

func TestDockerRuntimeCreateRelaxesSeccompAndAppArmor(t *testing.T) {
	r := newTestRuntime(t)
	ctx := context.Background()

	sid := uniqueSandboxID(t)
	t.Cleanup(func() { _ = r.Destroy(ctx, sid) })

	if _, err := r.Create(ctx, runtime.CreateRequest{
		SandboxID: sid,
		Image:     testImage,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	inspect, err := r.cli.ContainerInspect(ctx, sid, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("ContainerInspect: %v", err)
	}

	got := map[string]bool{}
	for _, opt := range inspect.Container.HostConfig.SecurityOpt {
		got[opt] = true
	}
	for _, want := range []string{"seccomp=unconfined", "apparmor=unconfined"} {
		if !got[want] {
			t.Errorf("SecurityOpt missing %q; got %v", want, inspect.Container.HostConfig.SecurityOpt)
		}
	}
}

func TestDockerRuntimeListManagedReturnsLabeledContainers(t *testing.T) {
	r := newTestRuntime(t)
	ctx := context.Background()

	sid := uniqueSandboxID(t)
	t.Cleanup(func() { _ = r.Destroy(ctx, sid) })

	if _, err := r.Create(ctx, runtime.CreateRequest{
		SandboxID: sid,
		Image:     testImage,
		Metadata:  map[string]string{"owner": "alice"},
		Labels:    map[string]string{"edvabe.sandbox.template.id": "base"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	managed, err := r.ListManaged(ctx)
	if err != nil {
		t.Fatalf("ListManaged: %v", err)
	}

	var found *runtime.ManagedContainer
	for i := range managed {
		if managed[i].SandboxID == sid {
			found = &managed[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("ListManaged did not return sandbox %q; got %d entries", sid, len(managed))
	}
	if found.State != runtime.ContainerStateRunning {
		t.Errorf("State = %q, want running", found.State)
	}
	if found.Labels["edvabe.sandbox.template.id"] != "base" {
		t.Errorf("template-id label not recovered: %v", found.Labels)
	}
	if found.Labels["edvabe.meta.owner"] != "alice" {
		t.Errorf("metadata label not recovered: %v", found.Labels)
	}
	if found.AgentHost == "" {
		t.Errorf("AgentHost empty for running container")
	}
	if found.AgentPort != 49983 {
		t.Errorf("AgentPort = %d, want 49983", found.AgentPort)
	}
}

func TestDockerRuntimeCreateRequiresID(t *testing.T) {
	r := newTestRuntime(t)
	ctx := context.Background()
	if _, err := r.Create(ctx, runtime.CreateRequest{Image: testImage}); err == nil {
		t.Error("Create with empty SandboxID should fail")
	}
}

func TestDockerRuntimeCreateRequiresImage(t *testing.T) {
	r := newTestRuntime(t)
	ctx := context.Background()
	if _, err := r.Create(ctx, runtime.CreateRequest{SandboxID: "isb_no_image"}); err == nil {
		t.Error("Create with empty Image should fail")
	}
}
