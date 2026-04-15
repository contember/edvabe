package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/contember/edvabe/internal/agent"
	"github.com/contember/edvabe/internal/runtime"
)

const (
	defaultAgentPort  = 49983
	pingInterval      = 100 * time.Millisecond
	defaultPingWindow = 10 * time.Second
)

// UpstreamEnvdProvider is the Phase 1 AgentProvider implementation backed by
// upstream envd running inside the sandbox container.
type UpstreamEnvdProvider struct {
	httpClient *http.Client
	pingWindow time.Duration
}

// New returns the default upstream envd provider.
func New() *UpstreamEnvdProvider {
	return &UpstreamEnvdProvider{
		httpClient: &http.Client{Timeout: 5 * time.Second},
		pingWindow: defaultPingWindow,
	}
}

func (p *UpstreamEnvdProvider) Name() string { return "upstream-envd" }

func (p *UpstreamEnvdProvider) Version() string { return DefaultEnvdVersion }

func (p *UpstreamEnvdProvider) Port() int { return defaultAgentPort }

func (p *UpstreamEnvdProvider) EnsureImage(ctx context.Context, _ runtime.Runtime, tag string) error {
	return EnsureBaseImage(ctx, tag)
}

func (p *UpstreamEnvdProvider) Ping(ctx context.Context, endpoint string) error {
	deadline := time.Now().Add(p.pingWindow)
	url := endpoint + "/health"

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("build ping request: %w", err)
		}
		resp, err := p.httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				return nil
			}
			err = fmt.Errorf("unexpected status %d", resp.StatusCode)
		}

		if ctx.Err() != nil {
			return fmt.Errorf("ping envd: %w", ctx.Err())
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ping envd %s: %w", url, err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("ping envd: %w", ctx.Err())
		case <-time.After(pingInterval):
		}
	}
}

func (p *UpstreamEnvdProvider) InitAgent(ctx context.Context, endpoint string, cfg agent.InitConfig) error {
	body, err := json.Marshal(initRequest{
		AccessToken:    cfg.AccessToken,
		EnvVars:        cfg.EnvVars,
		Timestamp:      time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		DefaultUser:    cfg.DefaultUser,
		DefaultWorkdir: cfg.DefaultWorkdir,
		VolumeMounts:   cfg.VolumeMounts,
		HyperloopIP:    cfg.HyperloopIP,
	})
	if err != nil {
		return fmt.Errorf("marshal init request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/init", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build init request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post /init: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("post /init: unexpected status %d", resp.StatusCode)
	}
	return nil
}

type initRequest struct {
	AccessToken    string              `json:"accessToken"`
	EnvVars        map[string]string   `json:"envVars,omitempty"`
	Timestamp      string              `json:"timestamp"`
	DefaultUser    string              `json:"defaultUser,omitempty"`
	DefaultWorkdir string              `json:"defaultWorkdir,omitempty"`
	VolumeMounts   []agent.VolumeMount `json:"volumeMounts,omitempty"`
	HyperloopIP    string              `json:"hyperloopIP,omitempty"`
}

var _ agent.AgentProvider = (*UpstreamEnvdProvider)(nil)
