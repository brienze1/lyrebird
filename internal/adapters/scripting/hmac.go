package scripting

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
)

// hmacAlgorithms is the set the sandbox exposes, keyed by the name a script
// passes. Lowercase and unabbreviated, matching how every provider names the
// algorithm in the header it expects back (`sha256=<hex>`), so a script author
// copies the name out of the provider's docs rather than translating it.
//
// sha1 is here because webhook signatures are the reason this global exists and
// several providers still send one (Meta's X-Hub-Signature, GitHub's
// X-Hub-Signature). It is offered for compatibility with what those senders
// compute, never as a recommendation.
var hmacAlgorithms = map[string]func() hash.Hash{
	"sha1":   sha1.New,
	"sha256": sha256.New,
	"sha512": sha512.New,
}

// newHMAC builds the sandbox's hmac(algorithm, key, data) global, which returns
// the keyed digest as lowercase hex — the encoding every webhook signature
// header carries, so a script concatenates the prefix and is done:
//
//	({signature: "sha256=" + hmac("sha256", "s3cr3t", req.rawBody)})
//
// This is the one thing a script provably cannot do for itself. Pure JS can
// implement SHA-256, but only in a few hundred lines nobody wants to own inside
// a mock definition, and a hand-rolled digest that is subtly wrong fails as an
// authentication error rather than as a visible bug.
//
// It does not widen the sandbox: FR-015 forbids filesystem, network and
// environment access, and a keyed digest over two strings the script already
// holds reaches none of them. It computes; it cannot observe or reach anything.
//
// An unknown algorithm is a programming error in the script, so it throws and
// the mock fails safe (recorded decision `script_failed`) rather than returning
// undefined — a signature that silently reads "undefined" would surface far
// away, as an authentication failure in whatever received it.
func newHMAC() func(algorithm, key, data string) (string, error) {
	return func(algorithm, key, data string) (string, error) {
		newHash, ok := hmacAlgorithms[algorithm]
		if !ok {
			return "", fmt.Errorf("hmac: unsupported algorithm %q (want sha1, sha256 or sha512)", algorithm)
		}
		mac := hmac.New(newHash, []byte(key))
		// hash.Hash's Write never returns an error, by its own contract.
		_, _ = mac.Write([]byte(data))
		return hex.EncodeToString(mac.Sum(nil)), nil
	}
}
