package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/contember/edvabe/internal/template"
)

func newTemplateTestRouter(t *testing.T) (http.Handler, *template.Store) {
	t.Helper()
	store, err := template.NewStore(template.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return NewRouter(RouterOptions{Templates: store}), store
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("X-API-Key", "dev")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCreateTemplateRequiresAPIKey(t *testing.T) {
	h, _ := newTemplateTestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/v3/templates", strings.NewReader(`{"name":"probe"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateTemplateHappyPath(t *testing.T) {
	h, _ := newTemplateTestRouter(t)
	rec := doJSON(t, h, http.MethodPost, "/v3/templates", map[string]any{
		"name":     "probe",
		"memoryMB": 1024,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp templateBuildResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.TemplateID == "" || resp.BuildID == "" {
		t.Fatalf("missing IDs: %+v", resp)
	}
	if len(resp.Names) != 1 || resp.Names[0] != "probe" {
		t.Fatalf("unexpected names: %+v", resp.Names)
	}
	if len(resp.Aliases) != 1 || resp.Aliases[0] != "probe" {
		t.Fatalf("unexpected aliases: %+v", resp.Aliases)
	}
}

func TestCreateTemplateLegacyPath(t *testing.T) {
	h, _ := newTemplateTestRouter(t)
	rec := doJSON(t, h, http.MethodPost, "/templates", map[string]any{"name": "legacy"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("legacy POST /templates: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateTemplateMissingName(t *testing.T) {
	h, _ := newTemplateTestRouter(t)
	rec := doJSON(t, h, http.MethodPost, "/v3/templates", map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestCreateTemplateConflict(t *testing.T) {
	h, _ := newTemplateTestRouter(t)
	doJSON(t, h, http.MethodPost, "/v3/templates", map[string]any{"name": "dup"})
	rec := doJSON(t, h, http.MethodPost, "/v3/templates", map[string]any{"name": "dup"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestListAndGetTemplate(t *testing.T) {
	h, _ := newTemplateTestRouter(t)
	create := doJSON(t, h, http.MethodPost, "/v3/templates", map[string]any{"name": "alpha"})
	var created templateBuildResponse
	_ = json.NewDecoder(create.Body).Decode(&created)

	listRec := doJSON(t, h, http.MethodGet, "/templates", nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status=%d", listRec.Code)
	}
	var list []templateListItem
	if err := json.NewDecoder(listRec.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "alpha" {
		t.Fatalf("unexpected list: %+v", list)
	}

	// Get by ID
	getRec := doJSON(t, h, http.MethodGet, "/templates/"+created.TemplateID, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d", getRec.Code)
	}
	var got templateWithBuilds
	_ = json.NewDecoder(getRec.Body).Decode(&got)
	if got.Name != "alpha" {
		t.Fatalf("unexpected get: %+v", got)
	}
	if len(got.Builds) != 1 || got.Builds[0].Status != "waiting" {
		t.Fatalf("expected pre-minted waiting build, got %+v", got.Builds)
	}

	// Get by alias — the SDK's exists() check works this way.
	aliasRec := doJSON(t, h, http.MethodGet, "/templates/alpha", nil)
	if aliasRec.Code != http.StatusOK {
		t.Fatalf("alias get status=%d", aliasRec.Code)
	}
}

func TestGetTemplateNotFound(t *testing.T) {
	h, _ := newTemplateTestRouter(t)
	rec := doJSON(t, h, http.MethodGet, "/templates/nope", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestResolveAlias(t *testing.T) {
	h, _ := newTemplateTestRouter(t)
	create := doJSON(t, h, http.MethodPost, "/v3/templates", map[string]any{"name": "resolved"})
	var created templateBuildResponse
	_ = json.NewDecoder(create.Body).Decode(&created)

	rec := doJSON(t, h, http.MethodGet, "/templates/aliases/resolved", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp templateAliasResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.TemplateID != created.TemplateID {
		t.Fatalf("alias returned wrong ID: %s vs %s", resp.TemplateID, created.TemplateID)
	}

	miss := doJSON(t, h, http.MethodGet, "/templates/aliases/nonexistent", nil)
	if miss.Code != http.StatusNotFound {
		t.Fatalf("miss status=%d", miss.Code)
	}
}

func TestDeleteTemplate(t *testing.T) {
	h, _ := newTemplateTestRouter(t)
	create := doJSON(t, h, http.MethodPost, "/v3/templates", map[string]any{"name": "gone"})
	var created templateBuildResponse
	_ = json.NewDecoder(create.Body).Decode(&created)

	del := doJSON(t, h, http.MethodDelete, "/templates/"+created.TemplateID, nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", del.Code)
	}
	get := doJSON(t, h, http.MethodGet, "/templates/"+created.TemplateID, nil)
	if get.Code != http.StatusNotFound {
		t.Fatalf("get after delete status=%d", get.Code)
	}
}

func TestPatchTemplate(t *testing.T) {
	h, _ := newTemplateTestRouter(t)
	create := doJSON(t, h, http.MethodPost, "/v3/templates", map[string]any{"name": "patchable"})
	var created templateBuildResponse
	_ = json.NewDecoder(create.Body).Decode(&created)

	trueVal := true
	rec := doJSON(t, h, http.MethodPatch, "/v2/templates/"+created.TemplateID, templateUpdateRequest{
		Public: &trueVal,
		Tags:   []string{"v1", "stable"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var updated templateListItem
	_ = json.NewDecoder(rec.Body).Decode(&updated)
	if !updated.Public || len(updated.Tags) != 2 {
		t.Fatalf("update did not take: %+v", updated)
	}
}

func TestTemplateRoutesDisabledWhenStoreNil(t *testing.T) {
	h := NewRouter(RouterOptions{})
	req := httptest.NewRequest(http.MethodGet, "/templates", nil)
	req.Header.Set("X-API-Key", "dev")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when templates disabled, got %d", rec.Code)
	}
}

func TestBuildResponseShapeMatchesSDK(t *testing.T) {
	// Sanity-check that the JSON keys line up with what the SDK's
	// TemplateRequestResponseV3 shape reads.
	h, _ := newTemplateTestRouter(t)
	rec := doJSON(t, h, http.MethodPost, "/v3/templates", map[string]any{"name": "shape"})
	var raw map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&raw)
	for _, key := range []string{"templateID", "buildID", "names", "tags", "aliases", "public"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("response missing key %q: %+v", key, raw)
		}
	}
}

// Sanity: ensure the real store satisfies the handler's interface.
var _ templateStore = (*template.Store)(nil)
