package auth

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func TestIssueThenVerify_Succeeds(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	iss := NewIssuer([]string{"secret-1"}, time.Hour, clock)

	tok, err := iss.Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := iss.Verify(tok); err != nil {
		t.Fatalf("Verify(freshly issued token): %v", err)
	}
}

func TestVerify_RejectsATokenSignedWithADifferentAcceptedKeySet(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	a := NewIssuer([]string{"secret-1"}, time.Hour, clock)
	b := NewIssuer([]string{"secret-2"}, time.Hour, clock)

	tok, err := a.Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := b.Verify(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify across different signing keys = %v, want ErrInvalidToken", err)
	}
}

func TestNewIssuer_KeyDerivationIsOrderIndependent(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	a := NewIssuer([]string{"secret-1", "secret-2"}, time.Hour, clock)
	b := NewIssuer([]string{"secret-2", "secret-1"}, time.Hour, clock)

	tok, err := a.Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := b.Verify(tok); err != nil {
		t.Fatalf("Verify(token from a differently-ordered but equal accepted-key set): %v", err)
	}
}

func TestVerify_RejectsAnExpiredToken(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	iss := NewIssuer([]string{"secret-1"}, 50*time.Millisecond, clock)

	tok, err := iss.Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	clock.now = clock.now.Add(51 * time.Millisecond)
	if err := iss.Verify(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify(expired token) = %v, want ErrInvalidToken", err)
	}
}

func TestVerify_RejectsAMalformedToken(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	iss := NewIssuer([]string{"secret-1"}, time.Hour, clock)

	for _, tok := range []string{"", "not-a-jwt", "a.b", "a.b.c.d"} {
		if err := iss.Verify(tok); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("Verify(%q) = %v, want ErrInvalidToken", tok, err)
		}
	}
}

func TestVerify_RejectsATamperedPayload(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	iss := NewIssuer([]string{"secret-1"}, time.Hour, clock)

	tok, err := iss.Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("issued token has %d parts, want 3", len(parts))
	}
	tamperedPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"iat":0,"exp":9999999999}`))
	tampered := parts[0] + "." + tamperedPayload + "." + parts[2]
	if err := iss.Verify(tampered); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify(tampered payload) = %v, want ErrInvalidToken", err)
	}
}

func TestIssuer_NeverExposesTheSigningKey(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	iss := NewIssuer([]string{"secret-1"}, time.Hour, clock)

	tok, err := iss.Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if strings.Contains(tok, "secret-1") {
		t.Fatalf("issued token unexpectedly contains the accepted client key: %s", tok)
	}
}
