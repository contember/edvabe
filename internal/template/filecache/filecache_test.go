package filecache

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newCache(t *testing.T) *Cache {
	t.Helper()
	c, err := New(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestCachePutThenHas(t *testing.T) {
	c := newCache(t)
	payload := []byte("hello world")
	hash := HashBytes(payload)

	if present, _ := c.Has(hash); present {
		t.Fatal("cache should start empty")
	}
	if err := c.Put(hash, bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if present, err := c.Has(hash); err != nil || !present {
		t.Fatalf("Has: present=%v err=%v", present, err)
	}

	r, err := c.Open(hash)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, payload) {
		t.Fatalf("round-trip mismatch: %q vs %q", got, payload)
	}
}

func TestCachePutIdempotent(t *testing.T) {
	c := newCache(t)
	payload := []byte("same")
	hash := HashBytes(payload)
	if err := c.Put(hash, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	// Second put with the same payload must not error and must not
	// corrupt the existing file.
	if err := c.Put(hash, bytes.NewReader(payload)); err != nil {
		t.Fatalf("second Put: %v", err)
	}
	// A reader that would otherwise be misidentified should still not
	// touch the cache if the hash already matches a present file —
	// the caller said "this is hash X", we trust the existing file.
	if err := c.Put(hash, strings.NewReader("different but ignored")); err != nil {
		t.Fatalf("idempotent third Put: %v", err)
	}
	r, _ := c.Open(hash)
	defer r.Close()
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, payload) {
		t.Fatalf("existing blob was overwritten: %q", got)
	}
}

func TestCachePutOpaqueHash(t *testing.T) {
	// The hash is an opaque client-supplied key. Put stores the bytes
	// under that key without re-verifying the content.
	c := newCache(t)
	hash := HashBytes([]byte("advertised"))
	content := "actual different content"
	if err := c.Put(hash, strings.NewReader(content)); err != nil {
		t.Fatalf("Put with opaque hash: %v", err)
	}
	if present, _ := c.Has(hash); !present {
		t.Fatal("blob should be present")
	}
	r, _ := c.Open(hash)
	defer r.Close()
	got, _ := io.ReadAll(r)
	if string(got) != content {
		t.Fatalf("content mismatch: %q", got)
	}
}

func TestCacheInvalidHash(t *testing.T) {
	c := newCache(t)
	if _, err := c.Has("not-a-hash"); err == nil {
		t.Fatal("expected error for invalid hash")
	}
	if err := c.Put("short", bytes.NewReader(nil)); err == nil {
		t.Fatal("expected error for invalid hash")
	}
	if _, err := c.Open("SHOUTY"); err == nil {
		t.Fatal("expected error for uppercase hash")
	}
}

func TestCacheConcurrentPutSameHash(t *testing.T) {
	c := newCache(t)
	payload := []byte("concurrent")
	hash := HashBytes(payload)

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Put(hash, bytes.NewReader(payload)); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Put: %v", err)
	}
	// Exactly one final blob, no leftover .part files.
	entries, _ := os.ReadDir(c.Root())
	var finals, parts int
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name(), ".tar"):
			finals++
		case strings.HasSuffix(e.Name(), ".part"):
			parts++
		}
	}
	if finals != 1 {
		t.Fatalf("expected 1 final blob, got %d", finals)
	}
	if parts != 0 {
		t.Fatalf("expected 0 stray .part files, got %d", parts)
	}
}

func TestSignerRoundTrip(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	s := NewSigner(SignerOptions{
		Secret: []byte("test-secret"),
		TTL:    time.Minute,
		Now:    func() time.Time { return now },
	})
	hash := HashBytes([]byte("payload"))
	token := s.Sign(hash)
	if err := s.Verify(hash, token); err != nil {
		t.Fatalf("Verify round-trip: %v", err)
	}
}

func TestSignerWrongHash(t *testing.T) {
	s := NewSigner(SignerOptions{Secret: []byte("k")})
	token := s.Sign(HashBytes([]byte("a")))
	if err := s.Verify(HashBytes([]byte("b")), token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestSignerExpiry(t *testing.T) {
	current := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return current }
	s := NewSigner(SignerOptions{
		Secret: []byte("k"),
		TTL:    time.Minute,
		Now:    now,
	})
	hash := HashBytes([]byte("x"))
	token := s.Sign(hash)
	// Jump past the expiry.
	current = current.Add(2 * time.Minute)
	if err := s.Verify(hash, token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected expiry failure, got %v", err)
	}
}

func TestSignerTamper(t *testing.T) {
	s := NewSigner(SignerOptions{Secret: []byte("k")})
	hash := HashBytes([]byte("x"))
	token := s.Sign(hash)
	tampered := token[:len(token)-4] + "XXXX"
	if err := s.Verify(hash, tampered); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected tamper rejection, got %v", err)
	}
}

func TestSignerMalformed(t *testing.T) {
	s := NewSigner(SignerOptions{Secret: []byte("k")})
	cases := []string{
		"",
		"not-even-a-dot",
		"sig.notanumber",
	}
	for _, c := range cases {
		if err := s.Verify("abc", c); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("case %q: expected ErrInvalidToken, got %v", c, err)
		}
	}
}

func TestNewRandomSigner(t *testing.T) {
	s, err := NewRandomSigner(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	hash := HashBytes([]byte("z"))
	if err := s.Verify(hash, s.Sign(hash)); err != nil {
		t.Fatalf("random signer round-trip: %v", err)
	}
}
