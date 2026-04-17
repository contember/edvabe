package docker

import (
	"context"
	"os"
	"regexp"
	"time"

	"github.com/moby/moby/client"
)

// cgroupContainerIDPattern matches a 64-char lower-hex Docker container
// ID in /proc/self/cgroup. Covers cgroup v1 (`/docker/<id>`) and cgroup
// v2 systemd scopes (`/system.slice/docker-<id>.scope`). With cgroup v2
// + private cgroup namespace (modern Docker default) the cgroup path is
// just `0::/` and this returns no match — mountinfo is the fallback.
var cgroupContainerIDPattern = regexp.MustCompile(`[0-9a-f]{64}`)

// mountinfoContainerIDPattern captures the container ID from a
// `/containers/<id>/` path in /proc/self/mountinfo (typically the
// bind-mounted `/etc/hostname`, `/etc/resolv.conf`, etc. from the
// host's `/var/lib/docker/containers/<id>/`). Must be anchored to
// `/containers/` — the file also contains overlay2 snapshot IDs
// (`/overlay2/<64-hex>/`) that would otherwise false-match.
var mountinfoContainerIDPattern = regexp.MustCompile(`/containers/([0-9a-f]{64})`)

// detectOwnContainerID returns the ID of the container the current
// process runs in, or "" when not running in a recognized container.
// Returns "" (not an error) for host processes and runtimes that
// don't expose the ID through /proc (some Podman rootless / k8s
// setups) — callers fall back to default behavior.
func detectOwnContainerID() string {
	if data, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		if id := cgroupContainerIDPattern.FindString(string(data)); id != "" {
			return id
		}
	}
	if data, err := os.ReadFile("/proc/self/mountinfo"); err == nil {
		if m := mountinfoContainerIDPattern.FindStringSubmatch(string(data)); len(m) == 2 {
			return m[1]
		}
	}
	return ""
}

// detectOwnIPv4 returns edvabe's own IPv4 address on the given Docker
// network, or "" when we are not containerized, the network isn't
// attached, or inspection fails. Used to default --dns-answer so
// compose setups don't need to pin a static IP.
func detectOwnIPv4(cli *client.Client, network string) string {
	id := detectOwnContainerID()
	if id == "" || network == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	inspect, err := cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil || inspect.Container.NetworkSettings == nil {
		return ""
	}
	ep, ok := inspect.Container.NetworkSettings.Networks[network]
	if !ok || ep == nil {
		return ""
	}
	if !ep.IPAddress.IsValid() {
		return ""
	}
	return ep.IPAddress.String()
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
