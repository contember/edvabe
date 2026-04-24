package sandbox

import (
	"strings"

	"github.com/contember/edvabe/internal/runtime"
)

// Docker labels edvabe stamps at Create time so Rehydrate can
// reconstruct the in-memory Sandbox after edvabe restarts. Only
// immutable facts belong here — Docker labels can't be modified after
// a container is created. Mutable state (PauseMode, PausedAt) is
// derived from the live container state instead.
const (
	LabelTemplateID    = "edvabe.sandbox.template.id"
	LabelTemplateAlias = "edvabe.sandbox.template.alias"
	LabelTokenEnvd     = "edvabe.sandbox.token.envd"
	LabelTokenTraffic  = "edvabe.sandbox.token.traffic"
	LabelOnTimeout     = "edvabe.sandbox.ontimeout"

	// labelImageTemplateID is the template-id label the template build
	// system stamps on the image itself. Used as a fallback when a
	// pre-rehydration sandbox (containers created before the
	// sandbox-level labels shipped) is being reconstructed.
	labelImageTemplateID = "edvabe.template.id"
)

// buildSandboxLabels returns the label map passed to the runtime for
// a new sandbox. Only non-empty values are included so the container's
// label set stays minimal.
func buildSandboxLabels(s *Sandbox) map[string]string {
	labels := map[string]string{
		LabelTemplateID: s.TemplateID,
		LabelTokenEnvd:  s.EnvdToken,
		LabelTokenTraffic: s.TrafficToken,
	}
	if s.Alias != "" {
		labels[LabelTemplateAlias] = s.Alias
	}
	if s.OnTimeout != "" {
		labels[LabelOnTimeout] = string(s.OnTimeout)
	}
	return labels
}

// sandboxFromManaged reconstructs a Sandbox from what the runtime
// reported. Fields that can't be recovered from labels or inspect
// (ExpiresAt, PausedAt) are left to the caller to default.
func sandboxFromManaged(mc runtime.ManagedContainer) *Sandbox {
	state := StateRunning
	var pauseMode PauseMode
	switch mc.State {
	case runtime.ContainerStatePaused:
		state = StatePaused
		pauseMode = PauseFrozen
	case runtime.ContainerStateStopped:
		state = StatePaused
		pauseMode = PauseStopped
	}

	templateID := mc.Labels[LabelTemplateID]
	if templateID == "" {
		templateID = mc.Labels[labelImageTemplateID]
	}

	return &Sandbox{
		ID:           mc.SandboxID,
		TemplateID:   templateID,
		Alias:        mc.Labels[LabelTemplateAlias],
		ContainerID:  mc.ContainerID,
		AgentHost:    mc.AgentHost,
		AgentPort:    mc.AgentPort,
		EnvdToken:    mc.Labels[LabelTokenEnvd],
		TrafficToken: mc.Labels[LabelTokenTraffic],
		State:        state,
		PauseMode:    pauseMode,
		OnTimeout:    OnTimeoutMode(mc.Labels[LabelOnTimeout]),
		Metadata:     extractMetadataFromLabels(mc.Labels),
		EnvVars:      extractUserEnvVars(mc.EnvVars),
		CreatedAt:    mc.CreatedAt,
		CPUCount:     mc.CPUCount,
		MemoryMB:     mc.MemoryMB,
	}
}

// extractMetadataFromLabels pulls user metadata (edvabe.meta.* keys)
// back out of the label map — mirror of the MetaPrefix stamping the
// docker runtime does on Create.
func extractMetadataFromLabels(labels map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range labels {
		if strings.HasPrefix(k, runtime.LabelMetaPrefix) {
			out[strings.TrimPrefix(k, runtime.LabelMetaPrefix)] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// extractUserEnvVars drops the edvabe-injected EDVABE_* bootstrap vars
// so the Sandbox's EnvVars map only reflects what the user actually
// asked for at Create time.
func extractUserEnvVars(env map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range env {
		if strings.HasPrefix(k, "EDVABE_") {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
