package builder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/contember/edvabe/internal/runtime"
	"github.com/contember/edvabe/internal/template/filecache"
)

// DockerExecutor binds the Manager's Executor contract to the real
// docker build pipeline: extract cached tar contexts, translate the
// step array into a Dockerfile, write it into a build dir, and hand
// off to runtime.Runtime.BuildImage with a streaming log sink.
//
// Build scratch lives under BuildRoot/<buildID>/; the directory is
// removed when the build finishes (success or failure) unless Keep is
// set, which the integration tests use to inspect the generated
// context.
type DockerExecutor struct {
	// Runtime is the docker runtime. Required.
	Runtime runtime.Runtime
	// Cache is the content-addressed file cache the SDK uploaded
	// tarred step contexts into. Required whenever any step carries a
	// filesHash — pure-RUN templates work with a nil cache.
	Cache *filecache.Cache
	// BuildRoot is the parent directory under which per-build scratch
	// dirs are created. Required; must exist (or be createable).
	BuildRoot string
	// StagingDir is the name of the directory, inside each per-build
	// scratch dir, where extracted file contexts land. Defaults to
	// "ctx". Must match the value used by Translate.
	StagingDir string
	// Keep, when true, leaves the scratch dir on disk after the build
	// completes. Used by tests to inspect the generated Dockerfile and
	// staged files.
	Keep bool
}

// Run implements builder.Executor.
func (e *DockerExecutor) Run(ctx context.Context, spec ExecutorSpec, sink LogSink) error {
	if e.Runtime == nil {
		return errors.New("builder: DockerExecutor: Runtime is required")
	}
	if e.BuildRoot == "" {
		return errors.New("builder: DockerExecutor: BuildRoot is required")
	}
	staging := e.StagingDir
	if staging == "" {
		staging = "ctx"
	}

	if err := os.MkdirAll(e.BuildRoot, 0o755); err != nil {
		return fmt.Errorf("builder: DockerExecutor: mkdir BuildRoot: %w", err)
	}
	buildDir := filepath.Join(e.BuildRoot, spec.BuildID)
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return fmt.Errorf("builder: DockerExecutor: mkdir buildDir: %w", err)
	}
	if !e.Keep {
		defer func() { _ = os.RemoveAll(buildDir) }()
	}

	input := Input{
		FromImage:         spec.Spec.FromImage,
		FromTemplateImage: spec.ParentImage,
		Steps:             spec.Spec.Steps,
		StagingDir:        staging,
	}
	// When the SDK specifies fromTemplate, the caller (the HTTP handler)
	// has pre-resolved ParentImage. In that case Translate wants only
	// FromTemplateImage set, so clear FromImage defensively.
	if input.FromTemplateImage != "" {
		input.FromImage = ""
	}

	out, err := Translate(input)
	if err != nil {
		return fmt.Errorf("translate: %w", err)
	}

	if len(out.RequiredFileHashes) > 0 {
		if e.Cache == nil {
			return errors.New("builder: DockerExecutor: Cache is required when steps reference files")
		}
		if err := PrepareContext(e.Cache, buildDir, staging, out.RequiredFileHashes); err != nil {
			return fmt.Errorf("prepare context: %w", err)
		}
	}

	dockerfilePath := filepath.Join(buildDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(out.Dockerfile), 0o644); err != nil {
		return fmt.Errorf("write Dockerfile: %w", err)
	}

	sink.Append(LogEntry{
		Level:   "info",
		Source:  "docker",
		Message: fmt.Sprintf("Building %s", spec.ResultImage),
	})

	writer := &sinkWriter{sink: sink, source: "docker"}
	buildErr := e.Runtime.BuildImage(ctx, runtime.BuildRequest{
		Tag:        spec.ResultImage,
		ContextDir: buildDir,
		Dockerfile: "Dockerfile",
		Labels: map[string]string{
			"edvabe.template.id": spec.TemplateID,
			"edvabe.build.id":    spec.BuildID,
		},
		LogWriter: writer,
	})
	writer.flush()
	if buildErr != nil {
		return buildErr
	}
	return nil
}

// sinkWriter adapts a LogSink to io.Writer so the runtime's LogWriter
// hook can stream daemon output straight into the per-build ring
// buffer. Bytes are split on newlines; trailing bytes that haven't hit
// a newline yet are buffered until flush.
type sinkWriter struct {
	mu     sync.Mutex
	sink   LogSink
	source string
	buf    bytes.Buffer
	clock  func() time.Time
}

func (w *sinkWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, _ := w.buf.Write(p)
	for {
		idx := bytes.IndexByte(w.buf.Bytes(), '\n')
		if idx < 0 {
			break
		}
		line := string(w.buf.Next(idx + 1))
		line = trimNewline(line)
		if line == "" {
			continue
		}
		w.sink.Append(LogEntry{
			Timestamp: w.now(),
			Level:     "info",
			Source:    w.source,
			Message:   line,
		})
	}
	return n, nil
}

func (w *sinkWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() == 0 {
		return
	}
	line := trimNewline(w.buf.String())
	w.buf.Reset()
	if line == "" {
		return
	}
	w.sink.Append(LogEntry{
		Timestamp: w.now(),
		Level:     "info",
		Source:    w.source,
		Message:   line,
	})
}

func (w *sinkWriter) now() time.Time {
	if w.clock != nil {
		return w.clock()
	}
	return time.Now()
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
