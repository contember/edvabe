package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── Teams ────────────────────────────────────────────────────────────

func TestListTeams(t *testing.T) {
	srv := httptest.NewServer(newTestControlRouter(t))
	defer srv.Close()

	resp := apiGet(t, srv.URL+"/teams")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var teams []teamResponse
	json.NewDecoder(resp.Body).Decode(&teams)
	resp.Body.Close()
	if len(teams) != 1 || teams[0].TeamID != "local" {
		t.Fatalf("unexpected teams: %+v", teams)
	}
}

func TestTeamMetrics(t *testing.T) {
	srv := httptest.NewServer(newTestControlRouter(t))
	defer srv.Close()

	resp := apiGet(t, srv.URL+"/teams/local/metrics")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var arr []any
	json.NewDecoder(resp.Body).Decode(&arr)
	resp.Body.Close()
	if len(arr) != 0 {
		t.Fatalf("expected empty array, got %d entries", len(arr))
	}
}

func TestTeamMetricsMax(t *testing.T) {
	srv := httptest.NewServer(newTestControlRouter(t))
	defer srv.Close()

	resp := apiGet(t, srv.URL+"/teams/local/metrics/max")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ── API Keys ─────────────────────────────────────────────────────────

func TestAPIKeysListAndCreate(t *testing.T) {
	srv := httptest.NewServer(newTestControlRouter(t))
	defer srv.Close()

	// List default keys
	resp := apiGet(t, srv.URL+"/api-keys")
	if resp.StatusCode != 200 {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	var keys []apiKeyEntry
	json.NewDecoder(resp.Body).Decode(&keys)
	resp.Body.Close()
	if len(keys) != 1 {
		t.Fatalf("expected 1 default key, got %d", len(keys))
	}

	// Create a new key
	body, _ := json.Marshal(map[string]string{"name": "test-key"})
	resp2 := apiPost(t, srv.URL+"/api-keys", body)
	if resp2.StatusCode != 201 {
		t.Fatalf("create status = %d", resp2.StatusCode)
	}
	var created apiKeyEntry
	json.NewDecoder(resp2.Body).Decode(&created)
	resp2.Body.Close()
	if created.Name != "test-key" || created.ID == "" {
		t.Fatalf("unexpected key: %+v", created)
	}

	// List should now have 2
	resp3 := apiGet(t, srv.URL+"/api-keys")
	var keys2 []apiKeyEntry
	json.NewDecoder(resp3.Body).Decode(&keys2)
	resp3.Body.Close()
	if len(keys2) != 2 {
		t.Fatalf("expected 2 keys after create, got %d", len(keys2))
	}
}

// ── Access Tokens ────────────────────────────────────────────────────

func TestAccessTokensCreateAndDelete(t *testing.T) {
	srv := httptest.NewServer(newTestControlRouter(t))
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"name": "my-token"})
	resp := apiPost(t, srv.URL+"/access-tokens", body)
	if resp.StatusCode != 201 {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	var created accessTokenEntry
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if created.Token == "" {
		t.Fatal("expected non-empty token")
	}

	// Delete
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/access-tokens/"+created.ID, nil)
	req.Header.Set("X-API-Key", "test")
	resp2, _ := http.DefaultClient.Do(req)
	if resp2.StatusCode != 204 {
		t.Fatalf("delete status = %d", resp2.StatusCode)
	}
	resp2.Body.Close()
}

// ── Volumes ──────────────────────────────────────────────────────────

func TestVolumesLifecycle(t *testing.T) {
	srv := httptest.NewServer(newTestControlRouter(t))
	defer srv.Close()

	// List empty
	resp := apiGet(t, srv.URL+"/volumes")
	var vols []volumeEntry
	json.NewDecoder(resp.Body).Decode(&vols)
	resp.Body.Close()
	if len(vols) != 0 {
		t.Fatalf("expected empty volumes, got %d", len(vols))
	}

	// Create
	body, _ := json.Marshal(map[string]string{"name": "data-vol"})
	resp2 := apiPost(t, srv.URL+"/volumes", body)
	if resp2.StatusCode != 201 {
		t.Fatalf("create status = %d", resp2.StatusCode)
	}
	var created volumeEntry
	json.NewDecoder(resp2.Body).Decode(&created)
	resp2.Body.Close()
	if created.Name != "data-vol" {
		t.Fatalf("unexpected volume: %+v", created)
	}

	// Get
	resp3 := apiGet(t, srv.URL+"/volumes/"+created.VolumeID)
	if resp3.StatusCode != 200 {
		t.Fatalf("get status = %d", resp3.StatusCode)
	}
	resp3.Body.Close()

	// Delete
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/volumes/"+created.VolumeID, nil)
	req.Header.Set("X-API-Key", "test")
	resp4, _ := http.DefaultClient.Do(req)
	if resp4.StatusCode != 204 {
		t.Fatalf("delete status = %d", resp4.StatusCode)
	}
	resp4.Body.Close()
}

// ── Nodes / Admin ────────────────────────────────────────────────────

func TestNodesReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(newTestControlRouter(t))
	defer srv.Close()

	resp := apiGet(t, srv.URL+"/nodes")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAdminReturns501(t *testing.T) {
	srv := httptest.NewServer(newTestControlRouter(t))
	defer srv.Close()

	resp := apiPost(t, srv.URL+"/admin/anything", nil)
	if resp.StatusCode != 501 {
		t.Fatalf("status = %d, want 501", resp.StatusCode)
	}
	resp.Body.Close()
}

// ─�� Snapshots list ───────────────────────────────────────────────────

func TestSnapshotsListEmpty(t *testing.T) {
	srv := httptest.NewServer(newTestControlRouter(t))
	defer srv.Close()

	resp := apiGet(t, srv.URL+"/snapshots")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var arr []any
	json.NewDecoder(resp.Body).Decode(&arr)
	resp.Body.Close()
	if len(arr) != 0 {
		t.Fatalf("expected empty, got %d", len(arr))
	}
}

// ── Sandbox network / refreshes / logs ───────────────────────────────

func TestSandboxNetworkUpdate(t *testing.T) {
	h := newTestControlRouter(t)

	// Create a sandbox first
	createReq := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(`{"templateID":"base","timeout":60}`))
	createReq.Header.Set("X-API-Key", "dev")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	var created sandboxResponse
	json.NewDecoder(createRec.Body).Decode(&created)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/sandboxes/"+created.SandboxID+"/network",
		strings.NewReader(`{"allowOut":["0.0.0.0/0"]}`))
	req.Header.Set("X-API-Key", "dev")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSandboxRefreshes(t *testing.T) {
	h := newTestControlRouter(t)

	createReq := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(`{"templateID":"base","timeout":60}`))
	createReq.Header.Set("X-API-Key", "dev")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	var created sandboxResponse
	json.NewDecoder(createRec.Body).Decode(&created)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/sandboxes/"+created.SandboxID+"/refreshes",
		strings.NewReader(`{"duration":300}`))
	req.Header.Set("X-API-Key", "dev")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSandboxLogs(t *testing.T) {
	h := newTestControlRouter(t)

	createReq := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(`{"templateID":"base","timeout":60}`))
	createReq.Header.Set("X-API-Key", "dev")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	var created sandboxResponse
	json.NewDecoder(createRec.Body).Decode(&created)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/sandboxes/"+created.SandboxID+"/logs", nil)
	req.Header.Set("X-API-Key", "dev")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	json.NewDecoder(rec.Body).Decode(&result)
	if result["hasMore"] != false {
		t.Fatalf("expected hasMore=false, got %v", result["hasMore"])
	}
}

func TestSandboxMetrics(t *testing.T) {
	h := newTestControlRouter(t)

	createReq := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(`{"templateID":"base","timeout":60}`))
	createReq.Header.Set("X-API-Key", "dev")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	var created sandboxResponse
	json.NewDecoder(createRec.Body).Decode(&created)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sandboxes/"+created.SandboxID+"/metrics", nil)
	req.Header.Set("X-API-Key", "dev")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateSandboxWithExtraFieldsDoesNotCrash(t *testing.T) {
	h := newTestControlRouter(t)

	body := `{
		"templateID": "base",
		"timeout": 60,
		"alias": "my-sandbox",
		"secure": true,
		"allow_internet_access": true,
		"network": {"allowPublicTraffic": true},
		"volumeMounts": [],
		"autoResume": {"enabled": false}
	}`
	req := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(body))
	req.Header.Set("X-API-Key", "dev")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var created sandboxResponse
	json.NewDecoder(rec.Body).Decode(&created)
	if created.Alias != "my-sandbox" {
		t.Errorf("alias = %q, want my-sandbox", created.Alias)
	}
}

// ── Helpers ──────────────────────────────────────────────────────────

func apiGet(t *testing.T, url string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("X-API-Key", "test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func apiPost(t *testing.T, url string, body []byte) *http.Response {
	t.Helper()
	var r *bytes.Reader
	if body != nil {
		r = bytes.NewReader(body)
	} else {
		r = bytes.NewReader([]byte{})
	}
	req, _ := http.NewRequest(http.MethodPost, url, r)
	req.Header.Set("X-API-Key", "test")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}
