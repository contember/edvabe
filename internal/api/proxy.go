package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/contember/edvabe/internal/sandbox"
)

// SandboxLookup is the subset of sandbox.Manager the proxy needs.
// Declaring it as an interface here lets tests inject a fake lookup
// without constructing a full Manager, and documents the minimal
// contract required for future proxy backends.
type SandboxLookup interface {
	Get(id string) (*sandbox.Sandbox, error)
}

// AgentResolver turns a sandbox ID into the host:port of its in-sandbox
// agent. Satisfied by runtime.Runtime in production.
type AgentResolver interface {
	AgentEndpoint(sandboxID string) (host string, port int, err error)
}

// NewProxy returns an http.Handler that forwards requests carrying an
// E2b-Sandbox-Id header to the in-sandbox agent. The handler:
//
//   - looks the sandbox up via SandboxLookup (404 if unknown)
//   - resolves the agent endpoint via AgentResolver (500 on failure)
//   - reverse-proxies the request with FlushInterval: -1 so Connect-RPC
//     server-stream frames reach the client immediately rather than
//     being buffered until the response closes.
//
// Hop-by-hop header stripping is handled by httputil.ReverseProxy's
// own transport layer — we don't need to touch Connection/Upgrade/etc.
// explicitly. The request body is never read by edvabe; it flows
// through as a stream.
func NewProxy(lookup SandboxLookup, resolver AgentResolver) http.Handler {
	rp := &httputil.ReverseProxy{
		// Negative flush interval = flush after every write. Required
		// for Connect-RPC server streams and NDJSON responses.
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			target := pr.In.Context().Value(proxyTargetKey{}).(*url.URL)
			pr.SetURL(target)
			pr.Out.Host = target.Host
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeErrorEnvelope(w, http.StatusBadGateway, fmt.Sprintf("upstream: %v", err))
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderSandboxID)
		if id == "" {
			writeErrorEnvelope(w, http.StatusBadRequest, "missing E2b-Sandbox-Id header")
			return
		}

		if _, err := lookup.Get(id); err != nil {
			if errors.Is(err, sandbox.ErrNotFound) {
				writeErrorEnvelope(w, http.StatusNotFound, fmt.Sprintf("sandbox %q not found", id))
				return
			}
			writeErrorEnvelope(w, http.StatusInternalServerError, err.Error())
			return
		}

		host, port, err := resolver.AgentEndpoint(id)
		if err != nil {
			writeErrorEnvelope(w, http.StatusInternalServerError,
				fmt.Sprintf("agent endpoint for %q: %v", id, err))
			return
		}

		target, err := url.Parse(fmt.Sprintf("http://%s:%d", host, port))
		if err != nil {
			writeErrorEnvelope(w, http.StatusInternalServerError,
				fmt.Sprintf("invalid target URL for %q: %v", id, err))
			return
		}

		ctx := context.WithValue(r.Context(), proxyTargetKey{}, target)
		rp.ServeHTTP(w, r.WithContext(ctx))
	})
}

// proxyTargetKey is the context-key type for passing a resolved target
// URL from the outer handler into the shared ReverseProxy's Rewrite
// callback. Unexported so no other package can collide on the key.
type proxyTargetKey struct{}

// writeErrorEnvelope writes a minimal E2B-shaped `{code, message}` JSON
// error body. Task 10 will consolidate this into a shared helper in
// internal/api/errors.go and every handler will use that; for now this
// stays local to the proxy.
func writeErrorEnvelope(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    code,
		"message": message,
	})
}
