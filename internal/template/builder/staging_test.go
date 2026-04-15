package builder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/contember/edvabe/internal/template/filecache"
)

func newTestCache(t *testing.T) *filecache.Cache {
	t.Helper()
	c, err := filecache.New(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type tarEntry struct {
	name    string
	content string
	mode    int64
	dir     bool
}

func makeTar(t *testing.T, gzipped bool, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w io.WriteCloser = nopWriteCloser{&buf}
	if gzipped {
		w = gzip.NewWriter(&buf)
	}
	tw := tar.NewWriter(w)
	for _, e := range entries {
		hdr := &tar.Header{
			Name: e.name,
			Mode: e.mode,
		}
		if e.dir {
			hdr.Typeflag = tar.TypeDir
		} else {
			hdr.Typeflag = tar.TypeReg
			hdr.Size = int64(len(e.content))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if !e.dir {
			if _, err := tw.Write([]byte(e.content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if gzipped {
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestPrepareContextGzippedTar(t *testing.T) {
	cache := newTestCache(t)
	tarBytes := makeTar(t, true, []tarEntry{
		{name: "template/", dir: true, mode: 0o755},
		{name: "template/build.ts", content: "console.log('hi')", mode: 0o644},
		{name: "template/src/", dir: true, mode: 0o755},
		{name: "template/src/foo.ts", content: "export const foo = 1", mode: 0o644},
	})
	hash := filecache.HashBytes(tarBytes)
	if err := cache.Put(hash, bytes.NewReader(tarBytes)); err != nil {
		t.Fatal(err)
	}

	buildDir := t.TempDir()
	if err := PrepareContext(cache, buildDir, "ctx", []string{hash}); err != nil {
		t.Fatalf("PrepareContext: %v", err)
	}

	want := filepath.Join(buildDir, "ctx", hash, "template", "build.ts")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if string(data) != "console.log('hi')" {
		t.Fatalf("contents mismatch: %q", data)
	}

	nested := filepath.Join(buildDir, "ctx", hash, "template", "src", "foo.ts")
	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("nested not extracted: %v", err)
	}
}

func TestPrepareContextPlainTar(t *testing.T) {
	cache := newTestCache(t)
	tarBytes := makeTar(t, false, []tarEntry{
		{name: "hello.txt", content: "world", mode: 0o644},
	})
	hash := filecache.HashBytes(tarBytes)
	_ = cache.Put(hash, bytes.NewReader(tarBytes))

	buildDir := t.TempDir()
	if err := PrepareContext(cache, buildDir, "", []string{hash}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(buildDir, "ctx", hash, "hello.txt"))
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if string(got) != "world" {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestPrepareContextSkipsExisting(t *testing.T) {
	cache := newTestCache(t)
	tarBytes := makeTar(t, true, []tarEntry{
		{name: "a.txt", content: "original", mode: 0o644},
	})
	hash := filecache.HashBytes(tarBytes)
	_ = cache.Put(hash, bytes.NewReader(tarBytes))

	buildDir := t.TempDir()
	if err := PrepareContext(cache, buildDir, "ctx", []string{hash}); err != nil {
		t.Fatal(err)
	}
	// Corrupt the extracted file in place, then call PrepareContext
	// again with the same hash. Since the directory is non-empty, the
	// call should be a no-op and leave our corruption intact.
	target := filepath.Join(buildDir, "ctx", hash, "a.txt")
	if err := os.WriteFile(target, []byte("corrupted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PrepareContext(cache, buildDir, "ctx", []string{hash}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "corrupted" {
		t.Fatalf("expected no-op, got: %q", got)
	}
}

func TestPrepareContextUnsafeEntry(t *testing.T) {
	cache := newTestCache(t)
	tarBytes := makeTar(t, true, []tarEntry{
		{name: "../escape.txt", content: "nope", mode: 0o644},
	})
	hash := filecache.HashBytes(tarBytes)
	_ = cache.Put(hash, bytes.NewReader(tarBytes))

	buildDir := t.TempDir()
	if err := PrepareContext(cache, buildDir, "ctx", []string{hash}); err == nil {
		t.Fatal("expected unsafe tar rejection")
	}
}

func TestPrepareContextMissingHash(t *testing.T) {
	cache := newTestCache(t)
	buildDir := t.TempDir()
	err := PrepareContext(cache, buildDir, "ctx", []string{"0000000000000000000000000000000000000000000000000000000000000000"})
	if err == nil {
		t.Fatal("expected missing-hash error")
	}
}
