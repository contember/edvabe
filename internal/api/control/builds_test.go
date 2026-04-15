package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/contember/edvabe/internal/template"
	"github.com/contember/edvabe/internal/template/builder"
)

// fakeBuildManager is an in-memory buildManager that tests can drive
// directly. Enqueue transitions the build into a caller-chosen
// status; Logs/Status reflect whatever state the test set.
type fakeBuildManager struct {
	mu      sync.Mutex
	builds  map[string]*fakeBuild
	enqueue func(req builder.EnqueueSpec) error
}

type fakeBuild struct {
	status builder.Status
	logs   []builder.LogEntry
}

func newFakeBuildManager() *fakeBuildManager {
	return &fakeBuildManager{builds: make(map[string]*fakeBuild)}
}

func (f *fakeBuildManager) Enqueue(ctx context.Context, req builder.EnqueueSpec) error {
	if f.enqueue != nil {
		if err := f.enqueue(req); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.builds[req.BuildID] = &fakeBuild{
		status: builder.Status{
			TemplateID: req.TemplateID,
			BuildID:    req.BuildID,
			Status:     template.BuildStatusBuilding,
			StartedAt:  time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC),
		},
	}
	return nil
}

func (f *fakeBuildManager) Status(buildID string) (builder.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.builds[buildID]
	if !ok {
		return builder.Status{}, builder.ErrBuildNotFound
	}
	return b.status, nil
}

func (f *fakeBuildManager) Logs(buildID string, offset int64, limit int) ([]builder.LogEntry, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.builds[buildID]
	if !ok {
		return nil, 0, builder.ErrBuildNotFound
	}
	total := int64(len(b.logs))
	if offset >= total {
		return nil, total, nil
	}
	slice := b.logs[offset:]
	if limit > 0 && limit < len(slice) {
		slice = slice[:limit]
	}
	return append([]builder.LogEntry(nil), slice...), offset + int64(len(slice)), nil
}

func (f *fakeBuildManager) setStatus(buildID string, status template.BuildStatus, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.builds[buildID]
	if b == nil {
		return
	}
	b.status.Status = status
	b.status.Reason = reason
}

func (f *fakeBuildManager) appendLog(buildID string, entries ...builder.LogEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.builds[buildID]
	if b == nil {
		return
	}
	b.logs = append(b.logs, entries...)
}

func newBuildTestRouter(t *testing.T) (http.Handler, *template.Store, *fakeBuildManager) {
	t.Helper()
	store, _ := template.NewStore(template.Options{})
	mgr := newFakeBuildManager()
	h := NewRouter(RouterOptions{Templates: store, Builds: mgr})
	return h, store, mgr
}

func createTplAndBuildID(t *testing.T, h http.Handler) (string, string) {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/v3/templates", map[string]any{"name": "probe"})
	var resp templateBuildResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	return resp.TemplateID, resp.BuildID
}

func TestStartBuildHappyPath(t *testing.T) {
	h, store, mgr := newBuildTestRouter(t)
	tplID, buildID := createTplAndBuildID(t, h)

	rec := doJSON(t, h, http.MethodPost, fmt.Sprintf("/v2/templates/%s/builds/%s", tplID, buildID), map[string]any{
		"fromImage": "oven/bun:slim",
		"startCmd":  "sleep infinity",
		"readyCmd":  "true",
		"steps": []map[string]any{
			{"type": "RUN", "args": []string{"echo ok"}},
		},
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start build status=%d body=%s", rec.Code, rec.Body.String())
	}

	// The manager should have been called with the right IDs.
	if _, err := mgr.Status(buildID); err != nil {
		t.Fatalf("build not enqueued: %v", err)
	}
	// The template store should now carry startCmd/readyCmd/imageTag.
	tpl, _ := store.Get(tplID)
	if tpl.StartCmd != "sleep infinity" || tpl.ReadyCmd != "true" {
		t.Fatalf("start/ready not persisted: %+v", tpl)
	}
	if tpl.ImageTag != "edvabe/user-"+tplID+":latest" {
		t.Fatalf("image tag not persisted: %s", tpl.ImageTag)
	}
}

func TestStartBuildRejectsMissingFrom(t *testing.T) {
	h, _, _ := newBuildTestRouter(t)
	tplID, buildID := createTplAndBuildID(t, h)
	rec := doJSON(t, h, http.MethodPost, fmt.Sprintf("/v2/templates/%s/builds/%s", tplID, buildID), map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStartBuildTemplateNotFound(t *testing.T) {
	h, _, _ := newBuildTestRouter(t)
	rec := doJSON(t, h, http.MethodPost, "/v2/templates/tpl_missing/builds/bld_x", map[string]any{
		"fromImage": "alpine",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestStartBuildFromTemplateRequiresReadyParent(t *testing.T) {
	h, store, _ := newBuildTestRouter(t)
	// Create the parent template via the store so we can control its
	// build state. No ready build → child build should be rejected.
	parent, _ := store.Create(template.CreateOptions{Name: "parent"})
	child, _ := store.Create(template.CreateOptions{Name: "child"})
	childBuild, _ := store.AppendBuild(child.ID, template.Build{ID: "bld_child", Status: template.BuildStatusWaiting, StartedAt: time.Now()})

	rec := doJSON(t, h, http.MethodPost, fmt.Sprintf("/v2/templates/%s/builds/%s", child.ID, childBuild.ID), map[string]any{
		"fromTemplate": parent.ID,
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStartBuildEnqueueFailureReturns409(t *testing.T) {
	h, _, mgr := newBuildTestRouter(t)
	tplID, buildID := createTplAndBuildID(t, h)
	mgr.enqueue = func(req builder.EnqueueSpec) error {
		return errors.New("already running")
	}
	rec := doJSON(t, h, http.MethodPost, fmt.Sprintf("/v2/templates/%s/builds/%s", tplID, buildID), map[string]any{
		"fromImage": "alpine",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestGetBuildStatus(t *testing.T) {
	h, _, mgr := newBuildTestRouter(t)
	tplID, buildID := createTplAndBuildID(t, h)

	// Start the build so the fake manager registers it, then
	// hand-craft the log state we want the status endpoint to see.
	doJSON(t, h, http.MethodPost, fmt.Sprintf("/v2/templates/%s/builds/%s", tplID, buildID), map[string]any{
		"fromImage": "alpine",
	})
	mgr.appendLog(buildID,
		builder.LogEntry{Timestamp: time.Unix(1, 0), Level: "info", Message: "step 1"},
		builder.LogEntry{Timestamp: time.Unix(2, 0), Level: "info", Message: "step 2"},
	)

	rec := doJSON(t, h, http.MethodGet, fmt.Sprintf("/templates/%s/builds/%s/status", tplID, buildID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp buildStatusResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Status != "building" {
		t.Fatalf("status field = %q", resp.Status)
	}
	if len(resp.Logs) != 2 || resp.Logs[1] != "step 2" {
		t.Fatalf("logs = %+v", resp.Logs)
	}
	if len(resp.LogEntries) != 2 {
		t.Fatalf("logEntries = %d", len(resp.LogEntries))
	}
}

func TestGetBuildStatusWithLogsOffset(t *testing.T) {
	h, _, mgr := newBuildTestRouter(t)
	tplID, buildID := createTplAndBuildID(t, h)
	doJSON(t, h, http.MethodPost, fmt.Sprintf("/v2/templates/%s/builds/%s", tplID, buildID), map[string]any{
		"fromImage": "alpine",
	})
	mgr.appendLog(buildID,
		builder.LogEntry{Message: "a"},
		builder.LogEntry{Message: "b"},
		builder.LogEntry{Message: "c"},
	)
	rec := doJSON(t, h, http.MethodGet, fmt.Sprintf("/templates/%s/builds/%s/status?logsOffset=2", tplID, buildID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var resp buildStatusResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Logs) != 1 || resp.Logs[0] != "c" {
		t.Fatalf("expected only 'c', got %+v", resp.Logs)
	}
}

func TestGetBuildStatusErrorIncludesReason(t *testing.T) {
	h, _, mgr := newBuildTestRouter(t)
	tplID, buildID := createTplAndBuildID(t, h)
	doJSON(t, h, http.MethodPost, fmt.Sprintf("/v2/templates/%s/builds/%s", tplID, buildID), map[string]any{
		"fromImage": "alpine",
	})
	mgr.setStatus(buildID, template.BuildStatusError, "docker build exited 1")

	rec := doJSON(t, h, http.MethodGet, fmt.Sprintf("/templates/%s/builds/%s/status", tplID, buildID), nil)
	var resp buildStatusResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Status != "error" || resp.Reason == nil || !strings.Contains(resp.Reason.Message, "exited 1") {
		t.Fatalf("unexpected: %+v", resp)
	}
}

func TestGetBuildStatusNotFound(t *testing.T) {
	h, _, _ := newBuildTestRouter(t)
	rec := doJSON(t, h, http.MethodGet, "/templates/tpl_x/builds/bld_x/status", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestGetBuildLogs(t *testing.T) {
	h, _, mgr := newBuildTestRouter(t)
	tplID, buildID := createTplAndBuildID(t, h)
	doJSON(t, h, http.MethodPost, fmt.Sprintf("/v2/templates/%s/builds/%s", tplID, buildID), map[string]any{
		"fromImage": "alpine",
	})
	mgr.appendLog(buildID,
		builder.LogEntry{Message: "one"},
		builder.LogEntry{Message: "two"},
	)
	rec := doJSON(t, h, http.MethodGet, fmt.Sprintf("/templates/%s/builds/%s/logs", tplID, buildID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Next-Cursor") == "" {
		t.Fatal("expected X-Next-Cursor header")
	}
	var entries []buildLogEntryOut
	_ = json.NewDecoder(rec.Body).Decode(&entries)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestBuildEndpointsDisabledWhenManagerNil(t *testing.T) {
	store, _ := template.NewStore(template.Options{})
	h := NewRouter(RouterOptions{Templates: store})

	// Build start should 404 when builds are disabled.
	req := httptest.NewRequest(http.MethodPost, "/v2/templates/tpl_x/builds/bld_x", strings.NewReader(`{}`))
	req.Header.Set("X-API-Key", "dev")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
