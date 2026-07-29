package dto

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/brienze1/lyrebird/internal/usecase"
)

func TestRecordedMessageToDTODecodesJSONBody(t *testing.T) {
	body := []byte(`{"offer":{"rate":0.011},"pix_key_type":"document"}`)

	out := RecordedMessageToDTO(usecase.RecordedMessage{Body: body})

	if len(out.JSON) == 0 {
		t.Fatalf("JSON = empty, want the decoded body")
	}
	var parsed struct {
		Offer struct {
			Rate json.Number `json:"rate"`
		} `json:"offer"`
	}
	if err := json.Unmarshal(out.JSON, &parsed); err != nil {
		t.Fatalf("JSON is not valid JSON: %v", err)
	}
	// The whole point of carrying the raw bytes: a rate stays exactly as the
	// sender wrote it, so a consumer asserting on "0.011" matches.
	if got := parsed.Offer.Rate.String(); got != "0.011" {
		t.Errorf("offer.rate = %q, want %q", got, "0.011")
	}
}

func TestRecordedMessageToDTOOmitsJSONForNonJSONBodies(t *testing.T) {
	for name, body := range map[string][]byte{
		"plain text":    []byte("req-body"),
		"empty":         nil,
		"zero length":   {},
		"truncated":     []byte(`{"offer":{"rate":0.0`),
		"binary":        {0x89, 0x50, 0x4e, 0x47},
		"trailing junk": []byte(`{"a":1} <html>`),
	} {
		t.Run(name, func(t *testing.T) {
			out := RecordedMessageToDTO(usecase.RecordedMessage{Body: body})
			if out.JSON != nil {
				t.Errorf("JSON = %s, want nil for a %s body", out.JSON, name)
			}
		})
	}
}

func TestRecordedMessageToDTOKeepsBodyAndMetadataUntouched(t *testing.T) {
	msg := usecase.RecordedMessage{
		Headers:       map[string][]string{"Content-Type": {"application/json"}},
		Body:          []byte(`{"a":1}`),
		BodyTruncated: true,
		BodyTotalSize: 4096,
	}

	out := RecordedMessageToDTO(msg)

	if string(out.Body) != `{"a":1}` {
		t.Errorf("Body = %s, want it passed through verbatim", out.Body)
	}
	if !out.BodyTruncated || out.BodyTotalSize != 4096 {
		t.Errorf("BodyTruncated/BodyTotalSize = %v/%d, want true/4096", out.BodyTruncated, out.BodyTotalSize)
	}
	if got := out.Headers["Content-Type"]; len(got) != 1 || got[0] != "application/json" {
		t.Errorf("Headers = %v, want the original headers", out.Headers)
	}
}

func TestRecordedMessageDTOWireShape(t *testing.T) {
	t.Run("json key present and parsed for a JSON body", func(t *testing.T) {
		raw, err := json.Marshal(RecordedMessageToDTO(usecase.RecordedMessage{Body: []byte(`{"rate":0.022}`)}))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(raw), `"json":{"rate":0.022}`) {
			t.Errorf("wire = %s, want an inline parsed json object", raw)
		}
		// body stays base64 so existing consumers are unaffected.
		if !strings.Contains(string(raw), `"body":"eyJyYXRlIjowLjAyMn0="`) {
			t.Errorf("wire = %s, want body still base64-encoded", raw)
		}
	})

	t.Run("json key absent for a non-JSON body", func(t *testing.T) {
		raw, err := json.Marshal(RecordedMessageToDTO(usecase.RecordedMessage{Body: []byte("nope")}))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(raw), `"json"`) {
			t.Errorf("wire = %s, want no json key at all", raw)
		}
	})
}
