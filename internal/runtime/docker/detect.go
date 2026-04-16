package docker

import (
	"context"
	"os"
	"regexp"
	"time"

	"github.com/moby/moby/client"
)

// containerIDPattern matches a 64-char lower-hex Docker container ID.
// Covers cgroup v1 (`/docker/<id>`), cgroup v2 systemd
// (`/system.slice/docker-<id>.scope`), Compose (`/docker/<id>`) and
// mountinfo paths (`/var/lib/docker/containers/<id>/...`).
var containerIDPattern = regexp.MustCompile(`[0-9a-f]{64}`)

func parseContainerID(contents string) string {
	return containerIDPattern.FindString(contents)
}

// detectOwnContainerID returns the ID of the container the current
// process runs in, or "" when not running in a recognized container.
// Returns "" (not an error) for host processes and runtimes that
// don't expose the ID through /proc (some Podman rootless / k8s
// setups) — callers fall back to default behavior.
func detectOwnContainerID() string {
	for _, path := range []string{"/proc/self/cgroup", "/proc/self/mountinfo"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if id := parseContainerID(string(data)); id != "" {
			return id
		}
	}
	return ""
}

// detectOwnNetwork returns the first non-default Docker network that
// edvabe's own container is attached to, or "" when we are not
// containerized, only use default networks (bridge/host/none), or the
// Docker lookup fails. Used to auto-attach sandbox containers to the
// same network so they're reachable from edvabe without explicit
// EDVABE_DOCKER_NETWORK configuration (Docker Compose default case).
func detectOwnNetwork(cli *client.Client) string {
	id := detectOwnContainerID()
	if id == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	inspect, err := cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return ""
	}
	if inspect.Container.NetworkSettings == nil {
		return ""
	}
	for name := range inspect.Container.NetworkSettings.Networks {
		switch name {
		case "bridge", "host", "none":
			continue
		}
		return name
	}
	return ""
}
