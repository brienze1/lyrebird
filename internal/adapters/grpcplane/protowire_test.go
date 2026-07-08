package grpcplane

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// buildMsg is a tiny test helper that encodes fields into a protobuf message.
func buildMsg(t *testing.T, build func(b []byte) []byte) []byte {
	t.Helper()
	return build(nil)
}

func TestDecodeAndProjectForMatch(t *testing.T) {
	// field 1 = varint 7; field 2 = string "hello"; field 3 = bytes {0xff,0x00}
	msg := buildMsg(t, func(b []byte) []byte {
		b = protowire.AppendTag(b, 1, protowire.VarintType)
		b = protowire.AppendVarint(b, 7)
		b = protowire.AppendTag(b, 2, protowire.BytesType)
		b = protowire.AppendString(b, "hello")
		b = protowire.AppendTag(b, 3, protowire.BytesType)
		b = protowire.AppendBytes(b, []byte{0xff, 0x00})
		return b
	})

	fields, err := decodeFields(msg)
	if err != nil {
		t.Fatalf("decodeFields: %v", err)
	}
	projected, err := projectForMatch(fields)
	if err != nil {
		t.Fatalf("projectForMatch: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(projected, &got); err != nil {
		t.Fatalf("projection is not valid JSON: %v (%s)", err, projected)
	}
	if got["f1"].(float64) != 7 {
		t.Errorf("f1 = %v, want 7", got["f1"])
	}
	// f2 is base64 of "hello", and f2_str is the decoded string.
	if got["f2"].(string) != base64.StdEncoding.EncodeToString([]byte("hello")) {
		t.Errorf("f2 = %v, want base64(hello)", got["f2"])
	}
	if got["f2_str"].(string) != "hello" {
		t.Errorf("f2_str = %v, want hello", got["f2_str"])
	}
	// f3 is binary; base64 present, no _str (not valid UTF-8... 0xff is invalid).
	if got["f3"].(string) != base64.StdEncoding.EncodeToString([]byte{0xff, 0x00}) {
		t.Errorf("f3 = %v, want base64(ff00)", got["f3"])
	}
	if _, ok := got["f3_str"]; ok {
		t.Errorf("f3_str should be absent for non-UTF8 bytes, got %v", got["f3_str"])
	}
}

func TestProjectRepeatedField(t *testing.T) {
	// field 1 repeated string "a","b"
	msg := buildMsg(t, func(b []byte) []byte {
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendString(b, "a")
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendString(b, "b")
		return b
	})
	fields, err := decodeFields(msg)
	if err != nil {
		t.Fatalf("decodeFields: %v", err)
	}
	projected, err := projectForMatch(fields)
	if err != nil {
		t.Fatalf("projectForMatch: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(projected, &got)
	arr, ok := got["f1_str"].([]any)
	if !ok || len(arr) != 2 || arr[0] != "a" || arr[1] != "b" {
		t.Errorf("f1_str = %v, want [a b]", got["f1_str"])
	}
}

func TestDecodeMalformed(t *testing.T) {
	// A tag claiming a length-delimited field but with a truncated length.
	bad := []byte{0x0a, 0x05} // field 1, BytesType, len 5, but no payload
	if _, err := decodeFields(bad); err == nil {
		t.Fatal("expected error decoding truncated message, got nil")
	}
}

func TestBuildResponseLiterals(t *testing.T) {
	spec := []byte(`{"f1":{"string":"ok"},"f2":{"int":5},"f3":{"bool":true}}`)
	out, err := buildResponse(spec, nil)
	if err != nil {
		t.Fatalf("buildResponse: %v", err)
	}
	fields, err := decodeFields(out)
	if err != nil {
		t.Fatalf("decode built response: %v", err)
	}
	if s := string(fields[1][0].bytes); s != "ok" {
		t.Errorf("f1 = %q, want ok", s)
	}
	if fields[2][0].varint != 5 {
		t.Errorf("f2 = %d, want 5", fields[2][0].varint)
	}
	if fields[3][0].varint != 1 {
		t.Errorf("f3 = %d, want 1", fields[3][0].varint)
	}
}

func TestBuildResponseCopyFromEcho(t *testing.T) {
	// KMS-shaped: request field 2 = ciphertext bytes; echo into response field 1.
	ciphertext := []byte("secret-bytes")
	reqMsg := buildMsg(t, func(b []byte) []byte {
		b = protowire.AppendTag(b, 2, protowire.BytesType)
		b = protowire.AppendBytes(b, ciphertext)
		return b
	})
	req, err := decodeFields(reqMsg)
	if err != nil {
		t.Fatalf("decodeFields: %v", err)
	}
	out, err := buildResponse([]byte(`{"f1":{"copyFrom":2}}`), req)
	if err != nil {
		t.Fatalf("buildResponse: %v", err)
	}
	resp, err := decodeFields(out)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !bytes.Equal(resp[1][0].bytes, ciphertext) {
		t.Errorf("echoed plaintext = %q, want %q", resp[1][0].bytes, ciphertext)
	}
}

func TestBuildResponseCopyFromAbsentIsOmitted(t *testing.T) {
	// copyFrom a field the request never sent → the field is omitted entirely.
	out, err := buildResponse([]byte(`{"f2":{"copyFrom":5}}`), map[protowire.Number][]rawField{})
	if err != nil {
		t.Fatalf("buildResponse: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty message when copyFrom source absent, got %d bytes", len(out))
	}
}

func TestBuildResponseRepeated(t *testing.T) {
	// Pub/Sub PublishResponse: repeated string message_ids field 1.
	out, err := buildResponse([]byte(`{"f1":[{"string":"1"},{"string":"2"}]}`), nil)
	if err != nil {
		t.Fatalf("buildResponse: %v", err)
	}
	fields, err := decodeFields(out)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(fields[1]) != 2 || string(fields[1][0].bytes) != "1" || string(fields[1][1].bytes) != "2" {
		t.Errorf("repeated f1 = %v, want two entries 1,2", fields[1])
	}
}

func TestBuildResponseEmptySpec(t *testing.T) {
	out, err := buildResponse([]byte(""), nil)
	if err != nil {
		t.Fatalf("buildResponse empty: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("empty spec should encode to empty message, got %d bytes", len(out))
	}
}

func TestBuildResponseBadKey(t *testing.T) {
	if _, err := buildResponse([]byte(`{"1":{"int":1}}`), nil); err == nil {
		t.Fatal("expected error for bare numeric key (must be fN), got nil")
	}
}
