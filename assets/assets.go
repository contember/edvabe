// Package assets exposes //go:embed'd files that ship inside the edvabe
// binary. The `assets/` directory is a Go package so its files can be
// embedded from one central location and passed into whichever internal
// package needs them.
package assets

import _ "embed"

//go:embed Dockerfile.base
var DockerfileBase []byte
