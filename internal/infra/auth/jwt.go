package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Clock abstracts time.Now so tests control token issuance/expiry instants.
type Clock interface{ Now() time.Time }

// ErrInvalidToken is returned by Verify for any failure (malformed,
// mis-signed, or expired) — deliberately not more specific (FR-033).
var ErrInvalidToken = errors.New("auth: invalid or expired token")

// jwtHeader is the one and only header this package issues or accepts;
// Verify never inspects a token's own header (rules out "alg" confusion).
const jwtHeader = `{"alg":"HS256","typ":"JWT"}`

// signingKeySalt is a fixed, public HKDF-Extract (RFC 5869) salt used to
// derive the signing key — see NewIssuer.
const signingKeySalt = "lyrebird-control-plane-auth-signing-key-v1"

// Issuer issues and verifies HS256 control-plane bearer tokens.
type Issuer struct {
	signingKey []byte
	ttl        time.Duration
	clock      Clock
}

// NewIssuer derives a 32-byte signing key from acceptedKeys via HKDF-Extract
// (HMAC-SHA256 keyed by signingKeySalt) over the sorted, NUL-joined keys.
func NewIssuer(acceptedKeys []string, ttl time.Duration, clock Clock) *Issuer {
	sorted := append([]string(nil), acceptedKeys...)
	sort.Strings(sorted)
	mac := hmac.New(sha256.New, []byte(signingKeySalt))
	mac.Write([]byte(strings.Join(sorted, "\x00")))
	return &Issuer{signingKey: mac.Sum(nil), ttl: ttl, clock: clock}
}

// claims is this package's entire JWT payload (iat/exp only — no per-client
// permissions exist in this control plane to authorize further against).
type claims struct {
	Iat int64 `json:"iat"`
	Exp int64 `json:"exp"`
}

// Issue mints a token valid for ttl from clock.Now().
func (iss *Issuer) Issue() (string, error) {
	now := iss.clock.Now()
	payload, err := json.Marshal(claims{Iat: now.Unix(), Exp: now.Add(iss.ttl).Unix()})
	if err != nil {
		return "", fmt.Errorf("auth: marshal claims: %w", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString([]byte(jwtHeader)) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(iss.sign(signingInput)), nil
}

// Verify checks tok's signature and expiry against clock.Now().
func (iss *Issuer) Verify(tok string) error {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return ErrInvalidToken
	}
	signingInput := parts[0] + "." + parts[1]
	gotSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return ErrInvalidToken
	}
	if subtle.ConstantTimeCompare(iss.sign(signingInput), gotSig) != 1 {
		return ErrInvalidToken
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ErrInvalidToken
	}
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return ErrInvalidToken
	}
	if iss.clock.Now().Unix() >= c.Exp {
		return ErrInvalidToken
	}
	return nil
}

func (iss *Issuer) sign(signingInput string) []byte {
	mac := hmac.New(sha256.New, iss.signingKey)
	mac.Write([]byte(signingInput))
	return mac.Sum(nil)
}
