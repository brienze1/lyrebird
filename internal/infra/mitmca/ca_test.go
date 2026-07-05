package mitmca

import (
	stdcrypto "crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/infra/crypto"
)

func mustSealer(t *testing.T) Sealer {
	t.Helper()
	key, err := crypto.NewRandomKey()
	if err != nil {
		t.Fatalf("crypto.NewRandomKey: %v", err)
	}
	sealer, err := crypto.New(key)
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	return sealer
}

func TestGenerate_ProducesASelfSignedCA(t *testing.T) {
	ca, err := Generate(mustSealer(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	block, _ := pem.Decode(ca.CACertPEM())
	if block == nil {
		t.Fatalf("CACertPEM() is not valid PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	if !cert.IsCA {
		t.Fatalf("generated certificate is not a CA")
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
		t.Fatalf("CA certificate does not verify against itself: %v", err)
	}
}

func TestGenerate_ProducesADifferentCAEachCall(t *testing.T) {
	sealer := mustSealer(t)
	a, err := Generate(sealer)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b, err := Generate(sealer)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if string(a.CACertPEM()) == string(b.CACertPEM()) {
		t.Fatalf("two calls to Generate produced the identical CA certificate")
	}
}

func TestLeafCertFor_MintsACertSignedByTheCA(t *testing.T) {
	ca, err := Generate(mustSealer(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	leaf, err := ca.LeafCertFor("example.internal")
	if err != nil {
		t.Fatalf("LeafCertFor: %v", err)
	}
	leafCert, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatalf("parse minted leaf cert: %v", err)
	}

	caBlock, _ := pem.Decode(ca.CACertPEM())
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := leafCert.Verify(x509.VerifyOptions{
		DNSName: "example.internal", Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Fatalf("minted leaf does not verify against the CA: %v", err)
	}
}

func TestLeafCertFor_CachesBySNICaseInsensitively(t *testing.T) {
	ca, err := Generate(mustSealer(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	first, err := ca.LeafCertFor("Example.Internal")
	if err != nil {
		t.Fatalf("LeafCertFor: %v", err)
	}
	second, err := ca.LeafCertFor("example.internal")
	if err != nil {
		t.Fatalf("LeafCertFor: %v", err)
	}
	if string(first.Certificate[0]) != string(second.Certificate[0]) {
		t.Fatalf("LeafCertFor minted a different certificate for the same SNI (case-insensitively)")
	}

	other, err := ca.LeafCertFor("other.internal")
	if err != nil {
		t.Fatalf("LeafCertFor: %v", err)
	}
	if string(first.Certificate[0]) == string(other.Certificate[0]) {
		t.Fatalf("LeafCertFor minted the same certificate for two different SNIs")
	}
}

func TestLeafCertFor_EmptySNIStillMintsAUsableCert(t *testing.T) {
	ca, err := Generate(mustSealer(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := ca.LeafCertFor(""); err != nil {
		t.Fatalf("LeafCertFor(\"\"): %v", err)
	}
}

func TestLoadFromFiles_RoundTripsAndMintsLeaves(t *testing.T) {
	dir := t.TempDir()
	certPEM, keyPEM := generateFixtureCA(t)
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("write fixture cert: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write fixture key: %v", err)
	}

	ca, err := LoadFromFiles(certFile, keyFile, mustSealer(t))
	if err != nil {
		t.Fatalf("LoadFromFiles: %v", err)
	}
	if strings.TrimSpace(string(ca.CACertPEM())) != strings.TrimSpace(string(certPEM)) {
		t.Fatalf("LoadFromFiles's CACertPEM() does not match the file it was loaded from")
	}
	if _, err := ca.LeafCertFor("loaded.internal"); err != nil {
		t.Fatalf("LeafCertFor after LoadFromFiles: %v", err)
	}
}

func TestLoadFromFiles_TwoLoadsOfTheSameFilesProduceTheSameCert(t *testing.T) {
	dir := t.TempDir()
	certPEM, keyPEM := generateFixtureCA(t)
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("write fixture cert: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write fixture key: %v", err)
	}

	a, err := LoadFromFiles(certFile, keyFile, mustSealer(t))
	if err != nil {
		t.Fatalf("LoadFromFiles: %v", err)
	}
	b, err := LoadFromFiles(certFile, keyFile, mustSealer(t))
	if err != nil {
		t.Fatalf("LoadFromFiles: %v", err)
	}
	if string(a.CACertPEM()) != string(b.CACertPEM()) {
		t.Fatalf("loading the same CA files twice produced different certificates")
	}
}

func TestLoadFromFiles_MissingFileFailsFast(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadFromFiles(filepath.Join(dir, "missing-cert.pem"), filepath.Join(dir, "missing-key.pem"), mustSealer(t)); err == nil {
		t.Fatalf("expected an error for missing CA files, got nil")
	}
}

// TestCA_NoExportedMethodExposesThePrivateKey is the structural guarantee
// T067 requires: whatever CA's public API looks like, no exported method may
// ever return the CA's own private key material (a crypto.Signer or
// *ecdsa.PrivateKey). CACertPEM (public cert bytes) and LeafCertFor (a
// freshly-minted LEAF's own, non-CA key, which tls.Config.GetCertificate
// genuinely needs) are the only methods that return key-shaped data, and are
// explicitly allowed here.
func TestCA_NoExportedMethodExposesThePrivateKey(t *testing.T) {
	signerType := reflect.TypeOf((*stdcrypto.Signer)(nil)).Elem()
	ecdsaKeyType := reflect.TypeOf(&ecdsa.PrivateKey{})

	typ := reflect.TypeOf(&CA{})
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		if m.Name == "LeafCertFor" {
			continue
		}
		for j := 0; j < m.Type.NumOut(); j++ {
			out := m.Type.Out(j)
			if out == signerType || out == ecdsaKeyType {
				t.Fatalf("exported method %s returns %s — CA private key must never be exposed", m.Name, out)
			}
		}
	}
}

// generateFixtureCA is test-only CA cert/key PEM generation, independent of
// Generate/LoadFromFiles under test, so these tests don't just validate the
// package against itself.
func generateFixtureCA(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate fixture key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fixture-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create fixture cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal fixture key: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}
