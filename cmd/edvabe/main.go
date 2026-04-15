package main

import (
	"flag"
	"fmt"
	"io"
	"os"
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
	case "fetch-envd":
		fetchEnvdCmd(args)
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
  build-image   Build edvabe/base:latest Docker image
  fetch-envd    Download upstream envd binary to cache
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
	force := fs.Bool("force", false, "Force rebuild even if image exists")
	_ = fs.Parse(args)
	fmt.Fprintf(os.Stderr, "build-image: not implemented (force=%v) — see docs/08-phase1-checklist.md task 5\n", *force)
}

func fetchEnvdCmd(args []string) {
	fs := flag.NewFlagSet("fetch-envd", flag.ExitOnError)
	v := fs.String("version", "0.5.7", "envd version to fetch")
	_ = fs.Parse(args)
	fmt.Fprintf(os.Stderr, "fetch-envd: not implemented (version=%q) — see docs/08-phase1-checklist.md task 4\n", *v)
}
