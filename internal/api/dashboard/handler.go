// Package dashboard serves an embedded HTML overview of running
// sandboxes and templates. Intended for local development — no auth
// required, consistent with the /health endpoint.
package dashboard

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/contember/edvabe/internal/sandbox"
	"github.com/contember/edvabe/internal/template"
)

//go:embed dashboard.html
var indexHTML []byte

type sandboxManager interface {
	List() []*sandbox.Sandbox
	Destroy(ctx context.Context, id string) error
}

type templateStore interface {
	List() []*template.Template
}

// HandlerOptions configures the dashboard handler. Templates is
// optional — when nil the dashboard omits the template section.
type HandlerOptions struct {
	Manager   sandboxManager
	Templates templateStore
}

// NewHandler returns an http.Handler that serves the dashboard UI
// and its backing API under the /dashboard prefix.
func NewHandler(opts HandlerOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && (r.URL.Path == "/dashboard" || r.URL.Path == "/dashboard/"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(indexHTML)
		case r.Method == http.MethodGet && r.URL.Path == "/dashboard/api/overview":
			serveOverview(opts, w, r)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/dashboard/api/sandboxes/"):
			serveDeleteSandbox(opts.Manager, w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

type overviewResponse struct {
	Sandboxes []overviewSandbox  `json:"sandboxes"`
	Templates []overviewTemplate `json:"templates"`
	Summary   overviewSummary    `json:"summary"`
}

type overviewSandbox struct {
	ID         string            `json:"id"`
	TemplateID string            `json:"templateID"`
	Alias      string            `json:"alias,omitempty"`
	State      sandbox.State     `json:"state"`
	CreatedAt  string            `json:"createdAt"`
	ExpiresAt  string            `json:"expiresAt"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type overviewTemplate struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Alias             string `json:"alias,omitempty"`
	ImageTag          string `json:"imageTag,omitempty"`
	CreatedAt         string `json:"createdAt"`
	LatestBuildStatus string `json:"latestBuildStatus,omitempty"`
}

type overviewSummary struct {
	TotalSandboxes int `json:"totalSandboxes"`
	Running        int `json:"running"`
	Paused         int `json:"paused"`
	TotalTemplates int `json:"totalTemplates"`
}

func serveOverview(opts HandlerOptions, w http.ResponseWriter, r *http.Request) {
	list := opts.Manager.List()

	resp := overviewResponse{
		Sandboxes: make([]overviewSandbox, 0, len(list)),
	}

	for _, s := range list {
		resp.Sandboxes = append(resp.Sandboxes, overviewSandbox{
			ID:         s.ID,
			TemplateID: s.TemplateID,
			Alias:      s.Alias,
			State:      s.State,
			CreatedAt:  s.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			ExpiresAt:  s.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"),
			Metadata:   s.Metadata,
		})
		switch s.State {
		case sandbox.StateRunning:
			resp.Summary.Running++
		case sandbox.StatePaused:
			resp.Summary.Paused++
		}
	}
	resp.Summary.TotalSandboxes = len(list)

	if opts.Templates != nil {
		tpls := opts.Templates.List()
		resp.Templates = make([]overviewTemplate, 0, len(tpls))
		for _, t := range tpls {
			ot := overviewTemplate{
				ID:        t.ID,
				Name:      t.Name,
				Alias:     t.Alias,
				ImageTag:  t.ImageTag,
				CreatedAt: t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			}
			if b := t.LatestReady(); b != nil {
				ot.LatestBuildStatus = string(b.Status)
			} else if len(t.Builds) > 0 {
				ot.LatestBuildStatus = string(t.Builds[len(t.Builds)-1].Status)
			}
			resp.Templates = append(resp.Templates, ot)
		}
		resp.Summary.TotalTemplates = len(tpls)
	} else {
		resp.Templates = []overviewTemplate{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func serveDeleteSandbox(mgr sandboxManager, w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/dashboard/api/sandboxes/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	if err := mgr.Destroy(r.Context(), id); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
