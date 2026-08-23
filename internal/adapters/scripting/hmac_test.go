package scripting

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/usecase"
)

func TestEvalRespondHMACMatchesCryptoHMAC(t *testing.T) {
	e := New(100 * time.Millisecond)
	body, err := e.EvalRespond(`hmac("sha256", "s3cr3t", "the-payload")`, usecase.MatchInput{})
	if err != nil {
		t.Fatalf("EvalRespond(hmac): %v", err)
	}

	mac := hmac.New(sha256.New, []byte("s3cr3t"))
	_, _ = mac.Write([]byte("the-payload"))
	want := hex.EncodeToString(mac.Sum(nil))

	if string(body) != want {
		t.Errorf("hmac() = %s, want %s", body, want)
	}
}

func TestEvalRespondHMACSupportsEveryDocumentedAlgorithm(t *testing.T) {
	e := New(100 * time.Millisecond)
	for _, alg := range []string{"sha1", "sha256", "sha512"} {
		body, err := e.EvalRespond(`hmac("`+alg+`", "k", "d")`, usecase.MatchInput{})
		if err != nil {
			t.Fatalf("EvalRespond(hmac %s): %v", alg, err)
		}
		if len(body) == 0 {
			t.Errorf("hmac(%q) returned an empty digest", alg)
		}
	}
}

// An unknown algorithm has to throw rather than yield undefined: a script that
// silently produced "undefined" would ship it as a signature, and the failure
// would surface as an authentication error in whatever received it.
func TestEvalRespondHMACRejectsUnknownAlgorithm(t *testing.T) {
	e := New(100 * time.Millisecond)
	_, err := e.EvalRespond(`hmac("md5", "k", "d")`, usecase.MatchInput{})
	if err == nil {
		t.Fatal("EvalRespond(hmac md5) = nil error, want a script failure")
	}
	if !strings.Contains(err.Error(), "unsupported algorithm") {
		t.Errorf("error = %v, want it to name the unsupported algorithm", err)
	}
}

// The reason rawBody exists. A webhook signature is computed over the bytes the
// sender put on the wire; req.body has been through json.Unmarshal by then, and
// re-encoding it produces different bytes — here, reordered keys — so a digest
// taken over the round-tripped form does not match the sender's. rawBody does.
func TestEvalRespondRawBodyPreservesTheSendersBytes(t *testing.T) {
	e := New(100 * time.Millisecond)
	sent := `{"zeta":1,"alpha":2,  "nested":{"b":false}}`
	in := usecase.MatchInput{Body: []byte(sent)}

	raw, err := e.EvalRespond(`req.rawBody`, in)
	if err != nil {
		t.Fatalf("EvalRespond(req.rawBody): %v", err)
	}
	if string(raw) != sent {
		t.Errorf("req.rawBody = %q, want the bytes as sent %q", raw, sent)
	}

	roundTripped, err := e.EvalRespond(`JSON.stringify(req.body)`, in)
	if err != nil {
		t.Fatalf("EvalRespond(JSON.stringify(req.body)): %v", err)
	}
	if string(roundTripped) == sent {
		t.Fatal("re-encoding req.body reproduced the sender's bytes; " +
			"if that ever holds, this test no longer proves why rawBody is needed")
	}

	signedRaw, err := e.EvalRespond(`hmac("sha256", "k", req.rawBody)`, in)
	if err != nil {
		t.Fatalf("EvalRespond(hmac over rawBody): %v", err)
	}
	mac := hmac.New(sha256.New, []byte("k"))
	_, _ = mac.Write([]byte(sent))
	if string(signedRaw) != hex.EncodeToString(mac.Sum(nil)) {
		t.Errorf("signature over req.rawBody = %s, want the digest of the sent bytes", signedRaw)
	}
}

func TestEvalRespondRawBodyIsEmptyStringWithoutABody(t *testing.T) {
	e := New(100 * time.Millisecond)
	body, err := e.EvalRespond(`({t: typeof req.rawBody, len: req.rawBody.length})`, usecase.MatchInput{})
	if err != nil {
		t.Fatalf("EvalRespond(): %v", err)
	}
	if string(body) != `{"len":0,"t":"string"}` {
		t.Errorf("body = %s, want an empty string rather than null/undefined", body)
	}
}
