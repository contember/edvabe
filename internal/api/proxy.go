package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"

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
			WriteError(w, http.StatusBadGateway, fmt.Sprintf("upstream: %v", err))
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderSandboxID)
		if id == "" {
			WriteError(w, http.StatusBadRequest, "missing E2b-Sandbox-Id header")
			return
		}

		if _, err := lookup.Get(id); err != nil {
			if errors.Is(err, sandbox.ErrNotFound) {
				WriteError(w, http.StatusNotFound, fmt.Sprintf("sandbox %q not found", id))
				return
			}
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		host, port, err := resolver.AgentEndpoint(id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError,
				fmt.Sprintf("agent endpoint for %q: %v", id, err))
			return
		}

		// The SDK sends E2b-Sandbox-Port to select which in-sandbox
		// service to talk to (49983 = envd, 49999 = code interpreter,
		// anything else = user service). Override the agent port when
		// the header carries a valid, different port number.
		if portHeader := r.Header.Get(HeaderSandboxPort); portHeader != "" {
			if p, err := strconv.Atoi(portHeader); err == nil && p > 0 && p <= 65535 {
				port = p
			}
		}

		target, err := url.Parse(fmt.Sprintf("http://%s:%d", host, port))
		if err != nil {
			WriteError(w, http.StatusInternalServerError,
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
