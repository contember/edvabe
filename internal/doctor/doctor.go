// Package doctor runs a preflight check over the environment edvabe needs
// to `serve` and prints an aligned pass/fail table. It is the `edvabe
// doctor` subcommand's backend.
package doctor

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"github.com/contember/edvabe/internal/agent/upstream"
	"github.com/contember/edvabe/internal/runtime/docker"
	"github.com/contember/edvabe/internal/sandbox"
)

const (
	// minDockerMajor.minDockerMinor is the oldest Docker server version
	// edvabe supports. Docker Desktop defaults to 4.x (engine 20.10+)
	// and every mainstream host has been well past this for years.
	minDockerMajor = 20
	minDockerMinor = 10
)

// Options configure a doctor Run.
type Options struct {
	// Port is the TCP port `edvabe serve` will bind. Defaults to 3000.
	Port int
	// BaseImage is the tag that `edvabe build-image` produces. Defaults
	// to sandbox.DefaultImage.
	BaseImage string
	// EnvdSourceImage is the scratch image `edvabe build-image` produces
	// alongside BaseImage. Defaults to upstream.EnvdSourceTag.
	EnvdSourceImage string
	// CodeInterpreterImage is the code-interpreter image tag. Defaults
	// to upstream.CodeInterpreterTag. Its absence is informational, not
	// a hard failure.
	CodeInterpreterImage string
	// PausePolicy, when non-zero, is surfaced as an info line so users
	// can see the limits that `edvabe serve` will apply.
	PausePolicy sandbox.PausePolicy
}

// Run executes each check in order, prints an aligned table to w, and
// returns a non-nil error if any check failed. The error message is a
// short summary — the detailed per-check output has already been
// written to w.
func Run(ctx context.Context, w io.Writer, opts Options) error {
	if opts.Port == 0 {
		opts.Port = 3000
	}
	if opts.BaseImage == "" {
		opts.BaseImage = sandbox.DefaultImage
	}
	if opts.EnvdSourceImage == "" {
		opts.EnvdSourceImage = upstream.EnvdSourceTag
	}
	if opts.CodeInterpreterImage == "" {
		opts.CodeInterpreterImage = upstream.CodeInterpreterTag
	}

	checks := []checkFunc{
		checkDockerSocket,
		checkDockerVersion,
		checkImage(opts.BaseImage, "run `edvabe build-image`"),
		checkImage(opts.EnvdSourceImage, "run `edvabe build-image`"),
		checkImageOptional(opts.CodeInterpreterImage, "run `edvabe build-image --template=code-interpreter`"),
		checkPortFree(opts.Port),
		checkPausePolicy(opts.PausePolicy),
	}

	results := make([]checkResult, 0, len(checks))
	state := &runState{}
	for _, c := range checks {
		r := c(ctx, state)
		results = append(results, r)
	}
	if state.cli != nil {
		_ = state.cli.Close()
	}

	printResults(w, results)

	for _, r := range results {
		if !r.ok {
			return fmt.Errorf("%d of %d checks failed", countFailed(results), len(results))
		}
	}
	return nil
}

// runState carries already-resolved artefacts between checks so each
// check does not redo connect + discover + negotiate work.
type runState struct {
	host string
	cli  *client.Client
}

type checkResult struct {
	name string
	ok   bool
	// detail is the parenthesized suffix on the OK line, or the failure
	// reason on the FAIL line. Kept to one line.
	detail string
}

type checkFunc func(ctx context.Context, state *runState) checkResult

func checkDockerSocket(ctx context.Context, state *runState) checkResult {
	host, err := docker.DiscoverHost()
	if err != nil {
		return checkResult{name: "Docker socket", ok: false, detail: err.Error()}
	}
	cli, err := client.NewClientWithOpts(
		client.WithHost(host),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return checkResult{name: "Docker socket", ok: false, detail: "new client: " + err.Error()}
	}

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := cli.Ping(pingCtx, client.PingOptions{}); err != nil {
		_ = cli.Close()
		return checkResult{name: "Docker socket", ok: false, detail: "ping: " + err.Error()}
	}

	state.host = host
	state.cli = cli
	return checkResult{name: "Docker socket", ok: true, detail: host}
}

func checkDockerVersion(ctx context.Context, state *runState) checkResult {
	if state.cli == nil {
		return checkResult{name: "Docker version", ok: false, detail: "skipped: no daemon connection"}
	}
	vctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	res, err := state.cli.ServerVersion(vctx, client.ServerVersionOptions{})
	if err != nil {
		return checkResult{name: "Docker version", ok: false, detail: err.Error()}
	}
	major, minor, ok := parseMajorMinor(res.Version)
	if !ok {
		return checkResult{name: "Docker version", ok: false, detail: "unparseable version " + strconv.Quote(res.Version)}
	}
	if major < minDockerMajor || (major == minDockerMajor && minor < minDockerMinor) {
		return checkResult{
			name:   "Docker version",
			ok:     false,
			detail: fmt.Sprintf("%s — need ≥ %d.%d", res.Version, minDockerMajor, minDockerMinor),
		}
	}
	return checkResult{name: "Docker version", ok: true, detail: res.Version}
}

// checkImage verifies that the given image tag is present in the local
// Docker daemon. hint is surfaced in the failure detail line so users
// get an actionable next step.
func checkImage(tag, hint string) checkFunc {
	return func(ctx context.Context, state *runState) checkResult {
		name := fmt.Sprintf("%s image", tag)
		if state.cli == nil {
			return checkResult{name: name, ok: false, detail: "skipped: no daemon connection"}
		}
		ictx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		filters := client.Filters{}.Add("reference", tag)
		res, err := state.cli.ImageList(ictx, client.ImageListOptions{Filters: filters})
		if err != nil {
			return checkResult{name: name, ok: false, detail: err.Error()}
		}
		if len(res.Items) == 0 {
			return checkResult{
				name:   name,
				ok:     false,
				detail: "not found — " + hint,
			}
		}
		return checkResult{name: name, ok: true, detail: ""}
	}
}

// checkImageOptional is like checkImage but a missing image is not a
// failure — it reports OK when present and INFO when absent. The
// code-interpreter image is optional; users only need it if they use
// the @e2b/code-interpreter SDK.
func checkImageOptional(tag, hint string) checkFunc {
	return func(ctx context.Context, state *runState) checkResult {
		name := fmt.Sprintf("%s image", tag)
		if state.cli == nil {
			return checkResult{name: name, ok: true, detail: "skipped: no daemon connection"}
		}
		ictx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		filters := client.Filters{}.Add("reference", tag)
		res, err := state.cli.ImageList(ictx, client.ImageListOptions{Filters: filters})
		if err != nil {
			return checkResult{name: name, ok: true, detail: "check failed: " + err.Error()}
		}
		if len(res.Items) == 0 {
			return checkResult{
				name:   name,
				ok:     true,
				detail: "not built — " + hint,
			}
		}
		return checkResult{name: name, ok: true, detail: ""}
	}
}

// checkPausePolicy is info-only — it surfaces the configured pause
// limits so users can confirm the knobs they set via env / flags took
// effect. Always reports OK; a missing policy renders as "default".
func checkPausePolicy(p sandbox.PausePolicy) checkFunc {
	return func(_ context.Context, _ *runState) checkResult {
		if p == (sandbox.PausePolicy{}) {
			p = sandbox.PausePolicy{
				FreezeDuration: sandbox.DefaultFreezeDuration,
				MaxFrozen:      sandbox.DefaultMaxFrozen,
				StoppedGCAfter: sandbox.DefaultStoppedGCAfter,
			}
		}
		detail := fmt.Sprintf("freeze=%s maxFrozen=%d gc=%s",
			humanDuration(p.FreezeDuration),
			p.MaxFrozen,
			humanDuration(p.StoppedGCAfter),
		)
		return checkResult{name: "Pause policy", ok: true, detail: detail}
	}
}

// humanDuration renders a duration with integer hour / day precision
// when possible so "24h0m0s" becomes "24h" and "720h0m0s" becomes "30d".
// Negative or zero values render as "off".
func humanDuration(d time.Duration) string {
	if d <= 0 {
		return "off"
	}
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return d.String()
}

func checkPortFree(port int) checkFunc {
	return func(_ context.Context, _ *runState) checkResult {
		name := fmt.Sprintf("Port %d free", port)
		addr := fmt.Sprintf(":%d", port)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return checkResult{name: name, ok: false, detail: err.Error()}
		}
		_ = ln.Close()
		return checkResult{name: name, ok: true, detail: ""}
	}
}

// parseMajorMinor extracts the leading "<major>.<minor>" from a Docker
// version string like "26.1.4" or "20.10.24+dfsg1". Returns ok=false if
// either component is missing.
func parseMajorMinor(v string) (int, int, bool) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err := strconv.Atoi(stripTrailingNonDigits(parts[1]))
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// stripTrailingNonDigits keeps only the leading run of digits from s so
// "10-dev" → "10" and "10" → "10".
func stripTrailingNonDigits(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return s[:i]
		}
	}
	return s
}

func countFailed(results []checkResult) int {
	n := 0
	for _, r := range results {
		if !r.ok {
			n++
		}
	}
	return n
}

func printResults(w io.Writer, results []checkResult) {
	const width = 36
	for _, r := range results {
		name := r.name
		if len(name) > width {
			name = name[:width]
		}
		dots := strings.Repeat(".", width-len(name))
		status := "OK"
		if !r.ok {
			status = "FAIL"
		}
		line := fmt.Sprintf("%s %s %s", name, dots, status)
		if r.detail != "" {
			line += " (" + r.detail + ")"
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w)
	failed := countFailed(results)
	if failed == 0 {
		fmt.Fprintln(w, "All checks passed.")
		return
	}
	fmt.Fprintf(w, "%d of %d checks failed.\n", failed, len(results))
}
