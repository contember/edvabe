package docker

import (
	"context"
	"fmt"
	"io"

	"github.com/moby/go-archive"
	"github.com/moby/moby/client"

	"github.com/contember/edvabe/internal/runtime"
)

// BuildImage builds an image from a filesystem build context. Used by
// the upstream AgentProvider (and later by user template builds in
// Phase 4). Consumes the full build output stream before returning so
// the image is ready when BuildImage returns.
func (r *Runtime) BuildImage(ctx context.Context, req runtime.BuildRequest) error {
	if req.Tag == "" {
		return fmt.Errorf("docker runtime: BuildImage: Tag is required")
	}
	if req.ContextDir == "" {
		return fmt.Errorf("docker runtime: BuildImage: ContextDir is required")
	}

	tarStream, err := archive.TarWithOptions(req.ContextDir, &archive.TarOptions{})
	if err != nil {
		return fmt.Errorf("docker runtime: tar build context %q: %w", req.ContextDir, err)
	}
	defer tarStream.Close()

	buildArgs := make(map[string]*string, len(req.BuildArgs))
	for k, v := range req.BuildArgs {
		val := v
		buildArgs[k] = &val
	}

	opts := client.ImageBuildOptions{
		Tags:       []string{req.Tag},
		Dockerfile: req.Dockerfile,
		BuildArgs:  buildArgs,
		Labels:     req.Labels,
		Remove:     true,
	}

	resp, err := r.cli.ImageBuild(ctx, tarStream, opts)
	if err != nil {
		return fmt.Errorf("docker runtime: image build %q: %w", req.Tag, err)
	}
	defer resp.Body.Close()

	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return fmt.Errorf("docker runtime: read build output %q: %w", req.Tag, err)
	}
	return nil
}

// Commit snapshots a sandbox filesystem as a new image. Phase 4. Stubbed
// out in Phase 1.
func (r *Runtime) Commit(ctx context.Context, sandboxID, imageTag string) error {
	return fmt.Errorf("docker runtime: Commit not implemented (phase 4)")
}
