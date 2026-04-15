// Package upstream is the Phase 1 AgentProvider. It builds
// `edvabe/base:latest` as a multi-stage Docker image: stage 1 compiles
// upstream envd from a pinned e2b-dev/infra commit; stage 2 starts from
// the pinned e2bdev/base runtime image and copies the envd binary in.
// See docs/07-open-questions.md#Q2 for why we layer envd on top of
// e2bdev/base instead of consuming either side alone.
package upstream

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/contember/edvabe/assets"
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
// bump date in a comment above. Also update the literal digest in
// assets/Dockerfile.base — Docker build-args can't be used in FROM
// reference digests.
const BaseImageDigest = "sha256:11349f027b11281645fd8b7874e94053681a0d374508067c16bf15b00e1161b2"

// EnvdSourceSHA pins the e2b-dev/infra commit from which envd is built
// at `docker build` time. We build from source because neither
// e2bdev/base nor any other public E2B Docker image ships envd — their
// orchestrator injects it outside what ends up on Docker Hub.
//
// Current pin: HEAD of tag `2026.15` (2026-04-09), resolved 2026-04-15
// via `gh api repos/e2b-dev/infra/git/refs/tags/2026.15`. Bump by
// picking a newer tag's commit SHA and updating this const; no
// Dockerfile edits required (passed as --build-arg).
const EnvdSourceSHA = "d9063bd8cc70b5ce653e9f7cd4ede0f1e3de0fef"

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

// EnsureBaseImage runs a multi-stage `docker build` that compiles envd
// from source (pinned via EnvdSourceSHA) and layers it onto the pinned
// e2bdev/base image, producing `tag` — typically "edvabe/base:latest".
//
// The embedded Dockerfile is piped via stdin with an empty build
// context (`docker build -`). Stdout/stderr are forwarded so the user
// sees progress during what can be a multi-minute first build.
//
// Idempotent: Docker's layer cache makes re-runs fast once the pinned
// commit has been built once.
func EnsureBaseImage(ctx context.Context, tag string) error {
	if tag == "" {
		return fmt.Errorf("EnsureBaseImage: tag is required")
	}
	cmd := exec.CommandContext(ctx, "docker", "build",
		"--build-arg", "ENVD_SHA="+EnvdSourceSHA,
		"-t", tag,
		"-")
	cmd.Stdin = bytes.NewReader(assets.DockerfileBase)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build %s: %w", tag, err)
	}
	return nil
}
