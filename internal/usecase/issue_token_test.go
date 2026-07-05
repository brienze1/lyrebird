package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

type fakeTokenIssuer struct {
	token string
	err   error
}

func (f *fakeTokenIssuer) Issue() (string, error) { return f.token, f.err }

func TestIssueToken_Execute_ValidClientKeyDelegatesToIssuer(t *testing.T) {
	uc := NewIssueToken([]string{"secret-1", "secret-2"}, &fakeTokenIssuer{token: "minted-token"})

	tok, err := uc.Execute(context.Background(), "secret-2")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if tok != "minted-token" {
		t.Fatalf("token = %q, want %q", tok, "minted-token")
	}
}

func TestIssueToken_Execute_InvalidClientKeyIsRejectedWithoutCallingTheIssuer(t *testing.T) {
	issuer := &fakeTokenIssuer{token: "should-never-be-returned"}
	uc := NewIssueToken([]string{"secret-1"}, issuer)

	_, err := uc.Execute(context.Background(), "wrong-key")
	if !errors.Is(err, domain.ErrInvalidClientKey) {
		t.Fatalf("Execute(wrong key) error = %v, want domain.ErrInvalidClientKey", err)
	}
}

func TestIssueToken_Execute_EmptyClientKeyIsRejected(t *testing.T) {
	uc := NewIssueToken([]string{"secret-1"}, &fakeTokenIssuer{token: "x"})

	if _, err := uc.Execute(context.Background(), ""); !errors.Is(err, domain.ErrInvalidClientKey) {
		t.Fatalf("Execute(\"\") error = %v, want domain.ErrInvalidClientKey", err)
	}
}

func TestValidClientKey_ChecksEveryAcceptedKey(t *testing.T) {
	accepted := []string{"a", "b", "c"}
	if !validClientKey(accepted, "c") {
		t.Fatalf("expected the last accepted key to match")
	}
	if validClientKey(accepted, "d") {
		t.Fatalf("expected an unaccepted key to be rejected")
	}
}
