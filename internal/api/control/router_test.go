package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/contember/edvabe/internal/agent"
	"github.com/contember/edvabe/internal/runtime"
	"github.com/contember/edvabe/internal/runtime/noop"
	"github.com/contember/edvabe/internal/sandbox"
)

type stubAgent struct{}

func (stubAgent) Name() string                                               { return "stub" }
func (stubAgent) Version() string                                            { return "0.5.7" }
func (stubAgent) Port() int                                                  { return 49983 }
func (stubAgent) EnsureImage(context.Context, runtime.Runtime, string) error { return nil }
func (stubAgent) InitAgent(context.Context, string, agent.InitConfig) error  { return nil }
func (stubAgent) Ping(context.Context, string) error                         { return nil }
func (stubAgent) WaitReady(context.Context, string, string) error            { return nil }

func newTestControlRouter(t *testing.T) http.Handler {
	t.Helper()
	rt := noop.New()
	mgr, err := sandbox.NewManager(sandbox.Options{Runtime: rt, Agent: stubAgent{}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return NewRouter(RouterOptions{Manager: mgr, Runtime: rt, Provider: stubAgent{}})
}

func TestHealthIsUnauthenticated(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)

	newTestControlRouter(t).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}

func TestCreateRequiresAPIKey(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(`{"templateID":"base"}`))

	newTestControlRouter(t).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestCreateAndGetSandbox(t *testing.T) {
	h := newTestControlRouter(t)

	createReq := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(`{"templateID":"base","timeout":120,"metadata":{"owner":"alice"},"envVars":{"FOO":"bar"}}`))
	createReq.Header.Set("X-API-Key", "dev")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createRec.Code, createRec.Body.String())
	}

	var created sandboxResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.SandboxID == "" {
		t.Fatal("SandboxID is empty")
	}
	if created.EnvdVersion != "0.5.7" {
		t.Fatalf("EnvdVersion = %q", created.EnvdVersion)
	}
	if created.Domain != sandbox.DefaultDomain {
		t.Fatalf("Domain = %q", created.Domain)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/sandboxes/"+created.SandboxID, nil)
	getReq.Header.Set("X-API-Key", "dev")
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", getRec.Code, getRec.Body.String())
	}

	var detail sandboxDetailResponse
	if err := json.NewDecoder(getRec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if detail.State != sandbox.StateRunning {
		t.Fatalf("State = %q", detail.State)
	}
	if detail.EnvdAccessToken == "" {
		t.Fatal("EnvdAccessToken is empty")
	}
	if detail.Metadata["owner"] != "alice" {
		t.Fatalf("Metadata = %#v", detail.Metadata)
	}
	if detail.EnvVars["FOO"] != "bar" {
		t.Fatalf("EnvVars = %#v", detail.EnvVars)
	}
}

func TestGetUnknownSandboxReturns404(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sandboxes/isb_missing", nil)
	req.Header.Set("X-API-Key", "dev")

	newTestControlRouter(t).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestListDeleteTimeoutAndConnect(t *testing.T) {
	h := newTestControlRouter(t)
	ids := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(`{"templateID":"base","timeout":60}`))
		req.Header.Set("X-API-Key", "dev")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %d status = %d body=%s", i, rec.Code, rec.Body.String())
		}
		var created sandboxResponse
		if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
			t.Fatalf("decode create %d: %v", i, err)
		}
		ids = append(ids, created.SandboxID)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v2/sandboxes?limit=1", nil)
	listReq.Header.Set("X-API-Key", "dev")
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listRec.Code, listRec.Body.String())
	}
	var listed []sandboxResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed len = %d, want 1", len(listed))
	}
	next := listRec.Header().Get("X-Next-Token")
	if next == "" {
		t.Fatal("X-Next-Token is empty")
	}
	if _, err := strconv.Atoi(next); err != nil {
		t.Fatalf("X-Next-Token = %q, not int", next)
	}

	connectReq := httptest.NewRequest(http.MethodPost, "/sandboxes/"+ids[0]+"/connect", strings.NewReader(`{"timeout":120}`))
	connectReq.Header.Set("X-API-Key", "dev")
	connectRec := httptest.NewRecorder()
	h.ServeHTTP(connectRec, connectReq)
	if connectRec.Code != http.StatusOK {
		t.Fatalf("connect status = %d body=%s", connectRec.Code, connectRec.Body.String())
	}
	var connected sandboxResponse
	if err := json.NewDecoder(connectRec.Body).Decode(&connected); err != nil {
		t.Fatalf("decode connect: %v", err)
	}
	if connected.SandboxID != ids[0] {
		t.Fatalf("connected sandbox = %q, want %q", connected.SandboxID, ids[0])
	}

	timeoutReq := httptest.NewRequest(http.MethodPost, "/sandboxes/"+ids[0]+"/timeout", strings.NewReader(`{"timeout":300}`))
	timeoutReq.Header.Set("X-API-Key", "dev")
	timeoutRec := httptest.NewRecorder()
	h.ServeHTTP(timeoutRec, timeoutReq)
	if timeoutRec.Code != http.StatusNoContent {
		t.Fatalf("timeout status = %d body=%s", timeoutRec.Code, timeoutRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/sandboxes/"+ids[1], nil)
	deleteReq.Header.Set("X-API-Key", "dev")
	deleteRec := httptest.NewRecorder()
	h.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestPauseSnapshotAndResume(t *testing.T) {
	h := newTestControlRouter(t)

	// Create a sandbox to work with.
	createReq := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(`{"templateID":"base","timeout":120}`))
	createReq.Header.Set("X-API-Key", "dev")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createRec.Code, createRec.Body.String())
	}
	var created sandboxResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	id := created.SandboxID

	// Pause → 204.
	pauseReq := httptest.NewRequest(http.MethodPost, "/sandboxes/"+id+"/pause", nil)
	pauseReq.Header.Set("X-API-Key", "dev")
	pauseRec := httptest.NewRecorder()
	h.ServeHTTP(pauseRec, pauseReq)
	if pauseRec.Code != http.StatusNoContent {
		t.Fatalf("pause status = %d body=%s", pauseRec.Code, pauseRec.Body.String())
	}

	// GET confirms paused state.
	getReq := httptest.NewRequest(http.MethodGet, "/sandboxes/"+id, nil)
	getReq.Header.Set("X-API-Key", "dev")
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", getRec.Code, getRec.Body.String())
	}
	var detail sandboxDetailResponse
	if err := json.NewDecoder(getRec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.State != sandbox.StatePaused {
		t.Errorf("state = %q, want paused", detail.State)
	}

	// Snapshot → 201 with SnapshotInfo body.
	snapReq := httptest.NewRequest(http.MethodPost, "/sandboxes/"+id+"/snapshots", strings.NewReader(`{"name":"v1"}`))
	snapReq.Header.Set("X-API-Key", "dev")
	snapRec := httptest.NewRecorder()
	h.ServeHTTP(snapRec, snapReq)
	if snapRec.Code != http.StatusCreated {
		t.Fatalf("snapshot status = %d body=%s", snapRec.Code, snapRec.Body.String())
	}
	var snapInfo snapshotResponse
	if err := json.NewDecoder(snapRec.Body).Decode(&snapInfo); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapInfo.Name != "v1" {
		t.Errorf("snapshot name = %q, want v1", snapInfo.Name)
	}
	if !strings.Contains(snapInfo.ImageTag, "edvabe/snapshot-"+id+":v1") {
		t.Errorf("snapshot imageTag = %q", snapInfo.ImageTag)
	}

	// Resume (deprecated alias for /connect) flips back to running.
	resumeReq := httptest.NewRequest(http.MethodPost, "/sandboxes/"+id+"/resume", strings.NewReader(`{"timeout":60}`))
	resumeReq.Header.Set("X-API-Key", "dev")
	resumeRec := httptest.NewRecorder()
	h.ServeHTTP(resumeRec, resumeReq)
	if resumeRec.Code != http.StatusOK {
		t.Fatalf("resume status = %d body=%s", resumeRec.Code, resumeRec.Body.String())
	}

	// GET after resume: should be running again.
	getReq2 := httptest.NewRequest(http.MethodGet, "/sandboxes/"+id, nil)
	getReq2.Header.Set("X-API-Key", "dev")
	getRec2 := httptest.NewRecorder()
	h.ServeHTTP(getRec2, getReq2)
	var detail2 sandboxDetailResponse
	if err := json.NewDecoder(getRec2.Body).Decode(&detail2); err != nil {
		t.Fatalf("decode detail after resume: %v", err)
	}
	if detail2.State != sandbox.StateRunning {
		t.Errorf("state after resume = %q, want running", detail2.State)
	}
}

func TestPauseUnknownSandboxReturns404(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/sandboxes/isb_missing/pause", nil)
	req.Header.Set("X-API-Key", "dev")

	newTestControlRouter(t).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCreateWithAutoPauseReflectsInDetail(t *testing.T) {
	h := newTestControlRouter(t)

	createReq := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(`{"templateID":"base","timeout":60,"autoPause":true}`))
	createReq.Header.Set("X-API-Key", "dev")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createRec.Code, createRec.Body.String())
	}
	var created sandboxResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/sandboxes/"+created.SandboxID, nil)
	getReq.Header.Set("X-API-Key", "dev")
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", getRec.Code, getRec.Body.String())
	}
	var detail sandboxDetailResponse
	if err := json.NewDecoder(getRec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Lifecycle.OnTimeout != string(sandbox.OnTimeoutPause) {
		t.Errorf("lifecycle.onTimeout = %q, want %q", detail.Lifecycle.OnTimeout, sandbox.OnTimeoutPause)
	}
	if !detail.Lifecycle.AutoResume.Enabled {
		t.Error("lifecycle.autoResume.enabled = false, want true for autoPause sandbox")
	}
}

func TestCreateWithLifecycleOnTimeoutPause(t *testing.T) {
	h := newTestControlRouter(t)

	createReq := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(`{"templateID":"base","timeout":60,"lifecycle":{"onTimeout":"pause"}}`))
	createReq.Header.Set("X-API-Key", "dev")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createRec.Code, createRec.Body.String())
	}
	var created sandboxResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/sandboxes/"+created.SandboxID, nil)
	getReq.Header.Set("X-API-Key", "dev")
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	var detail sandboxDetailResponse
	if err := json.NewDecoder(getRec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Lifecycle.OnTimeout != "pause" {
		t.Errorf("lifecycle.onTimeout = %q, want pause", detail.Lifecycle.OnTimeout)
	}
}

func TestCreateDefaultLifecycleIsKill(t *testing.T) {
	h := newTestControlRouter(t)

	createReq := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(`{"templateID":"base","timeout":60}`))
	createReq.Header.Set("X-API-Key", "dev")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	var created sandboxResponse
	_ = json.NewDecoder(createRec.Body).Decode(&created)

	getReq := httptest.NewRequest(http.MethodGet, "/sandboxes/"+created.SandboxID, nil)
	getReq.Header.Set("X-API-Key", "dev")
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	var detail sandboxDetailResponse
	if err := json.NewDecoder(getRec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Lifecycle.OnTimeout != "kill" {
		t.Errorf("lifecycle.onTimeout = %q, want kill", detail.Lifecycle.OnTimeout)
	}
	if detail.Lifecycle.AutoResume.Enabled {
		t.Error("kill sandbox should not advertise autoResume.enabled")
	}
}

func TestConnectMissingSandboxReturns404(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/sandboxes/isb_missing/connect", strings.NewReader(`{"timeout":60}`))
	req.Header.Set("X-API-Key", "dev")

	newTestControlRouter(t).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
