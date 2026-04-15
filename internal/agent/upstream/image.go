// Package upstream is the Phase 1 AgentProvider — it reuses the upstream
// E2B agent by consuming the public e2bdev/base Docker image unchanged.
// See docs/07-open-questions.md#Q2 for why we pull their image instead of
// fetching raw envd binaries.
package upstream

import (
	"context"
	"fmt"
	"os/exec"
)

// DefaultEnvdVersion is the value edvabe reports as `envdVersion` in
// Sandbox responses. Pinned per CLAUDE.md golden rule #3 — unlocking the
// newest code path in every SDK branch depends on this exact string.
// Not tied to the BaseImageDigest below.
const DefaultEnvdVersion = "0.5.7"

// BaseImageRepo is the public Docker Hub repository E2B publishes their
// sandbox base image to. Multi-arch (amd64 + arm64).
const BaseImageRepo = "docker.io/e2bdev/base"

// BaseImageDigest pins a specific OCI image index digest of
// e2bdev/base:latest. This is NOT a per-arch manifest — Docker resolves
// the right arch for the host when pulling.
//
// Verified 2026-04-15 via:
//
//	curl -sSI -H "Authorization: Bearer <token>" \
//	    -H "Accept: application/vnd.oci.image.index.v1+json" \
//	    https://registry-1.docker.io/v2/e2bdev/base/manifests/latest
//
// To bump: re-run the HEAD request and replace this value. Record the
// bump date in a comment above.
const BaseImageDigest = "sha256:11349f027b11281645fd8b7874e94053681a0d374508067c16bf15b00e1161b2"

// BaseImageRef returns the fully-qualified digest-pinned reference used
// when pulling or tagging the upstream base image.
func BaseImageRef() string {
	return BaseImageRepo + "@" + BaseImageDigest
}

// PullBase ensures the pinned e2bdev/base image is present on the local
// Docker daemon. Shells out to `docker pull`; the Docker SDK will
// replace this in task 7 when the runtime package needs it for more
// than one operation.
//
// Idempotent: `docker pull` by digest is a no-op once the image is
// already present in the local store.
func PullBase(ctx context.Context) error {
	ref := BaseImageRef()
	cmd := exec.CommandContext(ctx, "docker", "pull", ref)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker pull %s: %w\n%s", ref, err, out)
	}
	return nil
}

// EnsureBaseImage pulls the pinned upstream image (if missing) and tags
// it as `tag` — typically "edvabe/base:latest". Idempotent: safe to
// call on every edvabe serve boot and re-running always re-points the
// tag at the currently pinned digest.
func EnsureBaseImage(ctx context.Context, tag string) error {
	if tag == "" {
		return fmt.Errorf("EnsureBaseImage: tag is required")
	}
	if err := PullBase(ctx); err != nil {
		return err
	}
	ref := BaseImageRef()
	cmd := exec.CommandContext(ctx, "docker", "tag", ref, tag)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker tag %s %s: %w\n%s", ref, tag, err, out)
	}
	return nil
}
