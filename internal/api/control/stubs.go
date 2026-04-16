package control

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/contember/edvabe/internal/api"
)

// ──────────────────────────────────────────────────────────────────────
// Teams — hardcoded single "local" team.
// ──────────────────────────────────────────────────────────────────────

const localTeamID = "local"

type teamResponse struct {
	TeamID    string `json:"teamID"`
	Name      string `json:"name"`
	APIKey    string `json:"apiKey"`
	IsDefault bool   `json:"isDefault"`
}

func listTeams(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []teamResponse{{
		TeamID:    localTeamID,
		Name:      "edvabe-local",
		APIKey:    "edvabe_local",
		IsDefault: true,
	}})
}

func getTeamMetrics(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

func getTeamMetricsMax(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"maxConcurrentSandboxes": 0,
		"maxSandboxStartRate":    0,
	})
}

// ──────────────────────────────────────────────────────────────────────
// API keys — in-memory CRUD, no persistence across restarts.
// ──────────────────────────────────────────────────────────────────────

type apiKeyEntry struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Mask      apiKeyMask `json:"mask"`
	CreatedAt time.Time  `json:"createdAt"`
	LastUsed  *time.Time `json:"lastUsed"`
}

type apiKeyMask struct {
	First4 string `json:"first4"`
	Last4  string `json:"last4"`
}

type apiKeyStore struct {
	keys []apiKeyEntry
}

func newAPIKeyStore() *apiKeyStore {
	return &apiKeyStore{keys: []apiKeyEntry{{
		ID:        "ak_default",
		Name:      "default",
		Mask:      apiKeyMask{First4: "edva", Last4: "ocal"},
		CreatedAt: time.Now(),
	}}}
}

func (s *apiKeyStore) list(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.keys)
}

func (s *apiKeyStore) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	entry := apiKeyEntry{
		ID:        "ak_" + randomHex(8),
		Name:      req.Name,
		Mask:      apiKeyMask{First4: "edva", Last4: "ocal"},
		CreatedAt: time.Now(),
	}
	s.keys = append(s.keys, entry)
	writeJSON(w, http.StatusCreated, entry)
}

func (s *apiKeyStore) delete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api-keys/")
	for i, k := range s.keys {
		if k.ID == id {
			s.keys = append(s.keys[:i], s.keys[i+1:]...)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	api.WriteError(w, http.StatusNotFound, "api key not found")
}

// ──────────────────────────────────────────────────────────────────────
// Access tokens — in-memory CRUD.
// ──────────────────────────────────────────────────────────────────────

type accessTokenEntry struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Token     string         `json:"token,omitempty"`
	Mask      accessTokenMsk `json:"mask"`
	CreatedAt time.Time      `json:"createdAt"`
}

type accessTokenMsk struct {
	First4 string `json:"first4"`
	Last4  string `json:"last4"`
}

type accessTokenStore struct {
	tokens []accessTokenEntry
}

func newAccessTokenStore() *accessTokenStore {
	return &accessTokenStore{}
}

func (s *accessTokenStore) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	tok := "at_" + randomHex(16)
	entry := accessTokenEntry{
		ID:        "at_" + randomHex(8),
		Name:      req.Name,
		Token:     tok,
		Mask:      accessTokenMsk{First4: tok[:4], Last4: tok[len(tok)-4:]},
		CreatedAt: time.Now(),
	}
	s.tokens = append(s.tokens, entry)
	writeJSON(w, http.StatusCreated, entry)
}

func (s *accessTokenStore) delete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/access-tokens/")
	for i, t := range s.tokens {
		if t.ID == id {
			s.tokens = append(s.tokens[:i], s.tokens[i+1:]...)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	api.WriteError(w, http.StatusNotFound, "access token not found")
}

// ──────────────────────────────────────────────────────────────────────
// Volumes — in-memory registry. No real bind-mount in v1.
// ──────────────────────────────────────────────────────────────────────

type volumeEntry struct {
	VolumeID string `json:"volumeID"`
	Name     string `json:"name"`
}

type volumeStore struct {
	volumes []volumeEntry
}

func newVolumeStore() *volumeStore {
	return &volumeStore{}
}

func (s *volumeStore) list(w http.ResponseWriter, _ *http.Request) {
	out := s.volumes
	if out == nil {
		out = []volumeEntry{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *volumeStore) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	entry := volumeEntry{
		VolumeID: "vol_" + randomHex(8),
		Name:     req.Name,
	}
	s.volumes = append(s.volumes, entry)
	writeJSON(w, http.StatusCreated, entry)
}

func (s *volumeStore) get(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/volumes/")
	for _, v := range s.volumes {
		if v.VolumeID == id || v.Name == id {
			writeJSON(w, http.StatusOK, map[string]any{
				"volumeID": v.VolumeID,
				"name":     v.Name,
				"token":    "fake-volume-jwt",
			})
			return
		}
	}
	api.WriteError(w, http.StatusNotFound, "volume not found")
}

func (s *volumeStore) delete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/volumes/")
	for i, v := range s.volumes {
		if v.VolumeID == id || v.Name == id {
			s.volumes = append(s.volumes[:i], s.volumes[i+1:]...)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	api.WriteError(w, http.StatusNotFound, "volume not found")
}

// ──────────────────────────────────────────────────────────────────────
// Snapshots list — returns empty array. Individual snapshot creation
// lives in sandboxes.go (POST /sandboxes/{id}/snapshots).
// ──────────────────────────────────────────────────────────────────────

func listSnapshots(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

// ──────────────────────────────────────────────────────────────────────
// Nodes / Admin — empty or 501.
// ──────────────────────────────────────────────────────────────────────

func listNodes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

func adminStub(w http.ResponseWriter, _ *http.Request) {
	api.WriteError(w, http.StatusNotImplemented, "admin endpoints are not available in edvabe")
}

// ──────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = readCryptoRand(b)
	const hex = "0123456789abcdef"
	out := make([]byte, n*2)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0x0f]
	}
	return string(out)
}

// readCryptoRand is a package-level var backed by crypto/rand.Read.
var readCryptoRand = cryptoRandFn

func cryptoRandFn(b []byte) (int, error) {
	return rand.Read(b)
}
