// Package crypto provides at-rest encryption for sensitive stored payloads
// (constitution Principle V). Encryption is on by default: a random key is
// generated at startup unless the operator supplies a stable one via
// LYREBIRD_DATA_KEY. Every AEAD failure — wrong key or corrupt blob — is
// normalized to a single sentinel so callers can treat it uniformly as
// "this row is absent", never as a fatal error (FR-029).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrAuthFailed is returned by Open when the ciphertext does not authenticate
// under the configured key. Callers MUST treat this as "row absent", never as
// a fatal error.
var ErrAuthFailed = errors.New("crypto: message authentication failed")

// Sealer seals and opens payloads with AES-256-GCM.
type Sealer interface {
	// Seal returns nonce||ciphertext||tag for plaintext, using a fresh
	// random nonce.
	Seal(plaintext []byte) ([]byte, error)
	// Open reverses Seal. Any failure — wrong key, truncated input, or a
	// corrupt blob — returns ErrAuthFailed and nothing else.
	Open(ciphertext []byte) ([]byte, error)
}

type aesGCMSealer struct {
	aead cipher.AEAD
}

// New builds a Sealer from a 32-byte AES-256 key.
func New(key []byte) (Sealer, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}
	return &aesGCMSealer{aead: gcm}, nil
}

func (s *aesGCMSealer) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (s *aesGCMSealer) Open(ciphertext []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(ciphertext) < n {
		return nil, ErrAuthFailed
	}
	nonce, ct := ciphertext[:n], ciphertext[n:]
	pt, err := s.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrAuthFailed
	}
	return pt, nil
}

// NewRandomKey returns 32 crypto/rand bytes — the default startup key.
func NewRandomKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("crypto: random key: %w", err)
	}
	return key, nil
}

// DecodeKey base64-decodes a LYREBIRD_DATA_KEY value into a 32-byte key.
// Error messages never include the raw input.
func DecodeKey(b64 string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, errors.New("crypto: LYREBIRD_DATA_KEY is not valid base64")
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: LYREBIRD_DATA_KEY must decode to 32 bytes, got %d", len(key))
	}
	return key, nil
}

// KeySource records how the active key was obtained, safe to log (unlike the
// key itself).
type KeySource string

// The KeySource values reported by ResolveKey.
const (
	KeySourceRandom KeySource = "startup-random"
	KeySourceEnv    KeySource = "env"
)

// ResolveKey returns the env-supplied key if envB64 is non-empty, otherwise a
// fresh random key.
func ResolveKey(envB64 string) (key []byte, source KeySource, err error) {
	if envB64 != "" {
		k, err := DecodeKey(envB64)
		if err != nil {
			return nil, "", err
		}
		return k, KeySourceEnv, nil
	}
	k, err := NewRandomKey()
	if err != nil {
		return nil, "", err
	}
	return k, KeySourceRandom, nil
}
