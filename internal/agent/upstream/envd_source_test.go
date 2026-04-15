package upstream

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"

	"github.com/contember/edvabe/assets"
)

func TestEnvdSourceBuildContextIncludesBothFiles(t *testing.T) {
	raw, err := envdSourceBuildContext()
	if err != nil {
		t.Fatalf("envdSourceBuildContext: %v", err)
	}

	found := map[string][]byte{}
	tr := tar.NewReader(bytes.NewReader(raw))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("tar read %s: %v", hdr.Name, err)
		}
		found[hdr.Name] = body
	}

	if got := found["Dockerfile.envd-source"]; !bytes.Equal(got, assets.DockerfileEnvdSource) {
		t.Errorf("Dockerfile body = %d bytes, want %d", len(got), len(assets.DockerfileEnvdSource))
	}
	if got := found["edvabe-init.sh"]; !bytes.Equal(got, assets.EdvabeInitSh) {
		t.Errorf("edvabe-init.sh body = %d bytes, want %d", len(got), len(assets.EdvabeInitSh))
	}
	if !bytes.HasPrefix(found["edvabe-init.sh"], []byte("#!/bin/sh")) {
		t.Errorf("edvabe-init.sh should start with a shebang")
	}
}
