package crypto

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
)

func mustSealer(t *testing.T, key []byte) Sealer {
	t.Helper()
	s, err := New(key)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	return s
}

func TestSealOpenRoundTrip(t *testing.T) {
	key, err := NewRandomKey()
	if err != nil {
		t.Fatalf("NewRandomKey(): %v", err)
	}
	s := mustSealer(t, key)

	plaintext := []byte(`{"hello":"world"}`)
	ct, err := s.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal(): %v", err)
	}
	if bytes.Equal(ct, plaintext) {
		t.Fatal("Seal() returned plaintext unchanged")
	}

	pt, err := s.Open(ct)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Fatalf("Open() = %q, want %q", pt, plaintext)
	}
}

func TestSealProducesDistinctNoncesPerCall(t *testing.T) {
	key, _ := NewRandomKey()
	s := mustSealer(t, key)
	pt := []byte("same plaintext")

	ct1, err := s.Seal(pt)
	if err != nil {
		t.Fatalf("Seal() #1: %v", err)
	}
	ct2, err := s.Seal(pt)
	if err != nil {
		t.Fatalf("Seal() #2: %v", err)
	}
	if bytes.Equal(ct1, ct2) {
		t.Fatal("two Seal() calls on identical plaintext produced identical ciphertext (nonce reuse)")
	}
}

func TestOpenFailsUnderWrongKey(t *testing.T) {
	keyA, _ := NewRandomKey()
	keyB, _ := NewRandomKey()
	sealerA := mustSealer(t, keyA)
	sealerB := mustSealer(t, keyB)

	ct, err := sealerA.Seal([]byte("secret"))
	if err != nil {
		t.Fatalf("Seal(): %v", err)
	}

	_, err = sealerB.Open(ct)
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("Open() under wrong key = %v, want ErrAuthFailed", err)
	}
}

func TestOpenFailsOnCorruptOrTruncatedInput(t *testing.T) {
	key, _ := NewRandomKey()
	s := mustSealer(t, key)

	cases := map[string][]byte{
		"empty":     {},
		"too short": {0x01, 0x02, 0x03},
		"garbage":   bytes.Repeat([]byte{0xAB}, 40),
	}
	for name, ct := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Open(ct); !errors.Is(err, ErrAuthFailed) {
				t.Fatalf("Open(%s) = %v, want ErrAuthFailed", name, err)
			}
		})
	}
}

func TestNewRejectsWrongKeyLength(t *testing.T) {
	if _, err := New([]byte("too-short")); err == nil {
		t.Fatal("New() with a non-32-byte key, want error")
	}
}

func TestDecodeKeyRoundTrip(t *testing.T) {
	raw, _ := NewRandomKey()
	b64 := base64.StdEncoding.EncodeToString(raw)

	decoded, err := DecodeKey(b64)
	if err != nil {
		t.Fatalf("DecodeKey(): %v", err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Fatal("DecodeKey() did not round-trip the original key")
	}
}

func TestDecodeKeyRejectsMalformedBase64(t *testing.T) {
	if _, err := DecodeKey("not valid base64!!!"); err == nil {
		t.Fatal("DecodeKey() with malformed base64, want error")
	}
}

func TestDecodeKeyRejectsWrongLength(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("short"))
	if _, err := DecodeKey(short); err == nil {
		t.Fatal("DecodeKey() with a key that decodes to != 32 bytes, want error")
	}
}

func TestResolveKeyPrefersEnvWhenSet(t *testing.T) {
	raw, _ := NewRandomKey()
	b64 := base64.StdEncoding.EncodeToString(raw)

	key, source, err := ResolveKey(b64)
	if err != nil {
		t.Fatalf("ResolveKey(): %v", err)
	}
	if source != KeySourceEnv {
		t.Errorf("source = %q, want %q", source, KeySourceEnv)
	}
	if !bytes.Equal(key, raw) {
		t.Error("ResolveKey() did not return the env-supplied key")
	}
}

func TestResolveKeyGeneratesRandomWhenEnvEmpty(t *testing.T) {
	key, source, err := ResolveKey("")
	if err != nil {
		t.Fatalf("ResolveKey(): %v", err)
	}
	if source != KeySourceRandom {
		t.Errorf("source = %q, want %q", source, KeySourceRandom)
	}
	if len(key) != 32 {
		t.Errorf("key length = %d, want 32", len(key))
	}
}
