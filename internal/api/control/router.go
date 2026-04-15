package control

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/contember/edvabe/internal/api"
	"github.com/contember/edvabe/internal/runtime"
	"github.com/contember/edvabe/internal/sandbox"
)

type sandboxManager interface {
	Create(ctx context.Context, opts sandbox.CreateOptions) (*sandbox.Sandbox, error)
	Get(id string) (*sandbox.Sandbox, error)
	List() []*sandbox.Sandbox
	Destroy(ctx context.Context, id string) error
	SetTimeout(id string, timeout time.Duration) error
	Connect(ctx context.Context, id string, timeout time.Duration) (*sandbox.Sandbox, error)
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
		case r.Method == http.MethodGet && r.URL.Path == "/v2/sandboxes":
			listSandboxes(manager, provider, w, r)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			getSandbox(manager, rt, provider, w, r)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			deleteSandbox(manager, w, r)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/timeout"):
			setSandboxTimeout(manager, w, r)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/connect"):
			connectSandbox(manager, provider, w, r)
		default:
			http.NotFound(w, r)
		}
	}))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			health(w, r)
		case r.URL.Path == "/sandboxes" || r.URL.Path == "/v2/sandboxes" || strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			createGet.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}
