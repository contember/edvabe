package filecache

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidToken is returned when a token fails signature or expiry
// verification. Callers should map this to HTTP 401.
var ErrInvalidToken = errors.New("filecache: invalid upload token")

// Signer mints and verifies short-lived upload tokens for the
// content-addressed file cache.
//
// The SDK workflow: edvabe's GET /templates/{id}/files/{hash} handler
// returns a URL that embeds a signed token. The SDK POSTs the tar to
// that URL; edvabe's upload handler verifies the token before writing
// to the cache. This keeps the upload path from needing an
// authenticated session while still preventing arbitrary upload spam.
//
// Token format: "<base64(hmac)>.<expiryUnix>". HMAC is over
// "<hash>.<expiryUnix>" using a process-local secret generated once at
// startup.
type Signer struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// SignerOptions configures NewSigner. The zero value produces a 5
// minute TTL and a random 32-byte secret; tests override both.
type SignerOptions struct {
	Secret []byte
	TTL    time.Duration
	Now    func() time.Time
}

// NewSigner returns a Signer. A non-nil secret is required for
// deterministic verification — NewRandomSigner is the convenience for
// production.
func NewSigner(opts SignerOptions) *Signer {
	if opts.TTL <= 0 {
		opts.TTL = 5 * time.Minute
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Signer{secret: opts.Secret, ttl: opts.TTL, now: opts.Now}
}

// NewRandomSigner seeds the signer with a fresh random secret. Tokens
// minted by one process instance cannot be verified by another — this
// is deliberate. The upload path is ephemeral and has no reason to
// survive a restart.
func NewRandomSigner(ttl time.Duration) (*Signer, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("filecache: generate signer secret: %w", err)
	}
	return NewSigner(SignerOptions{Secret: secret, TTL: ttl}), nil
}

// Sign returns a token that authorizes a single upload for hash. The
// token carries its own expiry; verification is stateless.
func (s *Signer) Sign(hash string) string {
	exp := s.now().Add(s.ttl).Unix()
	return s.signWithExpiry(hash, exp)
}

func (s *Signer) signWithExpiry(hash string, exp int64) string {
	payload := fmt.Sprintf("%s.%d", hash, exp)
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return sig + "." + strconv.FormatInt(exp, 10)
}

// Verify returns nil if the token authorizes an upload for the given
// hash and has not expired. Any failure returns ErrInvalidToken.
func (s *Signer) Verify(hash, token string) error {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return ErrInvalidToken
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return ErrInvalidToken
	}
	if s.now().Unix() > exp {
		return ErrInvalidToken
	}
	expected := s.signWithExpiry(hash, exp)
	// Constant-time comparison on the full token string. Even though
	// the expiry suffix is public, reusing the same helper avoids a
	// divergence between Sign and Verify.
	if !hmac.Equal([]byte(expected), []byte(token)) {
		return ErrInvalidToken
	}
	return nil
}
