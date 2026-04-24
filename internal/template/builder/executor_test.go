package builder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/contember/edvabe/internal/runtime"
	"github.com/contember/edvabe/internal/template"
	"github.com/contember/edvabe/internal/template/filecache"
)

// fakeRuntime is a lightweight Runtime for DockerExecutor tests. It
// records the BuildRequest and, optionally, writes canned output
// through LogWriter before returning a configurable error.
type fakeRuntime struct {
	mu       sync.Mutex
	lastReq  runtime.BuildRequest
	output   string
	buildErr error
}

func (f *fakeRuntime) Name() string { return "fake" }
func (f *fakeRuntime) Create(context.Context, runtime.CreateRequest) (*runtime.SandboxHandle, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeRuntime) Destroy(context.Context, string) error { return nil }
func (f *fakeRuntime) Pause(context.Context, string) error   { return nil }
func (f *fakeRuntime) Unpause(context.Context, string) error { return nil }
func (f *fakeRuntime) Stop(context.Context, string) error    { return nil }
func (f *fakeRuntime) Start(context.Context, string) error   { return nil }
func (f *fakeRuntime) Commit(context.Context, string, string) error {
	return errors.New("not implemented")
}
func (f *fakeRuntime) Stats(context.Context, string) (*runtime.Stats, error) {
	return &runtime.Stats{}, nil
}
func (f *fakeRuntime) AgentEndpoint(string) (string, int, error) {
	return "", 0, errors.New("not implemented")
}
func (f *fakeRuntime) ListManaged(context.Context) ([]runtime.ManagedContainer, error) {
	return nil, nil
}

func (f *fakeRuntime) BuildImage(ctx context.Context, req runtime.BuildRequest) error {
	f.mu.Lock()
	f.lastReq = req
	out := f.output
	err := f.buildErr
	f.mu.Unlock()
	if req.LogWriter != nil && out != "" {
		_, _ = io.WriteString(req.LogWriter, out)
	}
	return err
}

type recordingSink struct {
	mu      sync.Mutex
	entries []LogEntry
}

func (s *recordingSink) Append(e LogEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
}

func (s *recordingSink) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.entries))
	for i, e := range s.entries {
		out[i] = e.Message
	}
	return out
}

func TestDockerExecutorWritesDockerfileAndStreamsLogs(t *testing.T) {
	root := t.TempDir()
	rt := &fakeRuntime{output: "Step 1/3 : FROM alpine\n ---> abc123\nSuccessfully built xyz\n"}
	exec := &DockerExecutor{
		Runtime:   rt,
		BuildRoot: filepath.Join(root, "builds"),
		Keep:      true,
	}
	sink := &recordingSink{}
	err := exec.Run(context.Background(), ExecutorSpec{
		TemplateID:  "tpl1",
		BuildID:     "bld1",
		ResultImage: "edvabe/user-tpl1:latest",
		Spec: template.BuildSpec{
			FromImage: "alpine:3.19",
			Steps: []template.Step{
				{Type: "RUN", Args: []string{"echo hello"}},
			},
		},
	}, sink)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if rt.lastReq.Tag != "edvabe/user-tpl1:latest" {
		t.Errorf("Tag = %q", rt.lastReq.Tag)
	}
	if rt.lastReq.Dockerfile != "Dockerfile" {
		t.Errorf("Dockerfile name = %q", rt.lastReq.Dockerfile)
	}
	if rt.lastReq.Labels["edvabe.template.id"] != "tpl1" {
		t.Errorf("template.id label = %q", rt.lastReq.Labels["edvabe.template.id"])
	}
	if rt.lastReq.Labels["edvabe.build.id"] != "bld1" {
		t.Errorf("build.id label = %q", rt.lastReq.Labels["edvabe.build.id"])
	}

	dfPath := filepath.Join(rt.lastReq.ContextDir, "Dockerfile")
	dfBytes, err := os.ReadFile(dfPath)
	if err != nil {
		t.Fatalf("read dockerfile: %v", err)
	}
	df := string(dfBytes)
	if !strings.Contains(df, "FROM alpine:3.19") {
		t.Errorf("missing FROM: %q", df)
	}
	if !strings.Contains(df, "RUN echo hello") {
		t.Errorf("missing RUN: %q", df)
	}
	if !strings.Contains(df, "CMD ") {
		t.Errorf("missing CMD: %q", df)
	}

	msgs := sink.messages()
	joined := strings.Join(msgs, "\n")
	if !strings.Contains(joined, "Building edvabe/user-tpl1:latest") {
		t.Errorf("missing Building line: %v", msgs)
	}
	if !strings.Contains(joined, "Step 1/3 : FROM alpine") {
		t.Errorf("missing docker step output: %v", msgs)
	}
	if !strings.Contains(joined, "Successfully built xyz") {
		t.Errorf("missing success line: %v", msgs)
	}
}

func TestDockerExecutorPropagatesBuildError(t *testing.T) {
	root := t.TempDir()
	rt := &fakeRuntime{
		output:   "Step 1/2 : FROM alpine\n",
		buildErr: errors.New("boom"),
	}
	exec := &DockerExecutor{
		Runtime:   rt,
		BuildRoot: filepath.Join(root, "builds"),
	}
	sink := &recordingSink{}
	err := exec.Run(context.Background(), ExecutorSpec{
		TemplateID:  "tpl2",
		BuildID:     "bld2",
		ResultImage: "edvabe/user-tpl2:latest",
		Spec: template.BuildSpec{
			FromImage: "alpine:3.19",
			Steps:     []template.Step{{Type: "RUN", Args: []string{"false"}}},
		},
	}, sink)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want boom", err)
	}
	// Scratch dir must be cleaned up when Keep is false.
	if _, statErr := os.Stat(filepath.Join(root, "builds", "bld2")); !os.IsNotExist(statErr) {
		t.Errorf("scratch dir not cleaned: %v", statErr)
	}
}

func TestDockerExecutorExtractsFileContextsFromCache(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, "cache")
	cache, err := filecache.New(cacheDir)
	if err != nil {
		t.Fatalf("filecache: %v", err)
	}
	tarBytes, hash := buildTar(t, map[string]string{"hello.txt": "hi\n"})
	if err := cache.Put(hash, bytes.NewReader(tarBytes)); err != nil {
		t.Fatalf("cache put: %v", err)
	}

	rt := &fakeRuntime{}
	exec := &DockerExecutor{
		Runtime:   rt,
		Cache:     cache,
		BuildRoot: filepath.Join(root, "builds"),
		Keep:      true,
	}
	sink := &recordingSink{}
	if err := exec.Run(context.Background(), ExecutorSpec{
		TemplateID:  "tpl3",
		BuildID:     "bld3",
		ResultImage: "edvabe/user-tpl3:latest",
		Spec: template.BuildSpec{
			FromImage: "alpine:3.19",
			Steps: []template.Step{
				{Type: "COPY", Args: []string{"hello.txt", "/hello.txt"}, FilesHash: hash},
			},
		},
	}, sink); err != nil {
		t.Fatalf("Run: %v", err)
	}

	staged := filepath.Join(rt.lastReq.ContextDir, "ctx", hash, "hello.txt")
	got, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("read staged: %v", err)
	}
	if string(got) != "hi\n" {
		t.Errorf("staged contents = %q", string(got))
	}
}

// buildTar produces a gzip'd tar archive containing files, and returns
// the bytes plus their sha256 hex hash — the shape the SDK uploads.
func buildTar(t *testing.T, files map[string]string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(body)),
		}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes(), filecache.HashBytes(buf.Bytes())
}

func TestSinkWriterSplitsOnNewlines(t *testing.T) {
	sink := &recordingSink{}
	w := &sinkWriter{sink: sink, source: "docker"}
	_, _ = w.Write([]byte("first\nsec"))
	_, _ = w.Write([]byte("ond\nthird"))
	w.flush()
	msgs := sink.messages()
	want := []string{"first", "second", "third"}
	if fmt.Sprint(msgs) != fmt.Sprint(want) {
		t.Errorf("messages = %v, want %v", msgs, want)
	}
}
