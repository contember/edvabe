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
	Pause(ctx context.Context, id string) error
	Snapshot(ctx context.Context, id, name string) (*sandbox.SnapshotInfo, error)
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
	// Builds is the async template BuildManager. Required only for
	// the build start/status/logs endpoints; CRUD-only deployments
	// can leave it nil.
	Builds buildManager
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
	// In-memory stores for Phase 4 stubs. Created once per server
	// lifetime — no persistence across restarts.
	apiKeys := newAPIKeyStore()
	accessTokens := newAccessTokenStore()
	volumes := newVolumeStore()

	sandboxHandler := api.RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			createSandbox(opts.Manager, opts.Provider, w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/sandboxes":
			listSandboxes(opts.Manager, opts.Provider, w, r)

		// GET /sandboxes/metrics is batch metrics — must be matched
		// before the /sandboxes/{id} wildcard.
		case r.Method == http.MethodGet && r.URL.Path == "/sandboxes/metrics":
			getBatchSandboxMetrics(opts.Manager, opts.Runtime, w, r)

		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/metrics") && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			getSandboxMetrics(opts.Manager, opts.Runtime, w, r)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/logs") && strings.HasPrefix(r.URL.Path, "/v2/sandboxes/"):
			getSandboxLogs(opts.Manager, w, r)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			getSandbox(opts.Manager, opts.Runtime, opts.Provider, w, r)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			deleteSandbox(opts.Manager, w, r)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/timeout"):
			setSandboxTimeout(opts.Manager, w, r)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/connect"):
			connectSandbox(opts.Manager, opts.Provider, w, r)
		case r.Method == http.MethodPost && (strings.HasSuffix(r.URL.Path, "/pause") || strings.HasSuffix(r.URL.Path, "/resume")):
			if strings.HasSuffix(r.URL.Path, "/pause") {
				pauseSandbox(opts.Manager, w, r)
			} else {
				resumeSandbox(opts.Manager, opts.Provider, w, r)
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/snapshots"):
			snapshotSandbox(opts.Manager, w, r)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/refreshes"):
			refreshSandbox(opts.Manager, w, r)
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/network"):
			updateSandboxNetwork(opts.Manager, w, r)
		default:
			http.NotFound(w, r)
		}
	}))

	filesEnabled := opts.FileCache != nil && opts.FileSigner != nil
	buildsEnabled := opts.Builds != nil
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
		case r.Method == http.MethodPost && buildsEnabled && isV2TemplateBuildPath(r.URL.Path):
			startBuild(opts.Templates, opts.Builds, w, r)
		case r.Method == http.MethodGet && buildsEnabled && isTemplateBuildStatusPath(r.URL.Path):
			getBuildStatus(opts.Builds, w, r)
		case r.Method == http.MethodGet && buildsEnabled && isTemplateBuildLogsPath(r.URL.Path):
			getBuildLogs(opts.Builds, w, r)
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

	// Phase 4 stub handler — teams, api-keys, access-tokens, volumes,
	// snapshots, nodes, admin. All behind RequireAPIKey.
	stubHandler := api.RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// Teams
		case r.Method == http.MethodGet && r.URL.Path == "/teams":
			listTeams(w, r)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/metrics/max") && strings.HasPrefix(r.URL.Path, "/teams/"):
			getTeamMetricsMax(w, r)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/metrics") && strings.HasPrefix(r.URL.Path, "/teams/"):
			getTeamMetrics(w, r)

		// API keys
		case r.Method == http.MethodGet && r.URL.Path == "/api-keys":
			apiKeys.list(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/api-keys":
			apiKeys.create(w, r)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api-keys/"):
			apiKeys.delete(w, r)

		// Access tokens
		case r.Method == http.MethodPost && r.URL.Path == "/access-tokens":
			accessTokens.create(w, r)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/access-tokens/"):
			accessTokens.delete(w, r)

		// Volumes
		case r.Method == http.MethodGet && r.URL.Path == "/volumes":
			volumes.list(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/volumes":
			volumes.create(w, r)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/volumes/"):
			volumes.get(w, r)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/volumes/"):
			volumes.delete(w, r)

		// Snapshots list
		case r.Method == http.MethodGet && r.URL.Path == "/snapshots":
			listSnapshots(w, r)

		// Nodes
		case r.Method == http.MethodGet && r.URL.Path == "/nodes":
			listNodes(w, r)

		// Admin catch-all
		case strings.HasPrefix(r.URL.Path, "/admin"):
			adminStub(w, r)

		default:
			http.NotFound(w, r)
		}
	}))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			health(w, r)
		case r.URL.Path == "/sandboxes" || r.URL.Path == "/v2/sandboxes" ||
			strings.HasPrefix(r.URL.Path, "/sandboxes/") ||
			strings.HasPrefix(r.URL.Path, "/v2/sandboxes/"):
			sandboxHandler.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/_upload/"):
			if !filesEnabled {
				http.NotFound(w, r)
				return
			}
			uploadFile(opts.FileCache, opts.FileSigner, w, r)
		case r.URL.Path == "/templates" || r.URL.Path == "/v3/templates" ||
			strings.HasPrefix(r.URL.Path, "/templates/") || strings.HasPrefix(r.URL.Path, "/v2/templates/"):
			templateHandler.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/teams") ||
			strings.HasPrefix(r.URL.Path, "/api-keys") ||
			strings.HasPrefix(r.URL.Path, "/access-tokens") ||
			strings.HasPrefix(r.URL.Path, "/volumes") ||
			r.URL.Path == "/snapshots" ||
			r.URL.Path == "/nodes" ||
			strings.HasPrefix(r.URL.Path, "/admin"):
			stubHandler.ServeHTTP(w, r)
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

// isV2TemplateBuildPath reports whether path looks like
// /v2/templates/{id}/builds/{bid} (no trailing segment).
func isV2TemplateBuildPath(path string) bool {
	if !strings.HasPrefix(path, "/v2/templates/") {
		return false
	}
	rest := strings.TrimPrefix(path, "/v2/templates/")
	parts := strings.SplitN(rest, "/", 4)
	return len(parts) == 3 && parts[1] == "builds" && parts[0] != "" && parts[2] != ""
}

// isTemplateBuildStatusPath reports whether path is
// /templates/{id}/builds/{bid}/status.
func isTemplateBuildStatusPath(path string) bool {
	return matchBuildSubpath(path, "status")
}

// isTemplateBuildLogsPath reports whether path is
// /templates/{id}/builds/{bid}/logs.
func isTemplateBuildLogsPath(path string) bool {
	return matchBuildSubpath(path, "logs")
}

func matchBuildSubpath(path, suffix string) bool {
	if !strings.HasPrefix(path, "/templates/") {
		return false
	}
	rest := strings.TrimPrefix(path, "/templates/")
	parts := strings.Split(rest, "/")
	return len(parts) == 4 && parts[1] == "builds" && parts[3] == suffix && parts[0] != "" && parts[2] != ""
}
