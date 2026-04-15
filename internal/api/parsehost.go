// Package api holds the HTTP-facing glue for edvabe: dispatch router,
// reverse proxy, and (later) the control-plane handlers.
package api

import (
	"strconv"
	"strings"
)

// parseHost extracts sandbox port and ID from a Host header of the form
// "<port>-<id>.<domain>" — the layout the E2B SDKs target when building
// data-plane URLs for a sandbox. Returns ok=false if the host has no
// dot (no subdomain), no hyphen in the subdomain, an empty ID, or a
// non-numeric port.
//
// Modeled on e2b-infra/packages/shared/pkg/proxy/host.go:41-66. The
// upstream version returns an error type; edvabe collapses the result
// into a bool because the dispatcher's only decision is "route to
// control plane or route to proxy?" — we never surface the error to
// the client.
func parseHost(host string) (port, id string, ok bool) {
	dot := strings.Index(host, ".")
	if dot == -1 {
		return "", "", false
	}
	subdomain := host[:dot]

	parts := strings.SplitN(subdomain, "-", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	if _, err := strconv.Atoi(parts[0]); err != nil {
		return "", "", false
	}
	return parts[0], parts[1], true
}
