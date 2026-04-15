package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/contember/edvabe/internal/api"
	"github.com/contember/edvabe/internal/template"
	"github.com/contember/edvabe/internal/template/filecache"
)

// fileUploadLinkResponse is the SDK's TemplateBuildFileUpload shape
// (see test/e2e/ts/node_modules/e2b/dist/index.d.ts:2463). When
// `present` is true the SDK skips upload entirely; otherwise it
// follows `url` with a single PUT.
type fileUploadLinkResponse struct {
	Present bool   `json:"present"`
	URL     string `json:"url,omitempty"`
}

// getFileUploadLink handles GET /templates/{templateID}/files/{hash}.
// Validates the template exists, checks the file cache, and either
// replies "already present" or hands back a short-lived signed upload
// URL pointing at the internal /_upload endpoint below.
func getFileUploadLink(
	store templateStore,
	cache *filecache.Cache,
	signer *filecache.Signer,
	publicBase string,
	w http.ResponseWriter,
	r *http.Request,
) {
	templateID, hash, ok := parseTemplateFilePath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, err := store.Get(templateID); err != nil {
		if errors.Is(err, template.ErrNotFound) {
			api.WriteError(w, http.StatusNotFound, "template not found")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	present, err := cache.Has(hash)
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp := fileUploadLinkResponse{Present: present}
	if !present {
		token := signer.Sign(hash)
		// The SDK treats the URL as opaque — we can point at our own
		// /_upload handler. publicBase is the reachable base URL of
		// edvabe; in local dev that's http://localhost:3000.
		base := publicBase
		if base == "" {
			base = inferBase(r)
		}
		resp.URL = fmt.Sprintf("%s/_upload/%s?token=%s", strings.TrimRight(base, "/"), hash, token)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// uploadFile handles PUT /_upload/{hash}?token=... — the endpoint we
// hand out in the link above. Verifies the HMAC token, streams the
// body through the content-addressed cache, and returns 204 on
// success. Lives outside the X-API-Key requirement because the
// signed token is the auth.
func uploadFile(
	cache *filecache.Cache,
	signer *filecache.Signer,
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	hash := strings.TrimPrefix(r.URL.Path, "/_upload/")
	if hash == "" || strings.Contains(hash, "/") {
		http.NotFound(w, r)
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		api.WriteError(w, http.StatusUnauthorized, "missing upload token")
		return
	}
	if err := signer.Verify(hash, token); err != nil {
		api.WriteError(w, http.StatusUnauthorized, "invalid upload token")
		return
	}
	if err := cache.Put(hash, r.Body); err != nil {
		if errors.Is(err, filecache.ErrHashMismatch) {
			api.WriteError(w, http.StatusBadRequest, "hash mismatch")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseTemplateFilePath splits /templates/{templateID}/files/{hash}
// into its two variables. Returns ok=false for anything else.
func parseTemplateFilePath(path string) (templateID, hash string, ok bool) {
	if !strings.HasPrefix(path, "/templates/") {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, "/templates/")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) != 3 || parts[1] != "files" || parts[0] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

// inferBase best-effort reconstructs the external base URL from the
// inbound request. Only used when the operator hasn't configured an
// explicit publicBase — local dev rarely needs that override.
func inferBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if xf := r.Header.Get("X-Forwarded-Proto"); xf != "" {
		scheme = xf
	}
	host := r.Host
	if xh := r.Header.Get("X-Forwarded-Host"); xh != "" {
		host = xh
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}
