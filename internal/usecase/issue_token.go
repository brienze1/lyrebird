package usecase

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"

	"github.com/brienze1/lyrebird/internal/domain"
)

// tokenIssuer is the subset of *auth.Issuer's behavior IssueToken depends
// on — this package has zero compile-time dependency on internal/infra/auth.
type tokenIssuer interface {
	Issue() (string, error)
}

// IssueToken validates a client_key against the accepted keys, then
// delegates minting to the injected tokenIssuer (FR-031).
type IssueToken struct {
	acceptedKeys []string
	issuer       tokenIssuer
}

// NewIssueToken builds an IssueToken use case.
func NewIssueToken(acceptedKeys []string, issuer tokenIssuer) *IssueToken {
	return &IssueToken{acceptedKeys: acceptedKeys, issuer: issuer}
}

// Execute mints a token if clientKey matches one of the accepted keys,
// otherwise returns domain.ErrInvalidClientKey.
func (uc *IssueToken) Execute(_ context.Context, clientKey string) (string, error) {
	if !validClientKey(uc.acceptedKeys, clientKey) {
		return "", domain.ErrInvalidClientKey
	}
	return uc.issuer.Issue()
}

// validClientKey hashes both sides then compares via constant-time
// crypto/subtle against every accepted key unconditionally (no early return).
func validClientKey(accepted []string, candidate string) bool {
	candidateDigest := sha256.Sum256([]byte(candidate))
	matched := 0
	for _, key := range accepted {
		keyDigest := sha256.Sum256([]byte(key))
		matched |= subtle.ConstantTimeCompare(candidateDigest[:], keyDigest[:])
	}
	return matched == 1
}
