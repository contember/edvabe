package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/moby/go-archive"
	"github.com/moby/moby/client"

	"github.com/contember/edvabe/internal/runtime"
)

// BuildImage builds an image from a filesystem build context. Used by
// the upstream AgentProvider and by the template builder. Consumes the
// full build output stream before returning so the image is ready when
// BuildImage returns. If req.LogWriter is non-nil, each line of docker
// daemon output is forwarded to it as the build progresses.
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

	if err := streamBuildOutput(resp.Body, req.LogWriter); err != nil {
		return fmt.Errorf("docker runtime: build %q: %w", req.Tag, err)
	}
	return nil
}

// buildMessage is a subset of the jsonmessage the docker daemon streams
// on the build output channel. Stream is the ordinary progress line;
// Error is set on a fatal failure.
type buildMessage struct {
	Stream      string          `json:"stream,omitempty"`
	Error       string          `json:"error,omitempty"`
	ErrorDetail json.RawMessage `json:"errorDetail,omitempty"`
}

// streamBuildOutput decodes the line-delimited JSON emitted by the
// docker daemon on an ImageBuild response. Each non-empty Stream line
// is forwarded to sink (one Docker output line per Write, trailing
// newlines trimmed). The first Error line surfaces as the returned
// error. Non-JSON lines are forwarded verbatim as a defensive fallback.
func streamBuildOutput(r io.Reader, sink io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var firstErr string
	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var msg buildMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			if sink != nil {
				_, _ = sink.Write(append(raw, '\n'))
			}
			continue
		}
		if msg.Error != "" && firstErr == "" {
			firstErr = msg.Error
		}
		if msg.Stream != "" && sink != nil {
			for _, line := range splitLines(msg.Stream) {
				if line == "" {
					continue
				}
				_, _ = sink.Write([]byte(line + "\n"))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan build output: %w", err)
	}
	if firstErr != "" {
		return fmt.Errorf("%s", firstErr)
	}
	return nil
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// Commit snapshots a sandbox filesystem as a new image. Phase 4. Stubbed
// out in Phase 1.
func (r *Runtime) Commit(ctx context.Context, sandboxID, imageTag string) error {
	return fmt.Errorf("docker runtime: Commit not implemented (phase 4)")
}
