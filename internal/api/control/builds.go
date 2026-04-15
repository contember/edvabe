package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/contember/edvabe/internal/api"
	"github.com/contember/edvabe/internal/template"
	"github.com/contember/edvabe/internal/template/builder"
)

// buildManager is the subset of builder.Manager the handlers depend
// on. Wrapping it in an interface keeps the HTTP layer test-friendly:
// tests can drive status/logs transitions with a fake manager without
// spinning up goroutines.
type buildManager interface {
	Enqueue(ctx context.Context, req builder.EnqueueSpec) error
	Status(buildID string) (builder.Status, error)
	Logs(buildID string, offset int64, limit int) ([]builder.LogEntry, int64, error)
}

// buildStartRequest decodes the SDK's TemplateBuildStartV2 body. The
// `steps` field is passed through verbatim — the translator lives in
// internal/template/builder and only runs once the executor picks the
// build up.
type buildStartRequest struct {
	FromImage         string                `json:"fromImage,omitempty"`
	FromTemplate      string                `json:"fromTemplate,omitempty"`
	FromImageRegistry *template.RegistryAuth `json:"fromImageRegistry,omitempty"`
	Steps             []template.Step       `json:"steps,omitempty"`
	StartCmd          string                `json:"startCmd,omitempty"`
	ReadyCmd          string                `json:"readyCmd,omitempty"`
	Force             bool                  `json:"force,omitempty"`
}

// buildStatusResponse matches the SDK's BuildStatusResponse shape
// from test/e2e/ts/node_modules/e2b/dist/index.d.ts — includes both
// the summary fields and the incrementally-paginated log chunk the
// SDK reads via logsOffset.
type buildStatusResponse struct {
	BuildID    string             `json:"buildID"`
	TemplateID string             `json:"templateID"`
	Status     string             `json:"status"`
	Reason     *buildReason       `json:"reason,omitempty"`
	Logs       []string           `json:"logs"`
	LogEntries []buildLogEntryOut `json:"logEntries"`
}

type buildReason struct {
	Message    string             `json:"message"`
	Step       string             `json:"step,omitempty"`
	LogEntries []buildLogEntryOut `json:"logEntries,omitempty"`
}

type buildLogEntryOut struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
}

// startBuild handles POST /v2/templates/{id}/builds/{bid}. The SDK
// sends the TemplateBuildStartV2 body here after uploading all file
// contexts; we hand it off to the BuildManager to run asynchronously
// and return 202 Accepted immediately.
//
// The template store must know about the build ID already (it was
// pre-minted during POST /v3/templates), so the handler also updates
// the persisted build's status from waiting → building for the sake
// of GET /templates/{id} callers that don't go through the build
// status endpoint.
func startBuild(
	store templateStore,
	mgr buildManager,
	w http.ResponseWriter,
	r *http.Request,
) {
	templateID, buildID, ok := parseBuildPath(r.URL.Path, "/v2/templates/", "/builds/")
	if !ok {
		http.NotFound(w, r)
		return
	}

	tpl, err := store.Get(templateID)
	if err != nil {
		if errors.Is(err, template.ErrNotFound) {
			api.WriteError(w, http.StatusNotFound, "template not found")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var req buildStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.FromImage == "" && req.FromTemplate == "" {
		api.WriteError(w, http.StatusBadRequest, "fromImage or fromTemplate is required")
		return
	}

	// Resolve the parent image if fromTemplate is set. The parent
	// must have at least one ready build; otherwise the child build
	// has nothing to FROM.
	parentImage := ""
	if req.FromTemplate != "" {
		parent, err := store.ResolveNameOrID(req.FromTemplate)
		if err != nil {
			api.WriteError(w, http.StatusBadRequest, "fromTemplate does not resolve")
			return
		}
		if parent.LatestReady() == nil {
			api.WriteError(w, http.StatusConflict, "fromTemplate has no ready build")
			return
		}
		// The result-tag format in builder.Manager and this resolution
		// must stay in sync.
		parentImage = "edvabe/user-" + parent.ID + ":latest"
	}

	// Persist the start/ready commands on the template record — the
	// sandbox manager reads them later at create time.
	if _, err := store.UpdateMeta(tpl.ID, func(t *template.Template) {
		if req.StartCmd != "" {
			t.StartCmd = req.StartCmd
		}
		if req.ReadyCmd != "" {
			t.ReadyCmd = req.ReadyCmd
		}
		t.ImageTag = "edvabe/user-" + t.ID + ":latest"
	}); err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	spec := template.BuildSpec{
		FromImage:         req.FromImage,
		FromTemplate:      req.FromTemplate,
		FromImageRegistry: req.FromImageRegistry,
		Steps:             req.Steps,
		StartCmd:          req.StartCmd,
		ReadyCmd:          req.ReadyCmd,
		Force:             req.Force,
	}

	if err := mgr.Enqueue(r.Context(), builder.EnqueueSpec{
		TemplateID:  tpl.ID,
		BuildID:     buildID,
		Spec:        spec,
		ParentImage: parentImage,
	}); err != nil {
		api.WriteError(w, http.StatusConflict, err.Error())
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// getBuildStatus handles GET /templates/{id}/builds/{bid}/status and
// merges the BuildManager's status with the paginated log slice the
// SDK polls for. The `logsOffset` query param is absolute — the
// manager's ring buffer may have evicted older entries but
// snap-forward semantics mean stale cursors still get progress.
func getBuildStatus(mgr buildManager, w http.ResponseWriter, r *http.Request) {
	templateID, buildID, ok := parseBuildPath(r.URL.Path, "/templates/", "/builds/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	offset, err := parseOffset(r.URL.Query().Get("logsOffset"))
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid logsOffset")
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid limit")
		return
	}
	// Strip the trailing /status from the buildID.
	buildID = strings.TrimSuffix(buildID, "/status")

	status, err := mgr.Status(buildID)
	if err != nil {
		if errors.Is(err, builder.ErrBuildNotFound) {
			api.WriteError(w, http.StatusNotFound, "build not found")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if status.TemplateID != templateID {
		api.WriteError(w, http.StatusNotFound, "build not found")
		return
	}

	entries, _, err := mgr.Logs(buildID, offset, limit)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := buildStatusResponse{
		BuildID:    status.BuildID,
		TemplateID: status.TemplateID,
		Status:     string(status.Status),
		Logs:       messagesOnly(entries),
		LogEntries: toWireEntries(entries),
	}
	if status.Status == template.BuildStatusError {
		resp.Reason = &buildReason{Message: status.Reason}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// getBuildLogs handles the explicit GET /templates/{id}/builds/{bid}/logs
// endpoint. The SDK prefers the status endpoint, but the OpenAPI spec
// exposes logs separately and legacy clients hit this path. Wire
// format is just an array of log entries.
func getBuildLogs(mgr buildManager, w http.ResponseWriter, r *http.Request) {
	templateID, buildID, ok := parseBuildPath(r.URL.Path, "/templates/", "/builds/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	buildID = strings.TrimSuffix(buildID, "/logs")

	status, err := mgr.Status(buildID)
	if err != nil {
		if errors.Is(err, builder.ErrBuildNotFound) {
			api.WriteError(w, http.StatusNotFound, "build not found")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if status.TemplateID != templateID {
		api.WriteError(w, http.StatusNotFound, "build not found")
		return
	}

	offset, err := parseOffset(r.URL.Query().Get("cursor"))
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid cursor")
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid limit")
		return
	}
	entries, next, err := mgr.Logs(buildID, offset, limit)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Next-Cursor", strconv.FormatInt(next, 10))
	_ = json.NewEncoder(w).Encode(toWireEntries(entries))
}

// parseBuildPath splits paths like
// /templates/{id}/builds/{bid}/(status|logs) or
// /v2/templates/{id}/builds/{bid} into their two variables. The
// trailing segment after the build ID stays attached; callers strip
// it. Returns ok=false for malformed paths.
func parseBuildPath(path, templatePrefix, buildsSeparator string) (templateID, buildIDAndTail string, ok bool) {
	if !strings.HasPrefix(path, templatePrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, templatePrefix)
	idIdx := strings.Index(rest, buildsSeparator)
	if idIdx <= 0 {
		return "", "", false
	}
	templateID = rest[:idIdx]
	buildIDAndTail = rest[idIdx+len(buildsSeparator):]
	if strings.Contains(templateID, "/") || buildIDAndTail == "" {
		return "", "", false
	}
	return templateID, buildIDAndTail, true
}

func parseOffset(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("invalid offset")
	}
	return n, nil
}

func parseLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, errors.New("invalid limit")
	}
	return n, nil
}

func messagesOnly(entries []builder.LogEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Message)
	}
	return out
}

func toWireEntries(entries []builder.LogEntry) []buildLogEntryOut {
	out := make([]buildLogEntryOut, 0, len(entries))
	for _, e := range entries {
		out = append(out, buildLogEntryOut{
			Timestamp: e.Timestamp,
			Level:     e.Level,
			Message:   e.Message,
		})
	}
	return out
}
