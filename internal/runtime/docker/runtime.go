// Package docker is the Phase 1 Runtime implementation backed by a
// local Docker daemon via github.com/docker/docker/client.
//
// Sandboxes are plain Docker containers. The sandbox ID is used verbatim
// as the container name, so lookups go directly through the Docker API
// without an auxiliary bookkeeping layer. A label
// (edvabe.sandbox.id=<id>) is also stamped on every container so a
// future reconnect flow can enumerate orphans on edvabe restart.
package docker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/moby/moby/client"

	"github.com/contember/edvabe/internal/runtime"
)

const (
	// LabelSandboxID stamps the sandbox ID on every container edvabe
	// creates. Used by doctor / future reconnect to enumerate managed
	// containers.
	LabelSandboxID = "edvabe.sandbox.id"
	// LabelManaged is a truthy marker so operators can filter edvabe's
	// containers apart from hand-launched ones.
	LabelManaged = "edvabe.managed"
	// LabelMetaPrefix is prepended to keys in CreateRequest.Metadata so
	// user metadata can't collide with edvabe's own labels.
	LabelMetaPrefix = "edvabe.meta."

	defaultAgentPort = 49983
)

// Runtime implements runtime.Runtime against a local Docker daemon.
//
// The zero value is not usable — construct with New.
type Runtime struct {
	cli     *client.Client
	host    string
	network string // Docker network sandboxes are attached to; empty = default bridge

	mu        sync.RWMutex
	endpoints map[string]endpoint
}

type endpoint struct {
	host string
	port int
}

// New constructs a Docker-backed Runtime, discovering the daemon socket
// via DOCKER_HOST or a list of well-known paths (Docker Desktop, Colima,
// OrbStack, Podman). Negotiates the Docker API version on first call so
// the client works across daemon versions.
//
// The Docker network sandbox containers are attached to is resolved in
// this order:
//  1. EDVABE_DOCKER_NETWORK env var (or --docker-network flag) — explicit
//  2. Auto-detected from edvabe's own container's networks when edvabe
//     runs inside Docker / Compose — zero-config for the common case
//  3. Default Docker `bridge` network
//
// (2) makes Docker Compose deployments work without the user having to
// look up and configure the compose network name.
func New() (*Runtime, error) {
	host, err := DiscoverHost()
	if err != nil {
		return nil, err
	}
	cli, err := client.NewClientWithOpts(
		client.WithHost(host),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("docker runtime: new client: %w", err)
	}
	network := os.Getenv("EDVABE_DOCKER_NETWORK")
	if network == "" {
		network = detectOwnNetwork(cli)
	}
	return &Runtime{
		cli:       cli,
		host:      host,
		network:   network,
		endpoints: make(map[string]endpoint),
	}, nil
}

// Network reports the Docker network name sandbox containers are
// attached to ("" means default bridge).
func (r *Runtime) Network() string { return r.network }

// Name is "docker".
func (r *Runtime) Name() string { return "docker" }

// Host reports the Docker host URI the runtime resolved to (useful for
// logging and doctor output).
func (r *Runtime) Host() string { return r.host }

// Close releases the underlying Docker client's HTTP resources.
func (r *Runtime) Close() error {
	if r.cli == nil {
		return nil
	}
	return r.cli.Close()
}

// DiscoverHost returns the Docker host URI to connect to, honoring
// DOCKER_HOST and otherwise probing the well-known socket paths in the
// order below. The first path that stats returns the URI `unix://<path>`.
//
//  1. $DOCKER_HOST (unchanged — may be tcp://, ssh://, unix://)
//  2. /var/run/docker.sock           — Docker Desktop / upstream
//  3. ~/.colima/docker.sock          — Colima default profile
//  4. ~/.orbstack/run/docker.sock    — OrbStack
//  5. ~/.local/share/containers/podman/machine/podman.sock — Podman
func DiscoverHost() (string, error) {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return h, nil
	}
	candidates := []string{"/var/run/docker.sock"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".colima", "docker.sock"),
			filepath.Join(home, ".orbstack", "run", "docker.sock"),
			filepath.Join(home, ".local", "share", "containers", "podman", "machine", "podman.sock"),
		)
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return "unix://" + path, nil
		}
	}
	return "", errors.New("docker runtime: no Docker socket found (set DOCKER_HOST, start Docker Desktop/Colima/OrbStack/Podman)")
}

// ensure *Runtime satisfies runtime.Runtime at compile time.
var _ runtime.Runtime = (*Runtime)(nil)
