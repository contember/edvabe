package builder

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/contember/edvabe/internal/template/filecache"
)

// PrepareContext extracts the file-cache blob for each hash in hashes
// into buildDir/<StagingDir>/<hash>/ so the Dockerfile that references
// those paths can be fed to docker build.
//
// SDK tars are gzipped (see tarFileStream in
// test/e2e/ts/node_modules/e2b/dist/index.mjs:4580-4596). We accept
// both gzipped and uncompressed tars transparently; anything else is
// an error.
//
// If a hash is already extracted (directory exists and is non-empty),
// the call is a no-op for that hash. buildDir is assumed to exist.
// stagingDir is the same value passed to Translate.Input.StagingDir.
func PrepareContext(cache *filecache.Cache, buildDir, stagingDir string, hashes []string) error {
	if cache == nil {
		return errors.New("builder: PrepareContext: cache is required")
	}
	if buildDir == "" {
		return errors.New("builder: PrepareContext: buildDir is required")
	}
	if stagingDir == "" {
		stagingDir = "ctx"
	}
	for _, hash := range hashes {
		dest := filepath.Join(buildDir, stagingDir, hash)
		if entries, err := os.ReadDir(dest); err == nil && len(entries) > 0 {
			continue
		}
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return fmt.Errorf("builder: PrepareContext: mkdir %q: %w", dest, err)
		}
		if err := extractTar(cache, hash, dest); err != nil {
			return fmt.Errorf("builder: PrepareContext: extract %s: %w", hash, err)
		}
	}
	return nil
}

func extractTar(cache *filecache.Cache, hash, dest string) error {
	rc, err := cache.Open(hash)
	if err != nil {
		return err
	}
	defer rc.Close()

	// Peek the first two bytes to decide whether we are looking at a
	// gzipped stream (magic 1f 8b) or a plain tar. bufio.Reader lets
	// us put the bytes back after the check.
	br := bufio.NewReader(rc)
	magic, err := br.Peek(2)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("peek: %w", err)
	}

	var reader io.Reader = br
	if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return fmt.Errorf("gzip: %w", err)
		}
		defer gz.Close()
		reader = gz
	}

	tr := tar.NewReader(reader)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}
		// Reject any entry whose normalized path escapes dest — a
		// malicious tar should not be able to write outside the
		// staging dir.
		cleaned := filepath.Clean(hdr.Name)
		if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
			return fmt.Errorf("unsafe tar entry %q", hdr.Name)
		}
		target := filepath.Join(dest, cleaned)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode&0o777)); err != nil {
				return fmt.Errorf("mkdir %q: %w", target, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent %q: %w", target, err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode&0o777))
			if err != nil {
				return fmt.Errorf("create %q: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return fmt.Errorf("copy %q: %w", target, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("close %q: %w", target, err)
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent %q: %w", target, err)
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return fmt.Errorf("symlink %q: %w", target, err)
			}
		default:
			// Skip anything else (fifos, devices, etc.) — the SDK
			// never produces them.
		}
	}
}
