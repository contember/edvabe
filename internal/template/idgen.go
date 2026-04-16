package template

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

// templateIDEncoding matches the style of internal/sandbox/idgen.go so
// that IDs across the project feel consistent: lowercase base32 without
// padding, URL-safe, case-insensitive.
var templateIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTemplateID returns "tpl_" followed by 16 random base32 characters.
// The E2B SDK treats template IDs as opaque strings, so the prefix is a
// local convention to disambiguate from sandbox IDs at a glance.
func NewTemplateID() string { return "tpl_" + randString(10) }

// NewBuildID returns "bld_" followed by 16 random base32 characters.
func NewBuildID() string { return "bld_" + randString(10) }

func randString(nbytes int) string {
	b := make([]byte, nbytes)
	_, _ = rand.Read(b)
	return strings.ToLower(templateIDEncoding.EncodeToString(b))
}
