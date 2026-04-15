package control

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/contember/edvabe/internal/api"
	"github.com/contember/edvabe/internal/runtime"
	"github.com/contember/edvabe/internal/sandbox"
	"github.com/contember/edvabe/internal/template/filecache"
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

// RouterOptions bundles the handlers' collaborators. Templates is
// optional — passing nil disables the template endpoints, which lets
// Phase 1 deployments continue to run with only the sandbox routes.
type RouterOptions struct {
	Manager  sandboxManager
	Runtime  runtime.Runtime
	Provider versionProvider
	// Templates is the Phase 3 template store (internal/template.Store
	// or a fake in tests). Pass nil to serve only the Phase 1 routes.
	Templates templateStore
	// FileCache and FileSigner power the SDK's Template.build() file
	// upload path. Both must be set together; if either is nil, the
	// file endpoints are skipped.
	FileCache  *filecache.Cache
	FileSigner *filecache.Signer
	// PublicBase is the externally reachable base URL of the edvabe
	// listener (e.g. "http://localhost:3000"). Used when minting
	// upload URLs. If empty, falls back to the inbound Host header.
	PublicBase string
}

// NewRouter returns the control-plane router. When Templates is set
// in RouterOptions, the /templates, /v3/templates, and
// /v2/templates/{id} routes are wired alongside the sandbox routes.
func NewRouter(opts RouterOptions) http.Handler {
	sandboxHandler := api.RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			createSandbox(opts.Manager, opts.Provider, w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/sandboxes":
			listSandboxes(opts.Manager, opts.Provider, w, r)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			getSandbox(opts.Manager, opts.Runtime, opts.Provider, w, r)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			deleteSandbox(opts.Manager, w, r)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/timeout"):
			setSandboxTimeout(opts.Manager, w, r)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/connect"):
			connectSandbox(opts.Manager, opts.Provider, w, r)
		default:
			http.NotFound(w, r)
		}
	}))

	filesEnabled := opts.FileCache != nil && opts.FileSigner != nil
	templateHandler := api.RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if opts.Templates == nil {
			http.NotFound(w, r)
			return
		}
		switch {
		case r.Method == http.MethodPost && (r.URL.Path == "/v3/templates" || r.URL.Path == "/templates"):
			createTemplate(opts.Templates, w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/templates":
			listTemplates(opts.Templates, w, r)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/templates/aliases/"):
			resolveAlias(opts.Templates, w, r)
		case r.Method == http.MethodGet && filesEnabled && isTemplateFilesPath(r.URL.Path):
			getFileUploadLink(opts.Templates, opts.FileCache, opts.FileSigner, opts.PublicBase, w, r)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/templates/"):
			getTemplate(opts.Templates, w, r)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/templates/"):
			deleteTemplate(opts.Templates, w, r)
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/v2/templates/"):
			patchTemplate(opts.Templates, w, r)
		default:
			http.NotFound(w, r)
		}
	}))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			health(w, r)
		case r.URL.Path == "/sandboxes" || r.URL.Path == "/v2/sandboxes" || strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			sandboxHandler.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/_upload/"):
			if !filesEnabled {
				http.NotFound(w, r)
				return
			}
			// Deliberately bypasses RequireAPIKey — the HMAC token in
			// the query string is the auth on this path.
			uploadFile(opts.FileCache, opts.FileSigner, w, r)
		case r.URL.Path == "/templates" || r.URL.Path == "/v3/templates" ||
			strings.HasPrefix(r.URL.Path, "/templates/") || strings.HasPrefix(r.URL.Path, "/v2/templates/"):
			templateHandler.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// isTemplateFilesPath reports whether path looks like
// /templates/{id}/files/{hash}.
func isTemplateFilesPath(path string) bool {
	if !strings.HasPrefix(path, "/templates/") {
		return false
	}
	rest := strings.TrimPrefix(path, "/templates/")
	parts := strings.SplitN(rest, "/", 3)
	return len(parts) == 3 && parts[1] == "files" && parts[0] != "" && parts[2] != ""
}
