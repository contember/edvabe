package upstream

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// connectFrame builds a single Connect-stream frame with flags=0 and
// the given JSON body, matching what envd emits per event.
func connectFrame(t *testing.T, payload any) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	buf := make([]byte, 0, 5+len(body))
	buf = append(buf, 0)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(body)))
	buf = append(buf, body...)
	return buf
}

func streamEndEvent(t *testing.T, exitCode int) []byte {
	t.Helper()
	return connectFrame(t, map[string]any{
		"event": map[string]any{
			"end": map[string]any{
				"exitCode": exitCode,
				"exited":   true,
			},
		},
	})
}

func TestWaitReadyEmptyCmdIsFastPath(t *testing.T) {
	p := New()
	// No server — an accidental HTTP call would time out. WaitReady
	// must return immediately without touching the network.
	if err := p.WaitReady(context.Background(), "http://unreachable.invalid", "", ""); err != nil {
		t.Fatalf("WaitReady(\"\") = %v", err)
	}
}

func TestWaitReadySucceedsOnFirstExit0(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/process.Process/Start" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/connect+json")
		var buf bytes.Buffer
		buf.Write(connectFrame(t, map[string]any{
			"event": map[string]any{"start": map[string]any{"pid": 1}},
		}))
		buf.Write(streamEndEvent(t, 0))
		_, _ = io.Copy(w, &buf)
	}))
	defer srv.Close()

	p := New()
	if err := p.WaitReady(context.Background(), srv.URL, "true", ""); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("process start called %d times, want 1", got)
	}
}

func TestWaitReadyRetriesUntilExit0(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		var buf bytes.Buffer
		if n < 3 {
			buf.Write(streamEndEvent(t, 1))
		} else {
			buf.Write(streamEndEvent(t, 0))
		}
		_, _ = io.Copy(w, &buf)
	}))
	defer srv.Close()

	p := New()
	// Override backoff via a quick context — the underlying function
	// still uses defaultReadyBackoff. Tests run fast enough that a
	// couple of 500 ms intervals is acceptable in CI; lower by monkey-
	// patching the constants via a test hook if flakiness appears.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := p.WaitReady(ctx, srv.URL, "flaky-check", ""); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got < 3 {
		t.Errorf("calls = %d, want ≥ 3", got)
	}
}

func TestWaitReadyFailsOnContextExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		buf.Write(streamEndEvent(t, 1)) // never succeeds
		_, _ = io.Copy(w, &buf)
	}))
	defer srv.Close()

	p := New()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := p.WaitReady(ctx, srv.URL, "false", "")
	if err == nil {
		t.Fatalf("expected error on context expiry")
	}
}

func TestDecodeEndExitPicksEndFrame(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(connectFrame(t, map[string]any{
		"event": map[string]any{"start": map[string]any{"pid": 1}},
	}))
	buf.Write(connectFrame(t, map[string]any{
		"event": map[string]any{"data": map[string]any{"stdout": "aGVsbG8="}},
	}))
	buf.Write(connectFrame(t, map[string]any{
		"event": map[string]any{
			"end": map[string]any{"exitCode": 42, "exited": true},
		},
	}))

	got, err := decodeEndExit(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEndExit: %v", err)
	}
	if got != 42 {
		t.Errorf("exit = %d, want 42", got)
	}
}

func TestDecodeEndExitIgnoresTrailerFrame(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(streamEndEvent(t, 0))
	// Trailer frame with flag bit 0x02 set.
	trailer := []byte(`{"metadata":{}}`)
	tbuf := []byte{0x02}
	tbuf = binary.BigEndian.AppendUint32(tbuf, uint32(len(trailer)))
	tbuf = append(tbuf, trailer...)
	buf.Write(tbuf)

	got, err := decodeEndExit(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEndExit: %v", err)
	}
	if got != 0 {
		t.Errorf("exit = %d, want 0", got)
	}
}
