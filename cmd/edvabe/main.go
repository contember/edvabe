package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/contember/edvabe/internal/agent/upstream"
)

const (
	name    = "edvabe"
	version = "v0.0.0-dev (phase 1)"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	cmd, args := os.Args[1], os.Args[2:]

	switch cmd {
	case "version", "-v", "--version":
		fmt.Printf("%s %s\n", name, version)
	case "serve":
		serveCmd(args)
	case "doctor":
		doctorCmd(args)
	case "build-image":
		buildImageCmd(args)
	case "pull-base":
		pullBaseCmd(args)
	case "help", "-h", "--help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `%s — local E2B-compatible sandbox runtime

Usage:
  %s <command> [flags]

Commands:
  serve         Start the HTTP server
  doctor        Run preflight checks
  build-image   Tag pulled e2bdev/base as edvabe/base:latest
  pull-base     Pull the pinned upstream e2bdev/base image
  version       Print version and exit
  help          Show this help

Run '%s <command> --help' for command-specific flags.
`, name, name, name)
}

func serveCmd(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", 3000, "HTTP port to listen on")
	socket := fs.String("docker-socket", "", "Path to Docker socket (auto-detected if empty)")
	_ = fs.Parse(args)
	fmt.Fprintf(os.Stderr, "serve: not implemented (port=%d, socket=%q) — see docs/08-phase1-checklist.md task 10\n", *port, *socket)
}

func doctorCmd(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	_ = fs.Parse(args)
	fmt.Fprintln(os.Stderr, "doctor: not implemented — see docs/08-phase1-checklist.md task 14")
}

func buildImageCmd(args []string) {
	fs := flag.NewFlagSet("build-image", flag.ExitOnError)
	tag := fs.String("tag", "edvabe/base:latest", "local tag to apply to the upstream base image")
	// --force is accepted for compatibility with docs/task description;
	// pulls by digest are already idempotent so the flag is a no-op.
	_ = fs.Bool("force", false, "re-pull even if already present (no-op — pulls by digest are idempotent)")
	_ = fs.Parse(args)
	if err := upstream.EnsureBaseImage(context.Background(), *tag); err != nil {
		fmt.Fprintf(os.Stderr, "build-image: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("tagged %s as %s\n", upstream.BaseImageRef(), *tag)
}

func pullBaseCmd(args []string) {
	fs := flag.NewFlagSet("pull-base", flag.ExitOnError)
	_ = fs.Parse(args)
	ref := upstream.BaseImageRef()
	if err := upstream.PullBase(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "pull-base: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("pulled %s\n", ref)
}
