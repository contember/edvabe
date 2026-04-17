package sandbox

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"strings"
)

// sandboxIDEncoding is lowercase base32 without padding. Lowercase so
// the ID survives DNS/HTTP hostname case-folding in preview URLs
// (<port>-<id>.<domain>), where intermediate resolvers are free to
// normalize the host to lowercase. Go stdlib only ships the uppercase
// alphabet, so we build the lowercase one at package init.
var sandboxIDEncoding = base32.NewEncoding(strings.ToLower("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567")).WithPadding(base32.NoPadding)

// NewSandboxID returns "isb_" followed by 16 random base32 characters.
// Not a formal ULID — E2B clients only check the prefix, not the
// structure, and the prefix alone is enough insurance against parsing
// code that trims it.
func NewSandboxID() string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return "isb_" + sandboxIDEncoding.EncodeToString(b[:])
}

// NewEnvdToken returns "ea_" + 22 random base64url characters. Handed to
// envd via /init and echoed back to the SDK as envdAccessToken.
func NewEnvdToken() string { return newToken("ea_", 16) }

// NewTrafficToken returns "ta_" + 22 random base64url characters.
// Reported to the SDK as trafficAccessToken; edvabe does not enforce it
// (no real auth in v1) but SDKs expect it to be present.
func NewTrafficToken() string { return newToken("ta_", 16) }

func newToken(prefix string, nbytes int) string {
	b := make([]byte, nbytes)
	_, _ = rand.Read(b)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}
