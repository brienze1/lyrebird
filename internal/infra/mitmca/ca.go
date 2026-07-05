// Package mitmca implements Lyrebird's MITM Certificate Authority (T067):
// a self-signed CA that signs on-the-fly leaf certificates for the
// transparent forward-proxy/MITM data-plane path (T054), per data-model.md's
// "MITM Certificate Authority" section. The CA's private key is sealed at
// rest (constitution Principle V) and never exposed outside this package —
// see signer's doc comment.
package mitmca

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// Sealer is the subset of crypto.Sealer's behavior CA depends on, named at
// the point of use per this codebase's convention (see handler.go's
// upstreamLister/trafficRecorder).
type Sealer interface {
	Seal(plaintext []byte) ([]byte, error)
	Open(ciphertext []byte) ([]byte, error)
}

// CA is Lyrebird's MITM certificate authority: an X.509 CA certificate plus
// its private key, sealed at rest. The plaintext key is never held as a
// struct field — only sealedKey (ciphertext) is — and is reconstructed
// transiently, only inside signer(), only for as long as one leaf-minting
// call needs it.
type CA struct {
	cert      *x509.Certificate
	certPEM   []byte
	sealedKey []byte
	sealer    Sealer

	// leafCache is unbounded and never evicted, mirroring matcher.go's
	// regexCache convention — bounded in practice by the number of distinct
	// hosts a client tunnels through in one process lifetime.
	leafCache sync.Map // lowercase SNI -> *tls.Certificate
}

// Generate creates a brand-new CA — a fresh keypair and self-signed
// certificate minted on every call — matching the default,
// regenerate-every-boot lifecycle (constitution Principle III: disposable by
// default; data-model.md's MITM CA section). The resulting private key is
// sealed immediately via sealer; Generate never returns or logs it.
func Generate(sealer Sealer) (*CA, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("mitmca: generate CA key: %w", err)
	}
	tmpl, err := caTemplate()
	if err != nil {
		return nil, err
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("mitmca: create CA certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("mitmca: parse generated CA certificate: %w", err)
	}
	return sealCA(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), priv, sealer)
}

// LoadFromFiles loads a stable CA from mounted PEM cert/key file paths — the
// operator-provided alternative to Generate (LYREBIRD_MITM_CA_CERT_FILE /
// LYREBIRD_MITM_CA_KEY_FILE), so the CA — and every leaf cert it mints —
// stays identical across restarts. The key is sealed immediately after
// reading, exactly as in Generate; the file's plaintext bytes are never
// retained beyond this call.
func LoadFromFiles(certFile, keyFile string, sealer Sealer) (*CA, error) {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("mitmca: read CA cert file %q: %w", certFile, err)
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("mitmca: read CA key file %q: %w", keyFile, err)
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("mitmca: %q does not contain a valid PEM certificate", certFile)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("mitmca: parse CA certificate from %q: %w", certFile, err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("mitmca: %q does not contain a valid PEM private key", keyFile)
	}
	key, err := parsePrivateKeyBlock(keyBlock)
	if err != nil {
		return nil, fmt.Errorf("mitmca: parse CA private key from %q: %w", keyFile, err)
	}

	return sealCA(cert, certPEM, key, sealer)
}

// sealCA is Generate and LoadFromFiles's shared tail: seal key immediately,
// hold only ciphertext. The one place besides signer() that ever touches
// the plaintext key.
func sealCA(cert *x509.Certificate, certPEM []byte, key crypto.Signer, sealer Sealer) (*CA, error) {
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("mitmca: marshal CA key: %w", err)
	}
	sealedKey, err := sealer.Seal(keyDER)
	if err != nil {
		return nil, fmt.Errorf("mitmca: seal CA key: %w", err)
	}
	return &CA{cert: cert, certPEM: certPEM, sealedKey: sealedKey, sealer: sealer}, nil
}

func parsePrivateKeyBlock(block *pem.Block) (crypto.Signer, error) {
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		signer, ok := key.(crypto.Signer)
		if !ok {
			return nil, fmt.Errorf("PKCS8 key is not a crypto.Signer (%T)", key)
		}
		return signer, nil
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("unrecognized private key encoding (tried PKCS8, EC, PKCS1)")
}

// CACertPEM returns the CA's own certificate, PEM-encoded — served verbatim
// by both the REST ca-cert endpoint and the get_mitm_ca_cert MCP tool so an
// agent's HTTP client can add it as a trusted root. Contains no key
// material.
func (c *CA) CACertPEM() []byte { return c.certPEM }

// signer reconstructs the CA's private key transiently, for use only inside
// LeafCertFor below — never returned to any caller outside this package and
// never held as a struct field. This is the only place sealedKey is ever
// opened; every other method on CA either returns public cert material or a
// freshly-minted leaf's own (non-CA) key.
func (c *CA) signer() (crypto.Signer, error) {
	keyDER, err := c.sealer.Open(c.sealedKey)
	if err != nil {
		return nil, fmt.Errorf("mitmca: open sealed CA key: %w", err)
	}
	key, err := x509.ParsePKCS8PrivateKey(keyDER)
	if err != nil {
		return nil, fmt.Errorf("mitmca: parse opened CA key: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("mitmca: opened CA key is not a crypto.Signer (%T)", key)
	}
	return signer, nil
}

// LeafCertFor mints (or returns a cached) leaf certificate for sni, signed
// by this CA — the certificate Lyrebird's CONNECT handler presents during
// TLS termination via tls.Config.GetCertificate. An empty sni (a client that
// sent no SNI, e.g. connecting to an IP literal) falls back to a fixed cache
// key so such connections still get a served, cached leaf rather than a
// fresh mint per request.
func (c *CA) LeafCertFor(sni string) (*tls.Certificate, error) {
	key := strings.ToLower(sni)
	if key == "" {
		key = "lyrebird-mitm-default"
	}
	if cached, ok := c.leafCache.Load(key); ok {
		return cached.(*tls.Certificate), nil
	}

	leaf, err := c.mintLeaf(key)
	if err != nil {
		return nil, err
	}
	actual, _ := c.leafCache.LoadOrStore(key, leaf)
	return actual.(*tls.Certificate), nil
}

func (c *CA) mintLeaf(sni string) (*tls.Certificate, error) {
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("mitmca: generate leaf key for %q: %w", sni, err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, fmt.Errorf("mitmca: generate leaf serial for %q: %w", sni, err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: sni},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(30 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(sni); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{sni}
	}

	caSigner, err := c.signer()
	if err != nil {
		return nil, err
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &leafKey.PublicKey, caSigner)
	if err != nil {
		return nil, fmt.Errorf("mitmca: sign leaf certificate for %q: %w", sni, err)
	}
	return &tls.Certificate{Certificate: [][]byte{der, c.cert.Raw}, PrivateKey: leafKey}, nil
}

func caTemplate() (*x509.Certificate, error) {
	serial, err := randomSerial()
	if err != nil {
		return nil, fmt.Errorf("mitmca: generate CA serial: %w", err)
	}
	return &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Lyrebird MITM CA", Organization: []string{"Lyrebird"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}, nil
}

func randomSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
}
