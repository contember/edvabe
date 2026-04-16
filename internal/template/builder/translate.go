// Package builder owns the async template build runtime — translator
// from SDK step arrays into generated Dockerfiles, file-context
// staging, and the BuildManager state machine that drives docker build
// to completion.
//
// The E2B JS SDK serializes a programmatic template into a small JSON
// wire format with exactly five step types — COPY, RUN, WORKDIR, USER,
// ENV — plus two out-of-band fields (startCmd, readyCmd) that the
// sandbox manager, not the Dockerfile, consumes. Everything else the
// SDK's fluent API exposes (aptInstall, pipInstall, makeDir, rename,
// remove, gitClone, …) compiles down to a RUN step client-side before
// it reaches us. See
// test/e2e/ts/node_modules/e2b/dist/index.mjs:5485-5646 for the full
// list.
//
// This package is pure — no Docker, no HTTP. The Manager in manager.go
// composes it with a runtime.Runtime executor.
package builder

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/contember/edvabe/internal/template"
)

// EnvdSourceImage is the scratch image edvabe builds once at startup
// that holds the envd binary and the edvabe-init wrapper. Every
// generated Dockerfile appends a final stage that copies from this
// image so user templates do not need to install envd themselves.
const EnvdSourceImage = "edvabe/envd-source:latest"

// DefaultEnvdInitPath is the path we place the edvabe-init wrapper at
// inside the final image. The wrapper launches envd in the background
// and then the user's startCmd, so both live alongside each other for
// the lifetime of the sandbox.
const DefaultEnvdInitPath = "/usr/local/bin/edvabe-init"

// Input is the translator's sole argument. All fields are required
// except where noted; the zero value is not usable.
type Input struct {
	// FromImage is the base image for the generated Dockerfile. Either
	// this or FromTemplateImage must be set, not both.
	FromImage string
	// FromTemplateImage is the resolved image tag of a parent template
	// (set by the caller after looking up TemplateBuildStartV2.fromTemplate
	// against the template store). Mutually exclusive with FromImage.
	FromTemplateImage string
	// Steps is the ordered SDK step list as received on the wire.
	Steps []template.Step
	// StagingDir is the relative path, inside the docker build
	// context, where extracted file contexts live. Each step's
	// filesHash gets extracted into <StagingDir>/<hash>/ so that
	// the generated COPY lines can reference them. Defaults to "ctx"
	// if empty.
	StagingDir string
}

// Output is the translator's result.
type Output struct {
	// Dockerfile is the generated Dockerfile as a single string. It
	// ends with a newline.
	Dockerfile string
	// RequiredFileHashes is the deduplicated list of filesHash values
	// referenced by COPY steps. The BuildManager uses this to stage
	// file contexts from the cache before invoking docker build.
	RequiredFileHashes []string
}

// Translate converts an SDK step array into a Dockerfile. The output
// Dockerfile has three sections:
//
//  1. FROM <base>
//  2. one or more lines per step, in order
//  3. an envd injection tail that copies envd + edvabe-init from the
//     EnvdSourceImage and rewrites CMD to the edvabe-init wrapper
//
// startCmd / readyCmd are *not* emitted into the Dockerfile — they
// travel through the sandbox manager as EDVABE_START_CMD /
// EDVABE_READY_CMD environment variables at container create time.
func Translate(in Input) (*Output, error) {
	if in.FromImage == "" && in.FromTemplateImage == "" {
		return nil, fmt.Errorf("builder: translator: FromImage or FromTemplateImage required")
	}
	if in.FromImage != "" && in.FromTemplateImage != "" {
		return nil, fmt.Errorf("builder: translator: FromImage and FromTemplateImage are mutually exclusive")
	}

	staging := in.StagingDir
	if staging == "" {
		staging = "ctx"
	}

	var df strings.Builder
	base := in.FromImage
	if base == "" {
		base = in.FromTemplateImage
	}
	df.WriteString("FROM ")
	df.WriteString(base)
	df.WriteByte('\n')

	// Ensure the "user" account exists early — envd refuses to run
	// processes if defaultUser is missing, and build steps (e.g.
	// chown user:user) may reference it. Placed right after FROM so
	// it's available before any user steps. Idempotent — `id -u user`
	// short-circuits if the account already exists. Passwordless sudo
	// matches what the E2B base image provides.
	df.WriteString("RUN id -u user >/dev/null 2>&1 || " +
		"(useradd -m -s /bin/bash user && " +
		"echo 'user ALL=(ALL:ALL) NOPASSWD:ALL' >> /etc/sudoers && " +
		"chmod 755 /home/user)\n")

	// currentUser is the Dockerfile's persistent USER context. It
	// defaults to root for a fresh FROM — that matches Docker's own
	// default when the base image does not set USER. RUN steps that
	// carry an args[1] override temporarily switch USER just for the
	// one RUN and then restore; explicit USER steps update the
	// persistent value directly.
	currentUser := "root"

	seenHashes := make(map[string]struct{})
	var requiredHashes []string

	for i, step := range in.Steps {
		if step.Force {
			// Cache-bust this step so docker build treats it as fresh
			// even if an identical-looking layer is sitting in the
			// layer cache. An ARG is the lightest-weight way to do
			// this; it does not affect runtime state.
			bust, err := randomBust()
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(&df, "ARG EDVABE_CACHE_BUST_%d=%s\n", i, bust)
		}

		switch step.Type {
		case "FROM":
			return nil, fmt.Errorf("builder: translator: step %d: FROM is implicit, not a valid step type", i)

		case "RUN":
			if len(step.Args) == 0 {
				return nil, fmt.Errorf("builder: translator: step %d: RUN requires at least one arg (the command)", i)
			}
			cmd := step.Args[0]
			runAs := ""
			if len(step.Args) >= 2 {
				runAs = step.Args[1]
			}
			if runAs != "" && runAs != currentUser {
				fmt.Fprintf(&df, "USER %s\n", runAs)
				fmt.Fprintf(&df, "RUN %s\n", cmd)
				fmt.Fprintf(&df, "USER %s\n", currentUser)
			} else {
				fmt.Fprintf(&df, "RUN %s\n", cmd)
			}

		case "COPY":
			if len(step.Args) < 2 {
				return nil, fmt.Errorf("builder: translator: step %d: COPY requires at least [src, dest]", i)
			}
			if step.FilesHash == "" {
				return nil, fmt.Errorf("builder: translator: step %d: COPY requires filesHash", i)
			}
			src := step.Args[0]
			dest := step.Args[1]
			var flags string
			if len(step.Args) >= 3 && step.Args[2] != "" {
				flags += " --chown=" + step.Args[2]
			}
			if len(step.Args) >= 4 && step.Args[3] != "" {
				flags += " --chmod=" + step.Args[3]
			}
			// Files are staged under <staging>/<hash>/<original-src-path>.
			// The generated COPY path preserves the original src string
			// so that Docker's native glob handling continues to work.
			fmt.Fprintf(&df, "COPY%s %s/%s/%s %s\n", flags, staging, step.FilesHash, src, dest)
			if _, seen := seenHashes[step.FilesHash]; !seen {
				seenHashes[step.FilesHash] = struct{}{}
				requiredHashes = append(requiredHashes, step.FilesHash)
			}

		case "WORKDIR":
			if len(step.Args) == 0 {
				return nil, fmt.Errorf("builder: translator: step %d: WORKDIR requires an arg", i)
			}
			fmt.Fprintf(&df, "WORKDIR %s\n", step.Args[0])

		case "USER":
			if len(step.Args) == 0 {
				return nil, fmt.Errorf("builder: translator: step %d: USER requires an arg", i)
			}
			currentUser = step.Args[0]
			fmt.Fprintf(&df, "USER %s\n", currentUser)

		case "ENV":
			if len(step.Args)%2 != 0 {
				return nil, fmt.Errorf("builder: translator: step %d: ENV args must be key/value pairs (even count)", i)
			}
			if len(step.Args) == 0 {
				continue
			}
			df.WriteString("ENV")
			for j := 0; j < len(step.Args); j += 2 {
				fmt.Fprintf(&df, " %s=%s", step.Args[j], quoteEnvValue(step.Args[j+1]))
			}
			df.WriteByte('\n')

		default:
			return nil, fmt.Errorf("builder: translator: step %d: unsupported step type %q", i, step.Type)
		}
	}

	// Envd injection tail. Copies envd + edvabe-init from the scratch
	// envd-source image and rewires CMD to the wrapper.
	fmt.Fprintf(&df, "COPY --from=%s /usr/local/bin/envd /usr/local/bin/envd\n", EnvdSourceImage)
	fmt.Fprintf(&df, "COPY --from=%s /usr/local/bin/edvabe-init %s\n", EnvdSourceImage, DefaultEnvdInitPath)
	fmt.Fprintf(&df, "CMD [%q]\n", DefaultEnvdInitPath)

	return &Output{
		Dockerfile:         df.String(),
		RequiredFileHashes: requiredHashes,
	}, nil
}

// quoteEnvValue wraps a value in double quotes if it contains
// whitespace or shell metacharacters. Dockerfile ENV tolerates bare
// values only when they are simple tokens.
func quoteEnvValue(v string) string {
	if v == "" {
		return `""`
	}
	needsQuote := false
	for _, r := range v {
		if r == ' ' || r == '\t' || r == '"' || r == '\\' || r == '$' {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return v
	}
	// Conservative escape: backslash-escape existing quotes and
	// backslashes, wrap in double quotes.
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range v {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

func randomBust() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("builder: translator: random cache bust: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}
