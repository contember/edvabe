// Package dashboard serves an embedded HTML overview of running
// sandboxes and templates. Intended for local development — no auth
// required, consistent with the /health endpoint.
package dashboard

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/contember/edvabe/internal/runtime"
	"github.com/contember/edvabe/internal/sandbox"
	"github.com/contember/edvabe/internal/template"
)

//go:embed dashboard.html
var indexHTML []byte

type sandboxManager interface {
	List() []*sandbox.Sandbox
	Destroy(ctx context.Context, id string) error
	Pause(ctx context.Context, id string) error
	Stop(ctx context.Context, id string) error
	Resume(ctx context.Context, id string) error
	PausePolicy() sandbox.PausePolicy
}

// statsProvider is the slice of runtime.Runtime the dashboard needs for
// per-sandbox memory / CPU reporting. Keeping the interface narrow so
// tests can plug in a fake without dragging in the whole runtime.
type statsProvider interface {
	Stats(ctx context.Context, sandboxID string) (*runtime.Stats, error)
}

type templateStore interface {
	List() []*template.Template
}

// HandlerOptions configures the dashboard handler. Templates is
// optional — when nil the dashboard omits the template section.
// Runtime is optional — without it, per-sandbox memory/CPU stats are
// omitted but the UI still works.
type HandlerOptions struct {
	Manager   sandboxManager
	Runtime   statsProvider
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
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/dashboard/api/sandboxes/"):
			serveSandboxAction(opts.Manager, w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

type overviewResponse struct {
	Sandboxes   []overviewSandbox  `json:"sandboxes"`
	Templates   []overviewTemplate `json:"templates"`
	Summary     overviewSummary    `json:"summary"`
	PausePolicy overviewPolicy     `json:"pausePolicy"`
}

type overviewSandbox struct {
	ID             string            `json:"id"`
	TemplateID     string            `json:"templateID"`
	Alias          string            `json:"alias,omitempty"`
	State          sandbox.State     `json:"state"`
	PauseMode      sandbox.PauseMode `json:"pauseMode,omitempty"`
	PausedAt       string            `json:"pausedAt,omitempty"`
	CreatedAt      string            `json:"createdAt"`
	ExpiresAt      string            `json:"expiresAt"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	CPUUsedPercent float64           `json:"cpuUsedPercent,omitempty"`
	MemoryUsedMiB  int64             `json:"memoryUsedMiB,omitempty"`
	MemoryLimitMiB int64             `json:"memoryLimitMiB,omitempty"`
	DiskUsedMiB    int64             `json:"diskUsedMiB,omitempty"`
}

// overviewPolicy mirrors sandbox.PausePolicy but renders durations as
// seconds so the dashboard can format them client-side without parsing
// Go duration strings.
type overviewPolicy struct {
	FreezeDurationSec int64 `json:"freezeDurationSec"`
	MaxFrozen         int   `json:"maxFrozen"`
	StoppedGCAfterSec int64 `json:"stoppedGCAfterSec"`
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
	TotalSandboxes     int   `json:"totalSandboxes"`
	Running            int   `json:"running"`
	Paused             int   `json:"paused"`
	Frozen             int   `json:"frozen"`
	Stopped            int   `json:"stopped"`
	TotalMemoryUsedMiB int64 `json:"totalMemoryUsedMiB"`
	TotalTemplates     int   `json:"totalTemplates"`
}

func serveOverview(opts HandlerOptions, w http.ResponseWriter, r *http.Request) {
	list := opts.Manager.List()

	resp := overviewResponse{
		Sandboxes: make([]overviewSandbox, 0, len(list)),
	}

	for _, s := range list {
		entry := overviewSandbox{
			ID:         s.ID,
			TemplateID: s.TemplateID,
			Alias:      s.Alias,
			State:      s.State,
			PauseMode:  s.PauseMode,
			CreatedAt:  s.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			ExpiresAt:  s.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"),
			Metadata:   s.Metadata,
		}
		if !s.PausedAt.IsZero() {
			entry.PausedAt = s.PausedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		// Stats only make sense for live containers — stopped
		// sandboxes have no cgroup to read.
		if opts.Runtime != nil && !(s.State == sandbox.StatePaused && s.PauseMode == sandbox.PauseStopped) {
			if stats, err := opts.Runtime.Stats(r.Context(), s.ID); err == nil && stats != nil {
				entry.CPUUsedPercent = stats.CPUUsedPercent
				entry.MemoryUsedMiB = stats.MemoryUsedMB
				entry.MemoryLimitMiB = stats.MemoryLimitMB
				entry.DiskUsedMiB = stats.DiskUsedMB
				resp.Summary.TotalMemoryUsedMiB += stats.MemoryUsedMB
			}
		}
		resp.Sandboxes = append(resp.Sandboxes, entry)
		switch s.State {
		case sandbox.StateRunning:
			resp.Summary.Running++
		case sandbox.StatePaused:
			resp.Summary.Paused++
			switch s.PauseMode {
			case sandbox.PauseFrozen:
				resp.Summary.Frozen++
			case sandbox.PauseStopped:
				resp.Summary.Stopped++
			}
		}
	}
	resp.Summary.TotalSandboxes = len(list)

	policy := opts.Manager.PausePolicy()
	resp.PausePolicy = overviewPolicy{
		FreezeDurationSec: int64(policy.FreezeDuration.Seconds()),
		MaxFrozen:         policy.MaxFrozen,
		StoppedGCAfterSec: int64(policy.StoppedGCAfter.Seconds()),
	}

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
		writeActionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// serveSandboxAction dispatches POST /dashboard/api/sandboxes/{id}/{action}.
// Actions beyond the E2B SDK surface: pause (freeze), stop (force demote
// to docker stop), resume (leave TTL alone, unlike /connect).
func serveSandboxAction(mgr sandboxManager, w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/dashboard/api/sandboxes/")
	id, action, ok := strings.Cut(rest, "/")
	if !ok || id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	var err error
	switch action {
	case "pause":
		err = mgr.Pause(r.Context(), id)
	case "stop":
		err = mgr.Stop(r.Context(), id)
	case "resume":
		err = mgr.Resume(r.Context(), id)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeActionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeActionError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, sandbox.ErrNotFound) {
		status = http.StatusNotFound
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
