package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/contember/edvabe/internal/sandbox"
)

// fakeLookup is a manually-populated SandboxLookup for proxy tests.
type fakeLookup struct {
	byID map[string]*sandbox.Sandbox
}

func (f *fakeLookup) Get(id string) (*sandbox.Sandbox, error) {
	if f.byID == nil {
		return nil, sandbox.ErrNotFound
	}
	s, ok := f.byID[id]
	if !ok {
		return nil, sandbox.ErrNotFound
	}
	return s, nil
}

// fakeResolver is an AgentResolver that always returns the configured
// host:port, or the configured error.
type fakeResolver struct {
	host string
	port int
	err  error
}

func (f *fakeResolver) AgentEndpoint(_ string) (string, int, error) {
	if f.err != nil {
		return "", 0, f.err
	}
	return f.host, f.port, nil
}

// newBackend spins up an httptest.Server, parses its URL, and returns
// the hostname, port, and a Close cleanup function.
func newBackend(t *testing.T, h http.Handler) (host string, port int, close func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	u, err := url.Parse(srv.URL)
	if err != nil {
		srv.Close()
		t.Fatalf("parse backend URL: %v", err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		srv.Close()
		t.Fatalf("parse backend port: %v", err)
	}
	return u.Hostname(), p, srv.Close
}

func TestProxyForwardsRequest(t *testing.T) {
	var gotPath, gotAccessToken, gotMethod string
	host, port, closeBackend := newBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAccessToken = r.Header.Get("X-Access-Token")
		gotMethod = r.Method
		w.Header().Set("X-Envd-Handled", "yes")
		w.WriteHeader(200)
		fmt.Fprintln(w, "hello from backend")
	}))
	defer closeBackend()

	mgr := &fakeLookup{byID: map[string]*sandbox.Sandbox{"isb_a": {ID: "isb_a"}}}
	rt := &fakeResolver{host: host, port: port}
	front := httptest.NewServer(NewProxy(mgr, rt))
	defer front.Close()

	req, _ := http.NewRequest("POST", front.URL+"/files?path=/etc/hostname", nil)
	req.Header.Set(HeaderSandboxID, "isb_a")
	req.Header.Set("X-Access-Token", "ea_secret")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Envd-Handled"); got != "yes" {
		t.Errorf("X-Envd-Handled = %q, want yes", got)
	}
	if gotPath != "/files" {
		t.Errorf("backend path = %q, want /files", gotPath)
	}
	if gotMethod != "POST" {
		t.Errorf("backend method = %q, want POST", gotMethod)
	}
	if gotAccessToken != "ea_secret" {
		t.Errorf("backend X-Access-Token = %q, want ea_secret", gotAccessToken)
	}
}

func TestProxyReturns404OnUnknownSandbox(t *testing.T) {
	mgr := &fakeLookup{byID: map[string]*sandbox.Sandbox{}}
	rt := &fakeResolver{host: "127.0.0.1", port: 1}
	front := httptest.NewServer(NewProxy(mgr, rt))
	defer front.Close()

	req, _ := http.NewRequest("GET", front.URL+"/health", nil)
	req.Header.Set(HeaderSandboxID, "isb_missing")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if code, _ := body["code"].(float64); int(code) != http.StatusNotFound {
		t.Errorf("envelope.code = %v, want 404", body["code"])
	}
	if msg, _ := body["message"].(string); msg == "" {
		t.Errorf("envelope.message is empty")
	}
}

func TestProxyReturns400WhenHeaderMissing(t *testing.T) {
	mgr := &fakeLookup{byID: map[string]*sandbox.Sandbox{}}
	rt := &fakeResolver{host: "127.0.0.1", port: 1}
	front := httptest.NewServer(NewProxy(mgr, rt))
	defer front.Close()

	resp, err := http.Get(front.URL + "/health")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestProxyReturns502WhenUpstreamDown(t *testing.T) {
	mgr := &fakeLookup{byID: map[string]*sandbox.Sandbox{"isb_a": {ID: "isb_a"}}}
	// Point the resolver at a port nothing listens on (port 1 is the
	// TCPMUX well-known port; effectively always closed on dev laptops).
	rt := &fakeResolver{host: "127.0.0.1", port: 1}
	front := httptest.NewServer(NewProxy(mgr, rt))
	defer front.Close()

	req, _ := http.NewRequest("GET", front.URL+"/health", nil)
	req.Header.Set(HeaderSandboxID, "isb_a")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

// TestProxyStreamsResponse verifies that FlushInterval: -1 is wired up
// correctly: a backend that flushes two chunks with a 100ms gap between
// them must not be coalesced into a single client-visible read.
func TestProxyStreamsResponse(t *testing.T) {
	const chunkDelay = 150 * time.Millisecond

	host, port, closeBackend := newBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("first\n"))
		flusher.Flush()
		time.Sleep(chunkDelay)
		_, _ = w.Write([]byte("second\n"))
		flusher.Flush()
	}))
	defer closeBackend()

	mgr := &fakeLookup{byID: map[string]*sandbox.Sandbox{"isb_s": {ID: "isb_s"}}}
	rt := &fakeResolver{host: host, port: port}
	front := httptest.NewServer(NewProxy(mgr, rt))
	defer front.Close()

	req, _ := http.NewRequest("GET", front.URL+"/stream", nil)
	req.Header.Set(HeaderSandboxID, "isb_s")

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)

	line1, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	t1 := time.Since(start)
	if line1 != "first\n" {
		t.Errorf("first = %q", line1)
	}

	line2, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	t2 := time.Since(start)
	if line2 != "second\n" {
		t.Errorf("second = %q", line2)
	}

	// If the proxy buffered the response, both reads would return at
	// roughly the same time near t2 ≈ chunkDelay. With streaming, the
	// first read should return well before the chunkDelay elapses.
	// Allow 50ms slack for scheduling jitter on busy CI.
	if t1 >= chunkDelay-50*time.Millisecond {
		t.Errorf("first chunk arrived too late (buffered?): t1=%v chunkDelay=%v", t1, chunkDelay)
	}
	if gap := t2 - t1; gap < chunkDelay-50*time.Millisecond {
		t.Errorf("chunks too close together: t1=%v t2=%v gap=%v want>=%v", t1, t2, gap, chunkDelay-50*time.Millisecond)
	}
}
