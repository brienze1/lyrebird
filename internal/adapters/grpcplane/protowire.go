package grpcplane

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
)

// rawField is one decoded protobuf field occurrence, kept with its wire type
// so it can be re-emitted (echoed) under a possibly-different field number
// without losing type information.
type rawField struct {
	typ     protowire.Type
	varint  uint64
	fixed32 uint32
	fixed64 uint64
	bytes   []byte // for BytesType (string/bytes/embedded message) and groups
}

// decodeFields parses a protobuf message at the wire level into a
// field-number → occurrences map, with no schema. It never panics on
// malformed input: a parse error is returned. Repeated fields naturally
// accumulate multiple occurrences under the same number.
func decodeFields(b []byte) (map[protowire.Number][]rawField, error) {
	out := map[protowire.Number][]rawField{}
	for len(b) > 0 {
		num, typ, tagLen := protowire.ConsumeTag(b)
		if tagLen < 0 {
			return nil, fmt.Errorf("grpcplane: decode tag: %w", protowire.ParseError(tagLen))
		}
		b = b[tagLen:]

		var valLen int
		var f rawField
		f.typ = typ
		switch typ {
		case protowire.VarintType:
			f.varint, valLen = protowire.ConsumeVarint(b)
		case protowire.Fixed32Type:
			f.fixed32, valLen = protowire.ConsumeFixed32(b)
		case protowire.Fixed64Type:
			f.fixed64, valLen = protowire.ConsumeFixed64(b)
		case protowire.BytesType:
			var v []byte
			v, valLen = protowire.ConsumeBytes(b)
			f.bytes = append([]byte(nil), v...)
		case protowire.StartGroupType:
			var v []byte
			v, valLen = protowire.ConsumeGroup(num, b)
			f.bytes = append([]byte(nil), v...)
		default:
			return nil, fmt.Errorf("grpcplane: unsupported wire type %d for field %d", typ, num)
		}
		if valLen < 0 {
			return nil, fmt.Errorf("grpcplane: decode field %d value: %w", num, protowire.ParseError(valLen))
		}
		b = b[valLen:]
		out[num] = append(out[num], f)
	}
	return out, nil
}

// projectForMatch turns decoded fields into a JSON object addressable by the
// existing gjson body matchers. Keys are "fN" (never bare "N", which gjson
// would treat as an array index). Length-delimited fields are exposed as
// base64 under "fN", and additionally as the decoded UTF-8 string under
// "fN_str" when the bytes are valid UTF-8 (so a recipe can match a string
// field without base64-encoding it). Repeated fields become JSON arrays.
func projectForMatch(fields map[protowire.Number][]rawField) ([]byte, error) {
	obj := map[string]any{}
	for num, occ := range fields {
		key := "f" + strconv.Itoa(int(num))
		values := make([]any, 0, len(occ))
		var strValues []any
		for _, f := range occ {
			switch f.typ {
			case protowire.VarintType:
				values = append(values, f.varint)
			case protowire.Fixed32Type:
				values = append(values, f.fixed32)
			case protowire.Fixed64Type:
				values = append(values, f.fixed64)
			default: // BytesType / groups
				values = append(values, base64.StdEncoding.EncodeToString(f.bytes))
				if utf8.Valid(f.bytes) {
					if strValues == nil {
						strValues = make([]any, 0, len(occ))
					}
					strValues = append(strValues, string(f.bytes))
				}
			}
		}
		obj[key] = collapse(values)
		if strValues != nil {
			obj[key+"_str"] = collapse(strValues)
		}
	}
	return json.Marshal(obj)
}

// collapse returns the single element for a one-occurrence field, or the
// array for a repeated one — so a scalar field matches as a scalar and a
// repeated field matches as an array.
func collapse(vs []any) any {
	if len(vs) == 1 {
		return vs[0]
	}
	return vs
}

// fieldSpec is one response-field value descriptor (data-model.md §2).
// Exactly one field is non-nil.
type fieldSpec struct {
	String   *string `json:"string,omitempty"`
	Bytes    *string `json:"bytes,omitempty"` // base64
	Int      *int64  `json:"int,omitempty"`
	Bool     *bool   `json:"bool,omitempty"`
	CopyFrom *int32  `json:"copyFrom,omitempty"` // source request field number
	Raw      *string `json:"raw,omitempty"`      // base64 of a pre-encoded len-delimited value
}

// buildResponse encodes a response message from a declarative field-spec (the
// matched mock's respond body) and the decoded request fields (for copyFrom
// echoes). An empty/blank spec encodes to a zero-length message (a valid
// empty proto). Fields are emitted in ascending field-number order for
// determinism. A copyFrom of an absent request field is omitted, not an error.
func buildResponse(spec []byte, req map[protowire.Number][]rawField) ([]byte, error) {
	if len(trimSpace(spec)) == 0 {
		return []byte{}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(spec, &m); err != nil {
		return nil, fmt.Errorf("grpcplane: response field-spec is not a JSON object: %w", err)
	}

	// Sort keys by field number for a deterministic wire layout.
	type kv struct {
		num protowire.Number
		raw json.RawMessage
	}
	entries := make([]kv, 0, len(m))
	for k, v := range m {
		num, err := fieldNumberFromKey(k)
		if err != nil {
			return nil, err
		}
		entries = append(entries, kv{num: num, raw: v})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].num < entries[j].num })

	var out []byte
	for _, e := range entries {
		specs, err := parseDescriptors(e.raw)
		if err != nil {
			return nil, fmt.Errorf("grpcplane: field %d: %w", e.num, err)
		}
		for _, fs := range specs {
			out, err = appendField(out, e.num, fs, req)
			if err != nil {
				return nil, fmt.Errorf("grpcplane: field %d: %w", e.num, err)
			}
		}
	}
	return out, nil
}

// parseDescriptors accepts either one descriptor object or a JSON array of
// them (a repeated field).
func parseDescriptors(raw json.RawMessage) ([]fieldSpec, error) {
	for _, c := range raw {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '[':
			var arr []fieldSpec
			if err := json.Unmarshal(raw, &arr); err != nil {
				return nil, err
			}
			return arr, nil
		default:
			var one fieldSpec
			if err := json.Unmarshal(raw, &one); err != nil {
				return nil, err
			}
			return []fieldSpec{one}, nil
		}
	}
	return nil, fmt.Errorf("empty field value")
}

// appendField encodes a single field occurrence per the descriptor. copyFrom
// re-emits every occurrence of the source request field under num, preserving
// the source wire type; an absent source field appends nothing.
func appendField(out []byte, num protowire.Number, fs fieldSpec, req map[protowire.Number][]rawField) ([]byte, error) {
	switch {
	case fs.String != nil:
		out = protowire.AppendTag(out, num, protowire.BytesType)
		out = protowire.AppendString(out, *fs.String)
	case fs.Bytes != nil:
		b, err := base64.StdEncoding.DecodeString(*fs.Bytes)
		if err != nil {
			return nil, fmt.Errorf("bytes is not valid base64: %w", err)
		}
		out = protowire.AppendTag(out, num, protowire.BytesType)
		out = protowire.AppendBytes(out, b)
	case fs.Raw != nil:
		b, err := base64.StdEncoding.DecodeString(*fs.Raw)
		if err != nil {
			return nil, fmt.Errorf("raw is not valid base64: %w", err)
		}
		out = protowire.AppendTag(out, num, protowire.BytesType)
		out = protowire.AppendBytes(out, b)
	case fs.Int != nil:
		out = protowire.AppendTag(out, num, protowire.VarintType)
		out = protowire.AppendVarint(out, uint64(*fs.Int))
	case fs.Bool != nil:
		out = protowire.AppendTag(out, num, protowire.VarintType)
		v := uint64(0)
		if *fs.Bool {
			v = 1
		}
		out = protowire.AppendVarint(out, v)
	case fs.CopyFrom != nil:
		for _, f := range req[protowire.Number(*fs.CopyFrom)] {
			out = protowire.AppendTag(out, num, f.typ)
			switch f.typ {
			case protowire.VarintType:
				out = protowire.AppendVarint(out, f.varint)
			case protowire.Fixed32Type:
				out = protowire.AppendFixed32(out, f.fixed32)
			case protowire.Fixed64Type:
				out = protowire.AppendFixed64(out, f.fixed64)
			case protowire.BytesType:
				out = protowire.AppendBytes(out, f.bytes)
			default:
				return nil, fmt.Errorf("copyFrom source field %d has uncopyable wire type %d", *fs.CopyFrom, f.typ)
			}
		}
	default:
		return nil, fmt.Errorf("descriptor must set exactly one of string/bytes/int/bool/copyFrom/raw")
	}
	return out, nil
}

func fieldNumberFromKey(k string) (protowire.Number, error) {
	if len(k) < 2 || k[0] != 'f' {
		return 0, fmt.Errorf("field key %q must be \"fN\" (e.g. \"f1\")", k)
	}
	n, err := strconv.Atoi(k[1:])
	if err != nil || n < 1 {
		return 0, fmt.Errorf("field key %q must be \"fN\" with a positive field number", k)
	}
	return protowire.Number(n), nil
}

func trimSpace(b []byte) []byte {
	i, j := 0, len(b)
	for i < j {
		switch b[i] {
		case ' ', '\t', '\n', '\r':
			i++
			continue
		}
		break
	}
	for j > i {
		switch b[j-1] {
		case ' ', '\t', '\n', '\r':
			j--
			continue
		}
		break
	}
	return b[i:j]
}
