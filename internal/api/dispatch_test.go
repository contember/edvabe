package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouterRoutesToControlWhenNoSandbox(t *testing.T) {
	var hits struct{ control, proxy int }
	ctrl := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { hits.control++ })
	prox := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { hits.proxy++ })
	h := NewRouter(ctrl, prox, nil)

	req := httptest.NewRequest("GET", "http://localhost:3000/health", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if hits.control != 1 || hits.proxy != 0 {
		t.Errorf("hits = %+v, want {control:1,proxy:0}", hits)
	}
}

func TestRouterRoutesToProxyViaHeader(t *testing.T) {
	var hits struct{ control, proxy int }
	ctrl := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { hits.control++ })
	prox := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { hits.proxy++ })
	h := NewRouter(ctrl, prox, nil)

	req := httptest.NewRequest("POST", "http://localhost:3000/process.Process/Start", nil)
	req.Header.Set(HeaderSandboxID, "isb_abc")
	req.Header.Set(HeaderSandboxPort, "49983")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if hits.control != 0 || hits.proxy != 1 {
		t.Errorf("hits = %+v, want {control:0,proxy:1}", hits)
	}
}

func TestRouterRoutesToProxyViaHost(t *testing.T) {
	var seen *http.Request
	ctrl := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { t.Error("should not hit control") })
	prox := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r })
	h := NewRouter(ctrl, prox, nil)

	req := httptest.NewRequest("GET", "http://49983-isb_host.localhost:3000/files?path=/etc", nil)
	req.Host = "49983-isb_host.localhost:3000"
	h.ServeHTTP(httptest.NewRecorder(), req)

	if seen == nil {
		t.Fatal("proxy handler was not called")
	}
	if got := seen.Header.Get(HeaderSandboxID); got != "isb_host" {
		t.Errorf("sandbox id header = %q, want isb_host", got)
	}
	if got := seen.Header.Get(HeaderSandboxPort); got != "49983" {
		t.Errorf("sandbox port header = %q, want 49983", got)
	}
}

func TestRouterHeaderWinsOverHost(t *testing.T) {
	var seen *http.Request
	ctrl := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { t.Error("should not hit control") })
	prox := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r })
	h := NewRouter(ctrl, prox, nil)

	req := httptest.NewRequest("GET", "http://49999-isb_host.localhost:3000/files", nil)
	req.Host = "49999-isb_host.localhost:3000"
	req.Header.Set(HeaderSandboxID, "isb_header")
	req.Header.Set(HeaderSandboxPort, "49983")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if seen == nil {
		t.Fatal("proxy handler was not called")
	}
	if got := seen.Header.Get(HeaderSandboxID); got != "isb_header" {
		t.Errorf("expected explicit header to win: got %q", got)
	}
	if got := seen.Header.Get(HeaderSandboxPort); got != "49983" {
		t.Errorf("expected explicit header to win: got %q", got)
	}
}
