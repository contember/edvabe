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

// CodeInterpreterTag is the default image tag for the code-interpreter
// image that edvabe builds and seeds as a built-in template.
const CodeInterpreterTag = "edvabe/code-interpreter:latest"

// CodeInterpreterRepoSHA pins the e2b-dev/code-interpreter commit from
// which the template files (server, config, startup scripts) are
// cloned at `docker build` time. Bump by picking a newer commit SHA.
const CodeInterpreterRepoSHA = "a1b5f41b2a5c37939d07c4785ee3027cf1c5fcc4"

// EnsureCodeInterpreterImage builds the code-interpreter Docker image.
// The Dockerfile is a multi-stage build that compiles envd from source
// (shared layer cache with EnsureBaseImage), clones the upstream
// code-interpreter repo at CodeInterpreterRepoSHA, and assembles the
// final image with Jupyter, the FastAPI overlay, envd, and edvabe-init.
//
// The build context is a tar stream containing the Dockerfile and
// edvabe-init.sh (same wrapper as EnsureEnvdSource).
//
// Idempotent: Docker's layer cache makes re-runs fast.
func EnsureCodeInterpreterImage(ctx context.Context, tag string) error {
	if tag == "" {
		return fmt.Errorf("EnsureCodeInterpreterImage: tag is required")
	}

	tarBytes, err := codeInterpreterBuildContext()
	if err != nil {
		return fmt.Errorf("code-interpreter build context: %w", err)
	}

	cmd := exec.CommandContext(ctx, "docker", "build",
		"--build-arg", "ENVD_SHA="+EnvdSourceSHA,
		"--build-arg", "CODE_INTERPRETER_SHA="+CodeInterpreterRepoSHA,
		"-f", "Dockerfile.code-interpreter",
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

// codeInterpreterBuildContext returns a tar archive holding the
// Dockerfile and edvabe-init.sh. The code-interpreter source files are
// fetched at build time via git clone inside the Dockerfile, so they
// don't need to be in the context.
func codeInterpreterBuildContext() ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()
	entries := []struct {
		name string
		mode int64
		body []byte
	}{
		{"Dockerfile.code-interpreter", 0o644, assets.DockerfileCodeInterpreter},
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
