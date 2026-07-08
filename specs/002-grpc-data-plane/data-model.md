# Data Model: Generic gRPC Data Plane

No new domain types and no persistence-schema changes. A gRPC mock is a `domain.Mock`
(`internal/domain/mock.go`) — same fields, lifecycle (seeded/ephemeral), store, reset, and GC as
an HTTP mock. This document defines the two adapter-local representations the gRPC transport uses
to bridge protobuf ⇄ the existing match→respond model.

## 1. Request projection (protobuf wire → match input)

The unknown-service handler decodes the request message with `protowire` (schema-free) and
projects it into a JSON object stored in `usecase.MatchInput.Body`, so the existing gjson body
matchers (`internal/adapters/matcher/matcher.go`, `gjson.GetBytes(in.Body, path)`) apply unchanged.

Projection rules (one top-level object, keys are `f<fieldNumber>`):

| protobuf wire type | projected JSON value at key `fN` |
|--------------------|----------------------------------|
| Varint (0)         | number (int64) |
| I64 (1)            | number |
| I32 (5)            | number |
| Len-delimited (2)  | base64 string of the raw value bytes (covers string/bytes/embedded message; a matcher that wants the UTF-8 string can also match the decoded form — see contract) |
| repeated field     | JSON array of the above, in wire order |

- Keys are `fN` (e.g. `f2`) — never bare `"2"` — because gjson treats a bare numeric path segment
  as an array index, which would misresolve. `fN` is an unambiguous object key.
- `MatchInput.Method` = `"POST"` (gRPC is always HTTP/2 POST). `MatchInput.Path` = the
  fully-qualified method, e.g. `/google.cloud.kms.v1.KeyManagementService/Decrypt`.
- `MatchInput.Header` = gRPC metadata (lowercased keys) — available for matching, not required.
- The raw decoded field values (field number → list of `{wireType, bytes}`) are also kept in
  memory by the handler (not in `MatchInput`) so the response builder can echo/copy them.

## 2. Response field-spec (respond body → protobuf wire)

On the gRPC listener, a matched `respond` mock's body (`domain.RespondAction.Body`, authored in
the recipe as `action.respond.body`) is interpreted as a **response field-spec**: a JSON object
mapping `f<fieldNumber>` → a value descriptor. The builder encodes it to protobuf wire via
`protowire` and returns it as the unary response message. (Interpreting the body as a field-spec
is a property of the gRPC transport, not of the domain — an HTTP mock's body is untouched.)

Value descriptor (exactly one key):

| descriptor | meaning | wire type emitted |
|------------|---------|-------------------|
| `{"string": "..."}` | UTF-8 string | len-delimited (2) |
| `{"bytes": "<base64>"}` | raw bytes | len-delimited (2) |
| `{"int": <n>}` | signed/unsigned varint (bool true→1) | varint (0) |
| `{"bool": true/false}` | 1/0 | varint (0) |
| `{"copyFrom": <reqFieldNumber>}` | copy the raw value bytes of request field N, **preserving its wire type**; if request field N is absent, the field is **omitted** | source field's wire type |
| `{"raw": "<base64>"}` | pre-encoded field value bytes emitted len-delimited | len-delimited (2) |

- **Repeated fields**: if the value for `fN` is a JSON **array**, each element is a descriptor and
  is emitted as a separate occurrence of field N (e.g. Pub/Sub `message_ids`).
- **Nested messages / wrappers** (e.g. KMS `plaintext_crc32c` is an `Int64Value`): use `copyFrom`
  to echo the request's corresponding wrapper field bytes, or `raw` to supply pre-encoded bytes.
  No schema knowledge is needed — the wrapper's wire bytes are copied verbatim.
- An empty object `{}` encodes to a zero-length message (valid `google.protobuf.Empty`).

## 3. Worked examples (these ARE the recipes — no code)

### KMS `DecryptResponse` (echo)
```json
{
  "f1": { "copyFrom": 2 },
  "f2": { "copyFrom": 5 },
  "f3": { "bool": true },
  "f4": { "int": 1 }
}
```
- `f1` plaintext ← request ciphertext (field 2). `f2` plaintext_crc32c ← request ciphertext_crc32c
  (field 5), omitted if the client didn't send it. `f3` used_primary=true, `f4` protection_level=1
  (SOFTWARE). `f3`/`f4` are optional realism; `f1` alone satisfies the acceptance.

### Pub/Sub `Topic` (GetTopic / CreateTopic echo)
```json
{ "f1": { "copyFrom": 1 } }
```
- `Topic.name` (field 1) ← request `topic`/`name` (field 1).

### Pub/Sub `PublishResponse`
```json
{ "f1": [ { "string": "1" } ] }
```
- One synthetic `message_ids` entry. (Publish is not functionally exercised locally.)

## 4. Traffic record

Each gRPC call is recorded via the existing `usecase.RecordTraffic` with `Path` = method,
`Method` = `POST`, request/response bodies = the raw protobuf message bytes (subject to the
existing body cap + disposability + GC). Decision reuses existing decisions (mocked / not-matched).
