package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"time"

	"github.com/contember/edvabe/internal/agent/upstream"
	api "github.com/contember/edvabe/internal/api"
	"github.com/contember/edvabe/internal/api/control"
	"github.com/contember/edvabe/internal/doctor"
	"github.com/contember/edvabe/internal/runtime/docker"
	"github.com/contember/edvabe/internal/sandbox"
	"github.com/contember/edvabe/internal/template"
	"github.com/contember/edvabe/internal/template/filecache"
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
	if *socket != "" {
		if err := os.Setenv("DOCKER_HOST", "unix://"+*socket); err != nil {
			fmt.Fprintf(os.Stderr, "serve: set DOCKER_HOST: %v\n", err)
			os.Exit(1)
		}
	}

	rt, err := docker.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: init runtime: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = rt.Close() }()

	ap := upstream.New()
	if err := ap.EnsureImage(context.Background(), rt, sandbox.DefaultImage); err != nil {
		fmt.Fprintf(os.Stderr, "serve: ensure image: %v\n", err)
		os.Exit(1)
	}

	domain := fmt.Sprintf("localhost:%d", *port)
	mgr, err := sandbox.NewManager(sandbox.Options{Runtime: rt, Agent: ap, Domain: domain})
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: init manager: %v\n", err)
		os.Exit(1)
	}

	templateStore, err := template.NewStore(template.Options{Path: templateStorePath()})
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: init template store: %v\n", err)
		os.Exit(1)
	}

	fileCache, err := filecache.New(fileCacheDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: init file cache: %v\n", err)
		os.Exit(1)
	}
	fileSigner, err := filecache.NewRandomSigner(10 * time.Minute)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: init file signer: %v\n", err)
		os.Exit(1)
	}

	controlHandler := control.NewRouter(control.RouterOptions{
		Manager:    mgr,
		Runtime:    rt,
		Provider:   ap,
		Templates:  templateStore,
		FileCache:  fileCache,
		FileSigner: fileSigner,
		PublicBase: fmt.Sprintf("http://localhost:%d", *port),
	})
	proxyHandler := api.NewProxy(mgr, rt)
	handler := api.NewRouter(controlHandler, proxyHandler)

	addr := fmt.Sprintf(":%d", *port)
	go mgr.Run(context.Background(), 0)
	log.Printf("edvabe listening on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}

func doctorCmd(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	port := fs.Int("port", 3000, "port `edvabe serve` will bind — checked for availability")
	image := fs.String("image", sandbox.DefaultImage, "base image tag to look for")
	_ = fs.Parse(args)

	err := doctor.Run(context.Background(), os.Stdout, doctor.Options{
		Port:      *port,
		BaseImage: *image,
	})
	if err != nil {
		os.Exit(1)
	}
}

func buildImageCmd(args []string) {
	fs := flag.NewFlagSet("build-image", flag.ExitOnError)
	tag := fs.String("tag", "edvabe/base:latest", "local tag to apply to the upstream base image")
	// --force is accepted for compatibility with docs/task description;
	// Docker's layer cache already makes re-runs fast so the flag is a
	// no-op. Users wanting a truly fresh build can `docker builder
	// prune` or `docker rmi edvabe/base:latest` first.
	_ = fs.Bool("force", false, "no-op; Docker's build cache handles re-runs")
	_ = fs.Parse(args)
	if err := upstream.EnsureBaseImage(context.Background(), *tag); err != nil {
		fmt.Fprintf(os.Stderr, "build-image: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("built %s (envd @ %s)\n", *tag, upstream.EnvdSourceSHA)
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

// templateStorePath returns ~/.local/share/edvabe/templates.json (or
// $EDVABE_STATE_DIR/templates.json when set). Falls back to a file in
// the current directory if the home dir cannot be resolved — better
// to have an ugly path than to crash out before the server starts.
func templateStorePath() string {
	if dir := os.Getenv("EDVABE_STATE_DIR"); dir != "" {
		return filepath.Join(dir, "templates.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "edvabe-templates.json"
	}
	return filepath.Join(home, ".local", "share", "edvabe", "templates.json")
}

// fileCacheDir returns the directory the content-addressed blob store
// lives in. $EDVABE_CACHE_DIR wins; otherwise ~/.cache/edvabe/template-files.
func fileCacheDir() string {
	if dir := os.Getenv("EDVABE_CACHE_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "edvabe-template-files"
	}
	return filepath.Join(home, ".cache", "edvabe", "template-files")
}
