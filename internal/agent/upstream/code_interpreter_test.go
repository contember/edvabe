package upstream

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"
)

func TestCodeInterpreterBuildContext(t *testing.T) {
	data, err := codeInterpreterBuildContext()
	if err != nil {
		t.Fatalf("codeInterpreterBuildContext: %v", err)
	}

	tr := tar.NewReader(bytes.NewReader(data))
	found := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		found[hdr.Name] = true
	}

	for _, want := range []string{"Dockerfile.code-interpreter", "edvabe-init.sh"} {
		if !found[want] {
			t.Errorf("missing tar entry %q", want)
		}
	}
}

func TestEnsureCodeInterpreterImageRejectsEmptyTag(t *testing.T) {
	err := EnsureCodeInterpreterImage(t.Context(), "")
	if err == nil {
		t.Fatal("expected error for empty tag")
	}
}
