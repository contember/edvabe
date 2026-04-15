package control

import (
	"encoding/json"
	"errors"
	"net/http"
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
	id := strings.TrimPrefix(r.URL.Path, "/sandboxes/")
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
