package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	if createRec.Code != http.StatusOK {
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
