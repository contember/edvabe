package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/contember/edvabe/internal/api"
)

// Requests below are the bytes the Python SDK puts on the wire for
// Sandbox.create() and Sandbox.connect(id), recorded from e2b 2.64.0
// and 2.20.0. They run through the full dispatcher, as in `edvabe serve`.

type sdkRequest struct {
	method, path, body string
	headers            map[string]string
}

func newTestDispatcher(t *testing.T) http.Handler {
	t.Helper()
	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("%s %s reached the sandbox proxy", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
	})
	return api.NewRouter(newTestControlRouter(t), proxy, nil)
}

func doSDKRequest(t *testing.T, h http.Handler, sr sdkRequest) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(sr.method, sr.path, strings.NewReader(sr.body))
	req.Header.Set("X-API-Key", "e2b_local")
	req.Header.Set("Content-Type", "application/json")
	for k, v := range sr.headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeSandbox(t *testing.T, rec *httptest.ResponseRecorder) sandboxResponse {
	t.Helper()
	var out sandboxResponse
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode sandbox response: %v", err)
	}
	if out.SandboxID == "" || out.EnvdVersion == "" || out.EnvdAccessToken == "" {
		t.Fatalf("sandbox response misses fields the SDK reads: %+v", out)
	}
	return out
}

func pauseForTest(t *testing.T, h http.Handler, id string) {
	t.Helper()
	rec := doSDKRequest(t, h, sdkRequest{method: http.MethodPost, path: "/sandboxes/" + id + "/pause"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("pause status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPythonSDK264CreateAndConnect(t *testing.T) {
	h := newTestDispatcher(t)

	createRec := doSDKRequest(t, h, sdkRequest{
		method: http.MethodPost,
		path:   "/v2/sandboxes",
		body:   `{"templateID":"base","metadata":{},"envVars":{}}`,
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 body=%s", createRec.Code, createRec.Body.String())
	}
	created := decodeSandbox(t, createRec)

	connect := sdkRequest{method: http.MethodPost, path: "/v2/sandboxes/" + created.SandboxID + "/connect", body: `{}`}
	connectRec := doSDKRequest(t, h, connect)
	if connectRec.Code != http.StatusOK {
		t.Fatalf("connect status = %d, want 200 body=%s", connectRec.Code, connectRec.Body.String())
	}
	connected := decodeSandbox(t, connectRec)
	if connected.SandboxID != created.SandboxID {
		t.Fatalf("connected sandbox = %q, want %q", connected.SandboxID, created.SandboxID)
	}
	// The body carries no timeout; a zero TTL would expire the sandbox at once.
	if earliest := time.Now().Add(4 * time.Minute); connected.EndAt.Before(earliest) {
		t.Fatalf("endAt = %v, want after %v", connected.EndAt, earliest)
	}

	pauseForTest(t, h, created.SandboxID)
	if rec := doSDKRequest(t, h, connect); rec.Code != http.StatusCreated {
		t.Fatalf("connect to paused sandbox status = %d, want 201 body=%s", rec.Code, rec.Body.String())
	}
}

func TestPythonSDK264ConnectMissingSandboxReturnsJSON404(t *testing.T) {
	rec := doSDKRequest(t, newTestDispatcher(t), sdkRequest{
		method: http.MethodPost,
		path:   "/v2/sandboxes/isb_missing/connect",
		body:   `{}`,
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var e struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&e); err != nil || e.Code != http.StatusNotFound {
		t.Fatalf("want the E2B error envelope, got %q (%v)", rec.Body.String(), err)
	}
}

func TestPythonSDK220CreateAndConnectWithSandboxHeader(t *testing.T) {
	h := newTestDispatcher(t)

	createRec := doSDKRequest(t, h, sdkRequest{
		method: http.MethodPost,
		path:   "/sandboxes",
		body:   `{"templateID":"base","allow_internet_access":true,"autoPause":false,"envVars":{},"metadata":{},"secure":true,"timeout":300}`,
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 body=%s", createRec.Code, createRec.Body.String())
	}
	created := decodeSandbox(t, createRec)

	// 2.20 builds the control-plane client for connect with the sandbox headers.
	connect := sdkRequest{
		method: http.MethodPost,
		path:   "/sandboxes/" + created.SandboxID + "/connect",
		body:   `{"timeout":300}`,
		headers: map[string]string{
			api.HeaderSandboxID:   created.SandboxID,
			api.HeaderSandboxPort: "49983",
		},
	}
	connectRec := doSDKRequest(t, h, connect)
	if connectRec.Code != http.StatusOK {
		t.Fatalf("connect status = %d, want 200 body=%s", connectRec.Code, connectRec.Body.String())
	}
	if got := decodeSandbox(t, connectRec); got.SandboxID != created.SandboxID {
		t.Fatalf("connected sandbox = %q, want %q", got.SandboxID, created.SandboxID)
	}

	pauseForTest(t, h, created.SandboxID)
	if rec := doSDKRequest(t, h, connect); rec.Code != http.StatusCreated {
		t.Fatalf("connect to paused sandbox status = %d, want 201 body=%s", rec.Code, rec.Body.String())
	}
}
