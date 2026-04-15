package control

import (
	"context"
	"net/http"
	"strings"

	"github.com/contember/edvabe/internal/api"
	"github.com/contember/edvabe/internal/runtime"
	"github.com/contember/edvabe/internal/sandbox"
)

type sandboxManager interface {
	Create(ctx context.Context, opts sandbox.CreateOptions) (*sandbox.Sandbox, error)
	Get(id string) (*sandbox.Sandbox, error)
	Domain() string
}

type versionProvider interface {
	Version() string
}

// NewRouter returns the Task 10 control-plane router: health, create, get.
func NewRouter(manager sandboxManager, rt runtime.Runtime, provider versionProvider) http.Handler {
	createGet := api.RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			createSandbox(manager, provider, w, r)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			getSandbox(manager, rt, provider, w, r)
		default:
			http.NotFound(w, r)
		}
	}))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			health(w, r)
		case r.URL.Path == "/sandboxes" || strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			createGet.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}
