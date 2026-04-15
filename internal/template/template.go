// Package template owns the persistent metadata store for user-defined
// sandbox templates and the types shared between the control plane,
// the builder runtime, and the sandbox manager.
//
// A Template is edvabe's record of one image the user can launch a
// sandbox from. Templates are created via the E2B SDK's programmatic
// Template() builder: the SDK calls POST /v3/templates with metadata,
// uploads file contexts into the content-addressed cache, then posts
// a TemplateBuildStartV2 with a step array that the builder translates
// into a generated Dockerfile and feeds to docker build. See
// docs/06-phases.md Phase 3 for the full wire protocol.
//
// This package owns no HTTP and no Docker — higher layers compose
// those in. Builder runtime lives under internal/template/builder,
// file-context cache under internal/template/filecache.
package template

import "time"

// BuildStatus is the lifecycle state of a template build.
type BuildStatus string

const (
	// BuildStatusWaiting is set the instant a build is enqueued. In
	// local mode it transitions to building almost immediately; the
	// state exists for wire compatibility with the E2B SDK.
	BuildStatusWaiting BuildStatus = "waiting"
	// BuildStatusBuilding is set while docker build is running.
	BuildStatusBuilding BuildStatus = "building"
	// BuildStatusReady is set on successful completion. The resulting
	// image is tagged edvabe/user-<templateID>:latest.
	BuildStatusReady BuildStatus = "ready"
	// BuildStatusError is set on any failure. Build.Reason carries the
	// human-readable cause.
	BuildStatusError BuildStatus = "error"
)

// Build is one attempt at materializing a template into an image. A
// Template can accumulate multiple Builds over its lifetime; the most
// recent successful one wins for sandbox create.
type Build struct {
	ID         string      `json:"id"`
	Status     BuildStatus `json:"status"`
	Reason     string      `json:"reason,omitempty"`
	StartedAt  time.Time   `json:"startedAt"`
	FinishedAt *time.Time  `json:"finishedAt,omitempty"`
}

// Template is edvabe's persistent record of a user template.
//
// The store is the source of truth for alias → image resolution at
// sandbox create time. StartCmd and ReadyCmd are applied by the
// sandbox manager (injected into the container's env as
// EDVABE_START_CMD / EDVABE_READY_CMD), not baked into the Dockerfile.
type Template struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Tags      []string  `json:"tags,omitempty"`
	Alias     string    `json:"alias,omitempty"`
	CPUCount  int       `json:"cpuCount,omitempty"`
	MemoryMB  int       `json:"memoryMB,omitempty"`
	StartCmd  string    `json:"startCmd,omitempty"`
	ReadyCmd  string    `json:"readyCmd,omitempty"`
	ImageTag  string    `json:"imageTag,omitempty"`
	Public    bool      `json:"public"`
	CreatedAt time.Time `json:"createdAt"`
	Builds    []Build   `json:"builds,omitempty"`
}

// Step is one element of a TemplateBuildStartV2.steps array as the SDK
// sends it. Kept here (rather than under builder/) so the HTTP handler
// and the builder package can share the decoded shape without a cycle.
type Step struct {
	Type      string   `json:"type"`
	Args      []string `json:"args,omitempty"`
	FilesHash string   `json:"filesHash,omitempty"`
	Force     bool     `json:"force,omitempty"`
}

// BuildSpec is the decoded TemplateBuildStartV2 body. Carries the base
// image selection, the step array, and the start/ready commands that
// apply at sandbox create time.
type BuildSpec struct {
	FromImage         string          `json:"fromImage,omitempty"`
	FromTemplate      string          `json:"fromTemplate,omitempty"`
	FromImageRegistry *RegistryAuth   `json:"fromImageRegistry,omitempty"`
	Steps             []Step          `json:"steps,omitempty"`
	StartCmd          string          `json:"startCmd,omitempty"`
	ReadyCmd          string          `json:"readyCmd,omitempty"`
	Force             bool            `json:"force,omitempty"`
}

// RegistryAuth carries private-registry credentials from the SDK to the
// docker build invocation. Passthrough — edvabe does not introspect.
type RegistryAuth struct {
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	ServerAddress string `json:"serverAddress,omitempty"`
}

// LatestReady returns the most recent build whose Status == Ready, or
// nil if none exist yet. Used at sandbox-create to pick the image tag.
func (t *Template) LatestReady() *Build {
	for i := len(t.Builds) - 1; i >= 0; i-- {
		if t.Builds[i].Status == BuildStatusReady {
			return &t.Builds[i]
		}
	}
	return nil
}
