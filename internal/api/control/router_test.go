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

func newTestControlRouter(t *testing.T) http.Handler {
	t.Helper()
	rt := noop.New()
	mgr, err := sandbox.NewManager(sandbox.Options{Runtime: rt, Agent: stubAgent{}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return NewRouter(mgr, rt, stubAgent{})
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

func TestConnectMissingSandboxReturns404(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/sandboxes/isb_missing/connect", strings.NewReader(`{"timeout":60}`))
	req.Header.Set("X-API-Key", "dev")

	newTestControlRouter(t).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
