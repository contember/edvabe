package api

import (
	"net/http"
	"strings"
)

const (
	// HeaderSandboxID is the header the E2B SDKs send on every
	// data-plane request. Carries the sandbox ID the reverse proxy
	// should forward to.
	HeaderSandboxID = "E2b-Sandbox-Id"
	// HeaderSandboxPort identifies which in-sandbox service the SDK
	// wants to talk to (49983 = envd, 49999 = code interpreter overlay,
	// anything else = user service). Phase 1 only routes envd.
	HeaderSandboxPort = "E2b-Sandbox-Port"
)

// NewRouter dispatches incoming requests to either the control-plane
// handler or the sandbox reverse proxy.
//
// A request belongs to the proxy if it carries E2b-Sandbox-Id in the
// headers or if its Host header has the "<port>-<id>.<domain>" layout
// that the SDKs fall back to when headers aren't an option (legacy
// clients, browser-side code). If we resolve the sandbox from the host,
// the headers are populated so the proxy handler doesn't have to
// re-parse the host. Requests with neither go to the control plane
// unchanged.
//
// onActivity, if non-nil, is called for every data-plane request
// (before proxying) so the sandbox manager can stamp LastActiveAt
// and reset the idle-TTL countdown.
func NewRouter(control, proxy, dashboard http.Handler, onActivity func(string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dashboard != nil && strings.HasPrefix(r.URL.Path, "/dashboard") {
			dashboard.ServeHTTP(w, r)
			return
		}

		id := r.Header.Get(HeaderSandboxID)

		if id == "" {
			if p, sid, ok := parseHost(r.Host); ok {
				id = sid
				r.Header.Set(HeaderSandboxID, sid)
				r.Header.Set(HeaderSandboxPort, p)
			}
		}

		if id == "" {
			control.ServeHTTP(w, r)
			return
		}
		if onActivity != nil {
			onActivity(id)
		}
		proxy.ServeHTTP(w, r)
	})
}
