package upstream

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/contember/edvabe/assets"
)

// EnvdSourceTag is the image tag the template builder references in
// its generated COPY --from lines. Keep in sync with
// internal/template/builder/translate.go's EnvdSourceImage constant.
const EnvdSourceTag = "edvabe/envd-source:latest"

// EnsureEnvdSource runs `docker build` for the minimal scratch image
// that holds the envd binary and the edvabe-init wrapper. Unlike
// EnsureBaseImage, the build context carries two files — the
// Dockerfile plus edvabe-init.sh — so we ship them over stdin as a
// tar stream and select the Dockerfile with `-f`.
//
// Idempotent: Docker's layer cache makes re-runs fast, and the
// envd-builder stage is shared with EnsureBaseImage when both images
// are built from the same process.
func EnsureEnvdSource(ctx context.Context, tag string) error {
	if tag == "" {
		return fmt.Errorf("EnsureEnvdSource: tag is required")
	}

	tarBytes, err := envdSourceBuildContext()
	if err != nil {
		return fmt.Errorf("envd-source build context: %w", err)
	}

	cmd := exec.CommandContext(ctx, "docker", "build",
		"--build-arg", "ENVD_SHA="+EnvdSourceSHA,
		"-f", "Dockerfile.envd-source",
		"-t", tag,
		"-")
	cmd.Stdin = bytes.NewReader(tarBytes)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build %s: %w", tag, err)
	}
	return nil
}

// envdSourceBuildContext returns a tar-archive build context holding
// the Dockerfile and the edvabe-init.sh wrapper. `docker build -`
// accepts either a raw Dockerfile or a tar on stdin; we use the latter
// so the Dockerfile can COPY edvabe-init.sh from the context.
func envdSourceBuildContext() ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()
	entries := []struct {
		name string
		mode int64
		body []byte
	}{
		{"Dockerfile.envd-source", 0o644, assets.DockerfileEnvdSource},
		{"edvabe-init.sh", 0o755, assets.EdvabeInitSh},
	}
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name:    e.name,
			Mode:    e.mode,
			Size:    int64(len(e.body)),
			ModTime: now,
		}); err != nil {
			return nil, fmt.Errorf("tar header %s: %w", e.name, err)
		}
		if _, err := tw.Write(e.body); err != nil {
			return nil, fmt.Errorf("tar write %s: %w", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("tar close: %w", err)
	}
	return buf.Bytes(), nil
}
