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
	TemplateID string                    `json:"templateID"`
	Alias      string                    `json:"alias,omitempty"`
	Timeout    int                       `json:"timeout"`
	Metadata   map[string]string         `json:"metadata"`
	EnvVars    map[string]string         `json:"envVars"`
	AutoPause  bool                      `json:"autoPause"`
	Lifecycle  *newSandboxLifecycleInput `json:"lifecycle,omitempty"`
	// CPUCount / MemoryMB override the template's resource caps for
	// this sandbox. Zero leaves the template default; negative means
	// "unlimited" for the override. Matches the E2B SDK field names.
	CPUCount int `json:"cpuCount,omitempty"`
	MemoryMB int `json:"memoryMB,omitempty"`
	// Accepted for wire compat but not enforced in edvabe.
	Secure              bool              `json:"secure,omitempty"`
	AllowInternetAccess *bool             `json:"allow_internet_access,omitempty"`
	Network             *json.RawMessage  `json:"network,omitempty"`
	VolumeMounts        []json.RawMessage `json:"volumeMounts,omitempty"`
	MCP                 *json.RawMessage  `json:"mcp,omitempty"`
	AutoResume          *json.RawMessage  `json:"autoResume,omitempty"`
}

// newSandboxLifecycleInput mirrors the SDK's NewSandbox.lifecycle field.
// The SDK also sends autoResume here; we accept it for wire compatibility
// but do not act on it — resume is always on via /connect.
type newSandboxLifecycleInput struct {
	OnTimeout string `json:"onTimeout"`
}

type sandboxResponse struct {
	SandboxID          string            `json:"sandboxID"`
	TemplateID         string            `json:"templateID"`
	Alias              string            `json:"alias,omitempty"`
	Aliases            []string          `json:"aliases,omitempty"`
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
		Alias:      req.Alias,
		Metadata:   req.Metadata,
		EnvVars:    req.EnvVars,
		Timeout:    time.Duration(req.Timeout) * time.Second,
		OnTimeout:  resolveOnTimeout(req),
		CPUCount:   req.CPUCount,
		MemoryMB:   req.MemoryMB,
	})
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(toSandboxResponse(manager, provider, sbx)); err != nil {
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

// snapshotRequest is the optional JSON body for POST .../snapshots.
// An empty body is fine — Snapshot derives a timestamp-based name.
type snapshotRequest struct {
	Name string `json:"name"`
}

// snapshotResponse mirrors the SDK's SnapshotInfo shape. Both the
// snapshot name and the underlying image tag are returned so callers
// can reference the snapshot by either.
type snapshotResponse struct {
	Name      string    `json:"name"`
	ImageTag  string    `json:"imageTag"`
	CreatedAt time.Time `json:"createdAt"`
}

func pauseSandbox(manager sandboxManager, w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(sandboxIDFromPath(r.URL.Path), "/pause")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	if err := manager.Pause(r.Context(), id); err != nil {
		writeManagerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resumeSandbox is the deprecated alias for /connect. The SDK used
// POST /sandboxes/{id}/resume in older releases; we route it through
// the same unpause+renew path so legacy clients keep working. Body
// shape matches timeoutRequest (the SDK sends its connect timeout
// there), but an empty body is accepted.
func resumeSandbox(manager sandboxManager, provider versionProvider, w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(sandboxIDFromPath(r.URL.Path), "/resume")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	var req timeoutRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.WriteError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	timeout := time.Duration(req.Timeout) * time.Second
	if timeout <= 0 {
		timeout = sandbox.DefaultTimeout
	}
	sbx, err := manager.Connect(r.Context(), id, timeout)
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

func snapshotSandbox(manager sandboxManager, w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(sandboxIDFromPath(r.URL.Path), "/snapshots")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	var req snapshotRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.WriteError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	info, err := manager.Snapshot(r.Context(), id, req.Name)
	if err != nil {
		writeManagerError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(snapshotResponse{
		Name:      info.Name,
		ImageTag:  info.ImageTag,
		CreatedAt: info.CreatedAt,
	}); err != nil {
		return
	}
}

// ──────────────────────────────────────────────────────────────────────
// Network update — records state but does not enforce.
// ──────────────────────────────────────────────────────────────────────

type networkUpdateRequest struct {
	AllowOut []string `json:"allowOut"`
	DenyOut  []string `json:"denyOut"`
}

func updateSandboxNetwork(manager sandboxManager, w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(sandboxIDFromPath(r.URL.Path), "/network")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	if _, err := manager.Get(id); err != nil {
		writeManagerError(w, err)
		return
	}
	// Accept and discard — edvabe does not enforce egress rules.
	var req networkUpdateRequest
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	w.WriteHeader(http.StatusNoContent)
}

// ──────────────────────────────────────────────────────────────────────
// Refreshes — legacy keepalive: treat as a timeout reset.
// ──────────────────────────────────────────────────────────────────────

type refreshRequest struct {
	Duration int `json:"duration"`
}

func refreshSandbox(manager sandboxManager, w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(sandboxIDFromPath(r.URL.Path), "/refreshes")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	var req refreshRequest
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.Duration > 0 {
		if err := manager.SetTimeout(id, time.Duration(req.Duration)*time.Second); err != nil {
			writeManagerError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// ──────────────────────────────────────────────────────────────────────
// Logs — empty paginated stub. Envd's own stdout is not captured yet.
// ──────────────────────────────────────────────────────────────────────

func getSandboxLogs(manager sandboxManager, w http.ResponseWriter, r *http.Request) {
	// Path: /v2/sandboxes/{id}/logs
	path := strings.TrimPrefix(r.URL.Path, "/v2/sandboxes/")
	id := strings.TrimSuffix(path, "/logs")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	if _, err := manager.Get(id); err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"logs":    []any{},
		"hasMore": false,
	})
}

// ──────────────────────────────────────────────────────────────────────
// Metrics — sourced from runtime.Stats where possible.
// ──────────────────────────────────────────────────────────────────────

type metricsEntry struct {
	Timestamp          time.Time `json:"timestamp"`
	CPUCount           int       `json:"cpuCount"`
	CPUUsedPct         float64   `json:"cpuUsedPct"`
	MemTotalMiB        int64     `json:"memTotalMiB"`
	MemUsedMiB         int64     `json:"memUsedMiB"`
	DiskTotalMiB       int64     `json:"diskTotalMiB"`
	DiskUsedMiB        int64     `json:"diskUsedMiB"`
}

func getSandboxMetrics(manager sandboxManager, rt runtime.Runtime, w http.ResponseWriter, r *http.Request) {
	// Path: /sandboxes/{id}/metrics
	id := strings.TrimSuffix(sandboxIDFromPath(r.URL.Path), "/metrics")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	if _, err := manager.Get(id); err != nil {
		writeManagerError(w, err)
		return
	}
	stats, err := rt.Stats(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusOK, []metricsEntry{})
		return
	}
	writeJSON(w, http.StatusOK, []metricsEntry{{
		Timestamp:    time.Now(),
		CPUUsedPct:   stats.CPUUsedPercent,
		MemTotalMiB:  stats.MemoryLimitMB,
		MemUsedMiB:   stats.MemoryUsedMB,
		DiskUsedMiB:  stats.DiskUsedMB,
	}})
}

func getBatchSandboxMetrics(manager sandboxManager, rt runtime.Runtime, w http.ResponseWriter, r *http.Request) {
	// Path: /sandboxes/metrics?sandbox_ids=a,b,c
	ids := strings.Split(r.URL.Query().Get("sandbox_ids"), ",")
	result := map[string][]metricsEntry{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, err := manager.Get(id); err != nil {
			result[id] = []metricsEntry{}
			continue
		}
		stats, err := rt.Stats(r.Context(), id)
		if err != nil {
			result[id] = []metricsEntry{}
			continue
		}
		result[id] = []metricsEntry{{
			Timestamp:   time.Now(),
			CPUUsedPct:  stats.CPUUsedPercent,
			MemTotalMiB: stats.MemoryLimitMB,
			MemUsedMiB:  stats.MemoryUsedMB,
			DiskUsedMiB: stats.DiskUsedMB,
		}}
	}
	writeJSON(w, http.StatusOK, result)
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
	var aliases []string
	if sbx.Alias != "" {
		aliases = []string{sbx.Alias}
	}
	return sandboxResponse{
		SandboxID:          sbx.ID,
		TemplateID:         sbx.TemplateID,
		Alias:              sbx.Alias,
		Aliases:            aliases,
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
	onTimeout := string(sbx.OnTimeout)
	if onTimeout == "" {
		onTimeout = string(sandbox.OnTimeoutKill)
	}
	resp := sandboxDetailResponse{
		sandboxResponse:     toSandboxResponse(manager, provider, sbx),
		State:               sbx.State,
		CPUCount:            sbx.CPUCount,
		MemoryMB:            int64(sbx.MemoryMB),
		Lifecycle:           sandboxLifecycle{OnTimeout: onTimeout, AutoResume: sandboxAutoResumeMode{Enabled: sbx.OnTimeout == sandbox.OnTimeoutPause}},
		Network:             sandboxNetworkConfig{AllowPublicTraffic: true, AllowOut: []string{}, DenyOut: []string{}},
		AllowInternetAccess: true,
		EnvVars:             sbx.EnvVars,
	}
	// Fall back to runtime stats for sandboxes created before this
	// field existed (CPUCount/MemoryMB == 0 means "unlimited" on the
	// request side, but we'd rather echo the cgroup limit than zero).
	if stats != nil {
		if resp.MemoryMB == 0 {
			resp.MemoryMB = stats.MemoryLimitMB
		}
		resp.DiskSizeMB = stats.DiskUsedMB
	}
	return resp
}

// resolveOnTimeout picks the sandbox lifecycle mode from the incoming
// request. Both autoPause (legacy shorthand) and lifecycle.onTimeout
// are accepted; lifecycle.onTimeout wins when both are set, mirroring
// the SDK's own precedence. Unknown onTimeout values fall through to
// the default kill mode so a typo doesn't silently leak a container.
func resolveOnTimeout(req newSandboxRequest) sandbox.OnTimeoutMode {
	if req.Lifecycle != nil {
		switch req.Lifecycle.OnTimeout {
		case string(sandbox.OnTimeoutPause):
			return sandbox.OnTimeoutPause
		case string(sandbox.OnTimeoutKill):
			return sandbox.OnTimeoutKill
		}
	}
	if req.AutoPause {
		return sandbox.OnTimeoutPause
	}
	return sandbox.OnTimeoutKill
}
