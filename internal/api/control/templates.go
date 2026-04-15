package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/contember/edvabe/internal/api"
	"github.com/contember/edvabe/internal/template"
)

// templateStore is the subset of template.Store the control-plane
// handlers depend on. Declared here as an interface so tests can
// substitute an in-memory fake without depending on the JSON-backed
// implementation.
type templateStore interface {
	Create(opts template.CreateOptions) (*template.Template, error)
	Get(id string) (*template.Template, error)
	ResolveAlias(alias string) (*template.Template, error)
	ResolveNameOrID(idOrAlias string) (*template.Template, error)
	List() []*template.Template
	Delete(id string) error
	UpdateMeta(id string, mutator func(*template.Template)) (*template.Template, error)
	AppendBuild(templateID string, build template.Build) (*template.Build, error)
}

type templateBuildRequest struct {
	Name      string   `json:"name"`
	Tags      []string `json:"tags"`
	CPUCount  int      `json:"cpuCount"`
	MemoryMB  int      `json:"memoryMB"`
	SkipCache bool     `json:"skipCache"`
}

// templateBuildResponse matches the E2B SDK's TemplateRequestResponseV3
// shape — see test/e2e/ts/node_modules/e2b/dist/index.d.ts:2603. The
// SDK reads {templateID, buildID} off this, and the alias.name/alias
// fields exist for legacy SDK compatibility.
type templateBuildResponse struct {
	TemplateID string   `json:"templateID"`
	BuildID    string   `json:"buildID"`
	Names      []string `json:"names"`
	Tags       []string `json:"tags"`
	Aliases    []string `json:"aliases"`
	Public     bool     `json:"public"`
}

type templateListItem struct {
	TemplateID string    `json:"templateID"`
	Name       string    `json:"name"`
	Alias      string    `json:"alias,omitempty"`
	Aliases    []string  `json:"aliases"`
	Tags       []string  `json:"tags"`
	CPUCount   int       `json:"cpuCount,omitempty"`
	MemoryMB   int       `json:"memoryMB,omitempty"`
	Public     bool      `json:"public"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type templateBuildSummary struct {
	BuildID    string     `json:"buildID"`
	Status     string     `json:"status"`
	Reason     string     `json:"reason,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

type templateWithBuilds struct {
	templateListItem
	Builds []templateBuildSummary `json:"builds"`
}

type templateAliasResponse struct {
	TemplateID string `json:"templateID"`
	Public     bool   `json:"public"`
}

type templateUpdateRequest struct {
	Public *bool    `json:"public"`
	Tags   []string `json:"tags"`
}

// createTemplate handles POST /v3/templates. The request creates a
// template record and mints a buildID; the SDK then uploads file
// contexts by hash and fires POST /v2/templates/{id}/builds/{bid} with
// the step array. This handler does not itself kick off a build — the
// build lifecycle handlers live in builds.go (phase 3 task 8).
func createTemplate(store templateStore, w http.ResponseWriter, r *http.Request) {
	var req templateBuildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Name == "" {
		api.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}

	tpl, err := store.Create(template.CreateOptions{
		Name:     req.Name,
		Tags:     req.Tags,
		CPUCount: req.CPUCount,
		MemoryMB: req.MemoryMB,
	})
	if err != nil {
		if errors.Is(err, template.ErrAliasTaken) {
			api.WriteError(w, http.StatusConflict, "template name already in use")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Pre-mint a build ID so the SDK can immediately start uploading
	// file contexts without waiting for a second round trip. The
	// actual Build record is persisted in "waiting" state so the
	// status endpoint has something to return before the build start
	// handler runs.
	buildID := template.NewBuildID()
	_, err = store.AppendBuild(tpl.ID, template.Build{
		ID:        buildID,
		Status:    template.BuildStatusWaiting,
		StartedAt: time.Now(),
	})
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(templateBuildResponse{
		TemplateID: tpl.ID,
		BuildID:    buildID,
		Names:      []string{tpl.Name},
		Tags:       nonNilStrings(tpl.Tags),
		Aliases:    aliasSlice(tpl),
		Public:     tpl.Public,
	})
}

// listTemplates handles GET /templates. Returns a flat list — no
// pagination since local dev tooling rarely has more than a handful
// of templates.
func listTemplates(store templateStore, w http.ResponseWriter, r *http.Request) {
	list := store.List()
	out := make([]templateListItem, 0, len(list))
	for _, tpl := range list {
		out = append(out, toTemplateListItem(tpl))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// getTemplate handles GET /templates/{id}. Returns the full
// TemplateWithBuilds shape the SDK expects — including the build
// history so the SDK can display progress for any in-flight build.
func getTemplate(store templateStore, w http.ResponseWriter, r *http.Request) {
	id := templateIDFromPath(r.URL.Path, "/templates/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	tpl, err := store.ResolveNameOrID(id)
	if err != nil {
		if errors.Is(err, template.ErrNotFound) {
			api.WriteError(w, http.StatusNotFound, "template not found")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toTemplateWithBuilds(tpl))
}

// deleteTemplate handles DELETE /templates/{id}. The underlying docker
// image is NOT removed here — that's a deliberate choice so users can
// inspect or recover it. Only the metadata record goes away.
func deleteTemplate(store templateStore, w http.ResponseWriter, r *http.Request) {
	id := templateIDFromPath(r.URL.Path, "/templates/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	tpl, err := store.ResolveNameOrID(id)
	if err != nil {
		if errors.Is(err, template.ErrNotFound) {
			api.WriteError(w, http.StatusNotFound, "template not found")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := store.Delete(tpl.ID); err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// patchTemplate handles PATCH /v2/templates/{id}. The only mutable
// fields are `public` (stored but not enforced) and `tags`.
func patchTemplate(store templateStore, w http.ResponseWriter, r *http.Request) {
	id := templateIDFromPath(r.URL.Path, "/v2/templates/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	var req templateUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Resolve by ID-or-alias first so we have a concrete template ID
	// to hand to UpdateMeta.
	tpl, err := store.ResolveNameOrID(id)
	if err != nil {
		if errors.Is(err, template.ErrNotFound) {
			api.WriteError(w, http.StatusNotFound, "template not found")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := store.UpdateMeta(tpl.ID, func(t *template.Template) {
		if req.Public != nil {
			t.Public = *req.Public
		}
		if req.Tags != nil {
			t.Tags = append([]string(nil), req.Tags...)
		}
	})
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toTemplateListItem(updated))
}

// resolveAlias handles GET /templates/aliases/{alias}. The SDK calls
// this from `Sandbox.betaCreate(template, …)` to resolve a human-
// readable template name into a concrete template ID before the
// sandbox create request.
func resolveAlias(store templateStore, w http.ResponseWriter, r *http.Request) {
	alias := strings.TrimPrefix(r.URL.Path, "/templates/aliases/")
	if alias == "" || strings.Contains(alias, "/") {
		http.NotFound(w, r)
		return
	}
	tpl, err := store.ResolveAlias(alias)
	if err != nil {
		if errors.Is(err, template.ErrNotFound) {
			api.WriteError(w, http.StatusNotFound, "alias not found")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(templateAliasResponse{
		TemplateID: tpl.ID,
		Public:     tpl.Public,
	})
}

func templateIDFromPath(path, prefix string) string {
	trimmed := strings.TrimPrefix(path, prefix)
	if trimmed == "" || strings.Contains(trimmed, "/") {
		return ""
	}
	return trimmed
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func aliasSlice(tpl *template.Template) []string {
	if tpl.Alias == "" {
		return []string{}
	}
	return []string{tpl.Alias}
}

func toTemplateListItem(tpl *template.Template) templateListItem {
	updatedAt := tpl.CreatedAt
	if len(tpl.Builds) > 0 {
		last := tpl.Builds[len(tpl.Builds)-1]
		if last.FinishedAt != nil && last.FinishedAt.After(updatedAt) {
			updatedAt = *last.FinishedAt
		} else if last.StartedAt.After(updatedAt) {
			updatedAt = last.StartedAt
		}
	}
	return templateListItem{
		TemplateID: tpl.ID,
		Name:       tpl.Name,
		Alias:      tpl.Alias,
		Aliases:    aliasSlice(tpl),
		Tags:       nonNilStrings(tpl.Tags),
		CPUCount:   tpl.CPUCount,
		MemoryMB:   tpl.MemoryMB,
		Public:     tpl.Public,
		CreatedAt:  tpl.CreatedAt,
		UpdatedAt:  updatedAt,
	}
}

func toTemplateWithBuilds(tpl *template.Template) templateWithBuilds {
	builds := make([]templateBuildSummary, 0, len(tpl.Builds))
	for _, b := range tpl.Builds {
		builds = append(builds, templateBuildSummary{
			BuildID:    b.ID,
			Status:     string(b.Status),
			Reason:     b.Reason,
			StartedAt:  b.StartedAt,
			FinishedAt: b.FinishedAt,
		})
	}
	return templateWithBuilds{
		templateListItem: toTemplateListItem(tpl),
		Builds:           builds,
	}
}
