package docker

import (
	"context"
	"fmt"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/contember/edvabe/internal/runtime"
)

// Create creates + starts a container from req.Image, names it after
// req.SandboxID, and resolves its bridge IP for the reverse proxy. On
// any error after ContainerCreate, the partial container is force-removed
// so the caller doesn't have to clean up.
func (r *Runtime) Create(ctx context.Context, req runtime.CreateRequest) (*runtime.SandboxHandle, error) {
	if req.SandboxID == "" {
		return nil, fmt.Errorf("docker runtime: Create: SandboxID is required")
	}
	if req.Image == "" {
		return nil, fmt.Errorf("docker runtime: Create: Image is required")
	}

	port := req.AgentPort
	if port == 0 {
		port = defaultAgentPort
	}

	labels := map[string]string{
		LabelSandboxID: req.SandboxID,
		LabelManaged:   "true",
	}
	for k, v := range req.Metadata {
		labels[LabelMetaPrefix+k] = v
	}

	var mounts []mount.Mount
	for hostPath, ctrPath := range req.BindMounts {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: hostPath,
			Target: ctrPath,
		})
	}

	cfg := &container.Config{
		Image:  req.Image,
		Env:    envMapToSlice(req.EnvVars),
		Labels: labels,
	}
	hostCfg := &container.HostConfig{Mounts: mounts}

	created, err := r.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     cfg,
		HostConfig: hostCfg,
		Name:       req.SandboxID,
	})
	if err != nil {
		return nil, fmt.Errorf("docker runtime: container create %q: %w", req.SandboxID, err)
	}
	containerID := created.ID

	if _, err := r.cli.ContainerStart(ctx, containerID, client.ContainerStartOptions{}); err != nil {
		r.forceRemove(ctx, containerID)
		return nil, fmt.Errorf("docker runtime: container start %q: %w", req.SandboxID, err)
	}

	inspect, err := r.cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		r.forceRemove(ctx, containerID)
		return nil, fmt.Errorf("docker runtime: container inspect %q: %w", req.SandboxID, err)
	}

	host, err := extractBridgeIP(inspect.Container)
	if err != nil {
		r.forceRemove(ctx, containerID)
		return nil, fmt.Errorf("docker runtime: resolve bridge IP for %q: %w", req.SandboxID, err)
	}

	r.mu.Lock()
	r.endpoints[req.SandboxID] = endpoint{host: host, port: port}
	r.mu.Unlock()

	return &runtime.SandboxHandle{
		ContainerID: containerID,
		AgentHost:   host,
		AgentPort:   port,
		CreatedAt:   time.Now(),
	}, nil
}

func envMapToSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// forceRemove is a best-effort cleanup used when a multi-step Create
// fails partway through. Errors are swallowed intentionally — the
// caller is already returning a more specific error.
func (r *Runtime) forceRemove(ctx context.Context, containerID string) {
	_, _ = r.cli.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{Force: true})
}
