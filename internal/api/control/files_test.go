package control

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/contember/edvabe/internal/template"
	"github.com/contember/edvabe/internal/template/filecache"
)

func newFilesTestRouter(t *testing.T) (http.Handler, *template.Store, *filecache.Cache, *filecache.Signer) {
	t.Helper()
	store, err := template.NewStore(template.Options{})
	if err != nil {
		t.Fatal(err)
	}
	cache, err := filecache.New(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	signer := filecache.NewSigner(filecache.SignerOptions{
		Secret: []byte("test-secret"),
		TTL:    5 * time.Minute,
		Now:    func() time.Time { return now },
	})
	h := NewRouter(RouterOptions{
		Templates:  store,
		FileCache:  cache,
		FileSigner: signer,
		PublicBase: "http://localhost:3000",
	})
	return h, store, cache, signer
}

func createTplForFiles(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/v3/templates", map[string]any{"name": "files-probe"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup create failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp templateBuildResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	return resp.TemplateID
}

func TestFileUploadLinkReportsAbsent(t *testing.T) {
	h, _, _, _ := newFilesTestRouter(t)
	tplID := createTplForFiles(t, h)
	hash := filecache.HashBytes([]byte("some tar"))
	rec := doJSON(t, h, http.MethodGet, "/templates/"+tplID+"/files/"+hash, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp fileUploadLinkResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Present {
		t.Fatal("expected Present=false for fresh cache")
	}
	if resp.URL == "" || !strings.Contains(resp.URL, "/_upload/"+hash+"?token=") {
		t.Fatalf("unexpected URL: %s", resp.URL)
	}
}

func TestFileUploadLinkReportsPresent(t *testing.T) {
	h, _, cache, _ := newFilesTestRouter(t)
	tplID := createTplForFiles(t, h)
	payload := []byte("already cached")
	hash := filecache.HashBytes(payload)
	if err := cache.Put(hash, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, h, http.MethodGet, "/templates/"+tplID+"/files/"+hash, nil)
	var resp fileUploadLinkResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Present {
		t.Fatal("expected Present=true")
	}
	if resp.URL != "" {
		t.Fatalf("expected empty URL on present, got %q", resp.URL)
	}
}

func TestFileUploadLinkTemplateNotFound(t *testing.T) {
	h, _, _, _ := newFilesTestRouter(t)
	hash := filecache.HashBytes([]byte("x"))
	rec := doJSON(t, h, http.MethodGet, "/templates/tpl_missing/files/"+hash, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestFileUploadLinkInvalidHash(t *testing.T) {
	h, _, _, _ := newFilesTestRouter(t)
	tplID := createTplForFiles(t, h)
	rec := doJSON(t, h, http.MethodGet, "/templates/"+tplID+"/files/not-a-hash", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestUploadHappyPath(t *testing.T) {
	h, _, cache, signer := newFilesTestRouter(t)
	payload := []byte("hello tar")
	hash := filecache.HashBytes(payload)
	token := signer.Sign(hash)

	req := httptest.NewRequest(http.MethodPut, "/_upload/"+hash+"?token="+token, bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if present, _ := cache.Has(hash); !present {
		t.Fatal("cache did not receive the upload")
	}
	rc, _ := cache.Open(hash)
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, payload) {
		t.Fatalf("cache contents mismatch: %q vs %q", got, payload)
	}
}

func TestUploadRejectsMissingToken(t *testing.T) {
	h, _, _, _ := newFilesTestRouter(t)
	hash := filecache.HashBytes([]byte("x"))
	req := httptest.NewRequest(http.MethodPut, "/_upload/"+hash, bytes.NewReader([]byte("x")))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestUploadRejectsInvalidToken(t *testing.T) {
	h, _, _, _ := newFilesTestRouter(t)
	hash := filecache.HashBytes([]byte("x"))
	req := httptest.NewRequest(http.MethodPut, "/_upload/"+hash+"?token=bogus.0", bytes.NewReader([]byte("x")))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestUploadAcceptsOpaqueHash(t *testing.T) {
	// The hash is a client-supplied content address computed over file
	// metadata, not the tar stream, so the server stores without
	// re-verifying.
	h, _, _, signer := newFilesTestRouter(t)
	advertised := filecache.HashBytes([]byte("advertised"))
	token := signer.Sign(advertised)
	req := httptest.NewRequest(http.MethodPut, "/_upload/"+advertised+"?token="+token, bytes.NewReader([]byte("actual")))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUploadDoesNotRequireAPIKey(t *testing.T) {
	// The auth on /_upload is the HMAC token, not X-API-Key. The
	// request fires without the header and still succeeds.
	h, _, _, signer := newFilesTestRouter(t)
	payload := []byte("no-apikey")
	hash := filecache.HashBytes(payload)
	req := httptest.NewRequest(http.MethodPut, "/_upload/"+hash+"?token="+signer.Sign(hash), bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestFileEndpointsDisabledWhenCacheNil(t *testing.T) {
	store, _ := template.NewStore(template.Options{})
	h := NewRouter(RouterOptions{Templates: store})
	hash := filecache.HashBytes([]byte("x"))
	// GET /templates/{id}/files/{hash} should fall through to
	// getTemplate which will return 404 for a nonexistent ID — same
	// shape whether files are enabled or not, but the important
	// thing is no panic. We go further and hit /_upload.
	req := httptest.NewRequest(http.MethodPut, "/_upload/"+hash, bytes.NewReader(nil))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when file cache disabled, got %d", rec.Code)
	}
}
