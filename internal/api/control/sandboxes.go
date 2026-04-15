package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/contember/edvabe/internal/api"
	"github.com/contember/edvabe/internal/runtime"
	"github.com/contember/edvabe/internal/sandbox"
)

type newSandboxRequest struct {
	TemplateID string            `json:"templateID"`
	Timeout    int               `json:"timeout"`
	Metadata   map[string]string `json:"metadata"`
	EnvVars    map[string]string `json:"envVars"`
}

type sandboxResponse struct {
	SandboxID          string            `json:"sandboxID"`
	TemplateID         string            `json:"templateID"`
	ClientID           string            `json:"clientID"`
	EnvdVersion        string            `json:"envdVersion"`
	EnvdAccessToken    string            `json:"envdAccessToken"`
	TrafficAccessToken string            `json:"trafficAccessToken"`
	Domain             string            `json:"domain"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	StartedAt          time.Time         `json:"startedAt"`
	EndAt              time.Time         `json:"endAt"`
}

type sandboxDetailResponse struct {
	sandboxResponse
	State               sandbox.State        `json:"state"`
	CPUCount            int                  `json:"cpuCount"`
	MemoryMB            int64                `json:"memoryMB"`
	DiskSizeMB          int64                `json:"diskSizeMB"`
	Lifecycle           sandboxLifecycle     `json:"lifecycle"`
	Network             sandboxNetworkConfig `json:"network"`
	AllowInternetAccess bool                 `json:"allowInternetAccess"`
	EnvVars             map[string]string    `json:"envVars,omitempty"`
}

type sandboxLifecycle struct {
	OnTimeout  string                `json:"onTimeout"`
	AutoResume sandboxAutoResumeMode `json:"autoResume"`
}

type sandboxAutoResumeMode struct {
	Enabled bool `json:"enabled"`
}

type sandboxNetworkConfig struct {
	AllowPublicTraffic bool     `json:"allowPublicTraffic"`
	AllowOut           []string `json:"allowOut"`
	DenyOut            []string `json:"denyOut"`
}

type timeoutRequest struct {
	Timeout int `json:"timeout"`
}

func createSandbox(manager sandboxManager, provider versionProvider, w http.ResponseWriter, r *http.Request) {
	var req newSandboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	sbx, err := manager.Create(r.Context(), sandbox.CreateOptions{
		TemplateID: req.TemplateID,
		Metadata:   req.Metadata,
		EnvVars:    req.EnvVars,
		Timeout:    time.Duration(req.Timeout) * time.Second,
	})
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(toSandboxResponse(manager, provider, sbx)); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "encode response")
		return
	}
}

func getSandbox(manager sandboxManager, rt runtime.Runtime, provider versionProvider, w http.ResponseWriter, r *http.Request) {
	id := sandboxIDFromPath(r.URL.Path)
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}

	sbx, err := manager.Get(id)
	if err != nil {
		if errors.Is(err, sandbox.ErrNotFound) {
			api.WriteError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	stats, err := rt.Stats(r.Context(), id)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(toSandboxDetailResponse(manager, provider, sbx, stats)); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "encode response")
		return
	}
}

func listSandboxes(manager sandboxManager, provider versionProvider, w http.ResponseWriter, r *http.Request) {
	list := manager.List()
	states := parseStateFilter(r.URL.Query().Get("state"))
	if len(states) > 0 {
		filtered := list[:0]
		for _, sbx := range list {
			if _, ok := states[sbx.State]; ok {
				filtered = append(filtered, sbx)
			}
		}
		list = filtered
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].ID < list[j].ID
		}
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})

	offset := 0
	if next := r.URL.Query().Get("nextToken"); next != "" {
		parsed, err := strconv.Atoi(next)
		if err != nil || parsed < 0 {
			api.WriteError(w, http.StatusBadRequest, "invalid nextToken")
			return
		}
		offset = parsed
	}
	if offset > len(list) {
		offset = len(list)
	}

	limit := len(list)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			api.WriteError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}

	page := list[offset:]
	if limit < len(page) {
		page = page[:limit]
		w.Header().Set("X-Next-Token", strconv.Itoa(offset+limit))
	}

	resp := make([]sandboxResponse, 0, len(page))
	for _, sbx := range page {
		resp = append(resp, toSandboxResponse(manager, provider, sbx))
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "encode response")
		return
	}
}

func deleteSandbox(manager sandboxManager, w http.ResponseWriter, r *http.Request) {
	id := sandboxIDFromPath(r.URL.Path)
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	if err := manager.Destroy(r.Context(), id); err != nil {
		if errors.Is(err, sandbox.ErrNotFound) {
			api.WriteError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func setSandboxTimeout(manager sandboxManager, w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(sandboxIDFromPath(r.URL.Path), "/timeout")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	var req timeoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := manager.SetTimeout(id, time.Duration(req.Timeout)*time.Second); err != nil {
		writeManagerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func connectSandbox(manager sandboxManager, provider versionProvider, w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(sandboxIDFromPath(r.URL.Path), "/connect")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	var req timeoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sbx, err := manager.Connect(r.Context(), id, time.Duration(req.Timeout)*time.Second)
	if err != nil {
		writeManagerError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(toSandboxResponse(manager, provider, sbx)); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "encode response")
		return
	}
}

func parseStateFilter(raw string) map[sandbox.State]struct{} {
	if raw == "" {
		return nil
	}
	out := make(map[sandbox.State]struct{})
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out[sandbox.State(part)] = struct{}{}
	}
	return out
}

func sandboxIDFromPath(path string) string {
	return strings.TrimPrefix(path, "/sandboxes/")
}

func writeManagerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sandbox.ErrNotFound):
		api.WriteError(w, http.StatusNotFound, "sandbox not found")
	case errors.Is(err, sandbox.ErrExpired):
		api.WriteError(w, http.StatusGone, "sandbox expired")
	default:
		api.WriteError(w, http.StatusInternalServerError, err.Error())
	}
}

func toSandboxResponse(manager sandboxManager, provider versionProvider, sbx *sandbox.Sandbox) sandboxResponse {
	return sandboxResponse{
		SandboxID:          sbx.ID,
		TemplateID:         sbx.TemplateID,
		ClientID:           "local",
		EnvdVersion:        provider.Version(),
		EnvdAccessToken:    sbx.EnvdToken,
		TrafficAccessToken: sbx.TrafficToken,
		Domain:             manager.Domain(),
		Metadata:           sbx.Metadata,
		StartedAt:          sbx.CreatedAt,
		EndAt:              sbx.ExpiresAt,
	}
}

func toSandboxDetailResponse(manager sandboxManager, provider versionProvider, sbx *sandbox.Sandbox, stats *runtime.Stats) sandboxDetailResponse {
	resp := sandboxDetailResponse{
		sandboxResponse:     toSandboxResponse(manager, provider, sbx),
		State:               sbx.State,
		Lifecycle:           sandboxLifecycle{OnTimeout: "kill", AutoResume: sandboxAutoResumeMode{Enabled: false}},
		Network:             sandboxNetworkConfig{AllowPublicTraffic: true, AllowOut: []string{}, DenyOut: []string{}},
		AllowInternetAccess: true,
		EnvVars:             sbx.EnvVars,
	}
	if stats != nil {
		resp.MemoryMB = stats.MemoryLimitMB
		resp.DiskSizeMB = stats.DiskUsedMB
	}
	return resp
}
