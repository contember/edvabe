package upstream

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
)

// defaultReadyBackoff and defaultReadyJitter control the poll cadence
// when WaitReady is retrying a failed readyCmd. The SDK expects a few
// hundred milliseconds between tries — long enough that a slow startup
// process has a chance to finish, short enough that the create call
// still feels responsive.
const (
	defaultReadyBackoff = 500 * time.Millisecond
	defaultReadyJitter  = 250 * time.Millisecond
)

// readyProbeUser is the unix account edvabe runs probe commands as.
// Matches the defaultUser used in sandbox.InitAgent so the readyCmd
// sees the same $HOME and $PATH the user's own SDK calls will.
const readyProbeUser = "user"

// WaitReady runs cmd through envd's process.Process/Start RPC in a
// poll loop until the command exits with status 0 or ctx expires.
// Empty cmd is the Phase 1 fast path — returns nil without ever
// touching envd.
//
// Errors are retried silently; only the final failure (after ctx
// expires) is surfaced to the caller, and it carries the most recent
// cause so the sandbox manager can log something actionable.
func (p *UpstreamEnvdProvider) WaitReady(ctx context.Context, endpoint, cmd string) error {
	if cmd == "" {
		return nil
	}

	var lastErr error
	for {
		exitCode, err := p.runReadyProbe(ctx, endpoint, cmd)
		if err == nil && exitCode == 0 {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("readyCmd exited with code %d", exitCode)
		}

		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("wait ready: %w (last: %v)", ctxErr, lastErr)
		}

		delay := defaultReadyBackoff + time.Duration(rand.Int63n(int64(defaultReadyJitter)))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait ready: %w (last: %v)", ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
}

// runReadyProbe issues one process.Process/Start RPC and decodes the
// returned event stream. The command is executed through `sh -c` so
// readyCmd can be a normal shell expression (matches the edvabe-init
// contract — start/ready commands are shell strings).
func (p *UpstreamEnvdProvider) runReadyProbe(ctx context.Context, endpoint, cmd string) (int, error) {
	body, err := json.Marshal(processStartRequest{
		Process: processConfig{
			Cmd:  "/bin/sh",
			Args: []string{"-c", cmd},
			Envs: map[string]string{},
			Cwd:  "/home/" + readyProbeUser,
		},
	})
	if err != nil {
		return -1, fmt.Errorf("marshal start request: %w", err)
	}

	envelope := make([]byte, 0, 5+len(body))
	envelope = append(envelope, 0) // flags: plain message
	envelope = binary.BigEndian.AppendUint32(envelope, uint32(len(body)))
	envelope = append(envelope, body...)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint+"/process.Process/Start",
		bytes.NewReader(envelope),
	)
	if err != nil {
		return -1, fmt.Errorf("build start request: %w", err)
	}
	req.Header.Set("Content-Type", "application/connect+json")
	req.Header.Set("Connect-Protocol-Version", "1")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return -1, fmt.Errorf("process start: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return -1, fmt.Errorf("read start stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return -1, fmt.Errorf("process start: status %d: %s", resp.StatusCode, truncate(raw, 200))
	}

	return decodeEndExit(raw)
}

// decodeEndExit walks a Connect server-stream response buffer frame by
// frame and returns the exit code from the first EndEvent it finds.
// Frames are `{flags: 1 byte, length: 4 bytes BE, payload: <length>}`;
// flag bit 0x02 marks the trailer frame, which we ignore for the
// exit-code lookup.
func decodeEndExit(raw []byte) (int, error) {
	off := 0
	for off+5 <= len(raw) {
		flags := raw[off]
		length := binary.BigEndian.Uint32(raw[off+1 : off+5])
		end := off + 5 + int(length)
		if end > len(raw) {
			return -1, fmt.Errorf("truncated frame at %d", off)
		}
		payload := raw[off+5 : end]
		off = end

		// Trailer frames (flags & 0x02) carry gRPC-style trailer
		// metadata, not process events. Skip them.
		if flags&0x02 != 0 {
			continue
		}

		var wrapper processEventWrapper
		if err := json.Unmarshal(payload, &wrapper); err != nil {
			continue
		}
		if wrapper.Event.End == nil {
			continue
		}
		return wrapper.Event.End.ExitCode, nil
	}
	return -1, fmt.Errorf("no EndEvent in stream")
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// processStartRequest matches envd's StartRequest wire shape for the
// subset of fields readyCmd probing uses. Omitted fields (pty, tag,
// stdin) serialize as absent, which is exactly what envd expects when
// running a one-shot foreground command.
type processStartRequest struct {
	Process processConfig `json:"process"`
}

type processConfig struct {
	Cmd  string            `json:"cmd"`
	Args []string          `json:"args"`
	Envs map[string]string `json:"envs"`
	Cwd  string            `json:"cwd,omitempty"`
}

// processEventWrapper decodes just enough of envd's ProcessEvent to
// read the EndEvent's exit code. Every other event type decodes into
// a nil pointer on the End field and is ignored.
type processEventWrapper struct {
	Event processEvent `json:"event"`
}

type processEvent struct {
	End *endEvent `json:"end,omitempty"`
}

type endEvent struct {
	ExitCode int  `json:"exitCode"`
	Exited   bool `json:"exited,omitempty"`
}
