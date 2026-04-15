//go:build integration

// Integration test for the real Docker-backed build executor. Run with:
//
//	go test -tags=integration ./internal/template/builder/...
//
// Requires a reachable Docker daemon plus the edvabe/envd-source:latest
// image the translator injects via COPY --from. When envd-source is
// missing the test skips rather than failing — task 6 builds that
// image on demand, and the executor itself does not care.
package builder

import (
	"context"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/contember/edvabe/internal/runtime/docker"
	"github.com/contember/edvabe/internal/template"
)

func TestDockerExecutorBuildsTrivialTemplate(t *testing.T) {
	if _, err := osexec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI not found: %v", err)
	}
	if err := osexec.Command("docker", "image", "inspect", "edvabe/envd-source:latest").Run(); err != nil {
		t.Skipf("edvabe/envd-source:latest not present — run build-image (task 6) first")
	}

	rt, err := docker.New()
	if err != nil {
		t.Skipf("docker runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	buildRoot := filepath.Join(t.TempDir(), "builds")
	exec := &DockerExecutor{
		Runtime:   rt,
		BuildRoot: buildRoot,
	}

	sink := &recordingSink{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	tag := "edvabe/user-integration-test:latest"
	t.Cleanup(func() {
		_ = osexec.Command("docker", "rmi", "-f", tag).Run()
	})

	err = exec.Run(ctx, ExecutorSpec{
		TemplateID:  "integration-test",
		BuildID:     "bld-integration",
		ResultImage: tag,
		Spec: template.BuildSpec{
			FromImage: "alpine:3.19",
			Steps: []template.Step{
				{Type: "RUN", Args: []string{"echo hello-from-edvabe-executor"}},
			},
		},
	}, sink)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs := sink.messages()
	joined := strings.Join(msgs, "\n")
	if !strings.Contains(joined, "hello-from-edvabe-executor") &&
		!strings.Contains(joined, "Running in") &&
		!strings.Contains(joined, "Successfully") {
		t.Errorf("expected a recognizable docker log line; got: %v", msgs)
	}

	if out, err := osexec.Command("docker", "image", "inspect", tag).CombinedOutput(); err != nil {
		t.Errorf("image %q not present after build: %v\n%s", tag, err, out)
	}
}
