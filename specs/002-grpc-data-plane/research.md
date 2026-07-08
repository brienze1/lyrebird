# Research: Generic gRPC Data Plane

## Authoritative protobuf field numbers (verified from googleapis protos, not memory)

Source: `google/cloud/kms/v1/service.proto`, `google/pubsub/v1/pubsub.proto`
(raw.githubusercontent.com/googleapis/googleapis, fetched 2026-07-07).

### KMS `google.cloud.kms.v1.KeyManagementService/Decrypt`
```
message DecryptRequest {
  string name = 1;
  bytes ciphertext = 2;
  bytes additional_authenticated_data = 3;
  google.protobuf.Int64Value ciphertext_crc32c = 5;
  google.protobuf.Int64Value additional_authenticated_data_crc32c = 6;
}
message DecryptResponse {
  bytes plaintext = 1;
  google.protobuf.Int64Value plaintext_crc32c = 2;
  bool used_primary = 3;
  ProtectionLevel protection_level = 4;
}
```
**Echo mapping (declarative field-copy):**
- `DecryptResponse.plaintext` (1, bytes) ← `DecryptRequest.ciphertext` (2, bytes)
- `DecryptResponse.plaintext_crc32c` (2, wrapper) ← `DecryptRequest.ciphertext_crc32c` (5, wrapper)
  — valid because under echo `plaintext == ciphertext`, so `crc32c(plaintext) == crc32c(ciphertext)`;
  copying the wire bytes of the Int64Value wrapper across field numbers preserves the value.
  Emitted only if the client sent field 5 (a client that doesn't send it won't verify it).
- Optional constants for realism: `used_primary` (3) = true, `protection_level` (4) = 1 (SOFTWARE).

### Pub/Sub `google.pubsub.v1.Publisher`
```
message GetTopicRequest { string topic = 1; }
message Topic          { string name  = 1; ... }
message PublishRequest  { string topic = 1; repeated PubsubMessage messages = 2; }
message PublishResponse { repeated string message_ids = 1; }
```
**Mappings:**
- `GetTopic`: `Topic.name` (1, string) ← `GetTopicRequest.topic` (1, string) — echo.
- `CreateTopic` (fallback path some clients take): request IS a `Topic` (name=1); echo name→name.
- `Publish`: `PublishResponse.message_ids` (1, repeated string) = one synthetic id (e.g. `"1"`).
  Publish is not functionally exercised locally (only the boot topic-exists check must pass),
  so a fixed single id is sufficient; count-matching to `messages` is deferred.

## Consequence for machinery sizing (advisor point 3)

Every acceptance behavior reduces to **copy a request field's raw bytes to a (possibly
different) response field number**, plus **constant/literal field values**. No CRC computation,
no goja proto codec, no compiled schema. The generic response primitive therefore needs only:
1. Set a response field `M` to a literal value (varint / string / bytes / repeated).
2. Set a response field `M` to the raw bytes of request field `N` (echo/copy).

Matching needs: read request field `N` as bytes/string for the existing Matcher (equals/contains/
regex/exists), addressed by field number.

## Transport decision (from plan, confirmed with user)

`google.golang.org/grpc` with `grpc.UnknownServiceHandler` + a raw `[]byte` `ForceServerCodec`.
grpc-go serves plaintext HTTP/2 (h2c) on a raw TCP listener natively — exactly what
`insecure.NewCredentials()` clients dial — and owns framing + `grpc-status` trailers + edge cases.
Pure-Go, `CGO_ENABLED=0`-safe, keeps the static multi-arch image (Principle VI) though larger.

The unknown-service handler:
- `grpc.MethodFromServerStream(stream)` → `/pkg.Service/Method` (the match path).
- `stream.RecvMsg(&raw []byte)` reads the unary request bytes.
- Parse protobuf wire → field-number-keyed projection for matching/templating (key `fN`, keep
  raw bytes for length-delimited fields to preserve wire type on re-encode).
- Run the existing `usecase.MatchRequest` (default space).
- Matched respond → build response wire bytes from the declarative field spec, `SendMsg`, return nil.
- Unmatched / non-unary / non-respond → `status.Error(codes.Unimplemented, ...)`.
- Malformed request / internal error → clean gRPC error status (never panic/hang).

## Config decision

`LYREBIRD_GRPC_PORT` is **opt-in**: the gRPC listener binds only when the variable is set
(mirrors MITM's explicit-enable shape and guarantees FR-010 — unchanged behavior when unset,
zero new surface by default). Documented in the existing `LYREBIRD_*` style; exposed in the image.

## Open items routed to clarification

- Whether the KMS consumer (wallet-api) actually verifies `plaintext_crc32c` and reads
  `protection_level`/`used_primary`. Mitigated by building the recipe to satisfy all cases
  (echo plaintext + echo crc-when-present + constant protection fields), so no blocking decision.
