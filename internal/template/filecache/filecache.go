// Package filecache is the content-addressed blob store edvabe uses
// for template file contexts.
//
// The E2B SDK's Template.build() tars each COPY/copyItems step's source
// paths client-side, hashes the tar, and asks the server via
// GET /templates/{id}/files/{hash} whether the blob is already present.
// On a miss, the server hands back a short-lived upload URL; the SDK
// PUTs the tar there. Cache is deduplicated across templates — two
// templates that COPY the same files share a single on-disk blob.
//
// The on-disk layout is flat: <root>/<hash>.tar. Writes are atomic via
// a .part sidecar that's renamed into place on success, so a crash or
// a concurrent Put never leaves a reader seeing a half-written blob.
package filecache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// ErrHashMismatch is returned by Put when the bytes received did not
// hash to the expected value. Callers should treat this as a client
// error (HTTP 400) — the upload was corrupted in flight.
var ErrHashMismatch = errors.New("filecache: hash mismatch")

// validHash is the SDK's hash convention: 64 lowercase hex characters
// (sha256). Any other format is rejected before touching the disk.
var validHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Cache is a content-addressed blob store rooted at Root. The zero
// value is not usable; construct with New.
type Cache struct {
	root string
}

// New constructs a Cache with the given root directory. The directory
// is created if it does not already exist.
func New(root string) (*Cache, error) {
	if root == "" {
		return nil, errors.New("filecache: root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("filecache: mkdir %q: %w", root, err)
	}
	return &Cache{root: root}, nil
}

// Root returns the directory the cache is rooted at.
func (c *Cache) Root() string { return c.root }

// Has reports whether the given blob exists in the cache. Returns an
// error only for I/O problems, not for a missing blob (which returns
// false, nil).
func (c *Cache) Has(hash string) (bool, error) {
	if !validHash.MatchString(hash) {
		return false, fmt.Errorf("filecache: invalid hash %q", hash)
	}
	_, err := os.Stat(c.path(hash))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("filecache: stat %q: %w", hash, err)
	}
	return true, nil
}

// Open returns a reader over the cached blob. Caller must close the
// returned reader. Returns os.ErrNotExist if the blob is not in the
// cache.
func (c *Cache) Open(hash string) (io.ReadCloser, error) {
	if !validHash.MatchString(hash) {
		return nil, fmt.Errorf("filecache: invalid hash %q", hash)
	}
	f, err := os.Open(c.path(hash))
	if err != nil {
		return nil, fmt.Errorf("filecache: open %q: %w", hash, err)
	}
	return f, nil
}

// Put writes the reader's bytes into the cache under the given hash.
// The bytes are hashed as they are written; on mismatch, the partial
// file is deleted and ErrHashMismatch is returned. Put is idempotent:
// if the blob is already present, the existing file is kept and the
// input reader is drained but not used. Callers can count on the final
// on-disk file being the hash-verified copy.
func (c *Cache) Put(hash string, r io.Reader) error {
	if !validHash.MatchString(hash) {
		return fmt.Errorf("filecache: invalid hash %q", hash)
	}
	final := c.path(hash)
	if _, err := os.Stat(final); err == nil {
		// Already present. Still drain the input so the HTTP handler
		// can keep pipelining and report Content-Length consistently.
		_, _ = io.Copy(io.Discard, r)
		return nil
	}

	tmp, err := os.CreateTemp(c.root, ".put-*.part")
	if err != nil {
		return fmt.Errorf("filecache: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	// On any non-happy path, make sure the .part is cleaned up.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), r); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("filecache: write tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("filecache: close tmp: %w", err)
	}

	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != hash {
		return fmt.Errorf("%w: expected %s got %s", ErrHashMismatch, hash, actual)
	}

	if err := os.Rename(tmpPath, final); err != nil {
		// A concurrent Put may have won the race. If the final file
		// now exists, treat that as success and drop our .part.
		if _, statErr := os.Stat(final); statErr == nil {
			success = true
			return nil
		}
		return fmt.Errorf("filecache: rename: %w", err)
	}
	success = true
	return nil
}

// path is the on-disk location for a given hash.
func (c *Cache) path(hash string) string {
	return filepath.Join(c.root, hash+".tar")
}

// HashBytes returns the hex-encoded sha256 of b. Useful for tests and
// for callers that need to pre-compute a hash before calling Put.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
