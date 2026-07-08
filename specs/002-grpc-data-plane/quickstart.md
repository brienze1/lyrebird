# Quickstart / Validation: Generic gRPC Data Plane

Proves the feature end-to-end. Detailed field mappings are in `data-model.md`; the transport
contract is in `contracts/grpc-data-plane.md`.

## Prerequisites

- Go 1.26 toolchain; `google.golang.org/grpc` added to the module.
- Build: `go build ./...`. Full suite: `go test ./...`.

## 1. Automated (BDD) — the primary gate

`test/features/grpc_data_plane.feature` (authored failing first), driven by
`test/support/steps_grpc.go` registered in `test/support/main_test.go`.

Scenarios:
1. **Generic unary echo** — seed a mock for `/demo.Echo/Say` responding `{"f1":{"copyFrom":1}}`;
   a raw gRPC client calls it with a message whose field 1 = "hello"; response field 1 == "hello",
   status OK. (Proves SC-001: new service by config alone.)
2. **No match → Unimplemented** — call an unseeded method; expect gRPC `Unimplemented`.
3. **KMS Decrypt echo** — seed the `gcp-kms-grpc` recipe; use the real
   `cloud.google.com/go/kms/apiv1` client with `option.WithEndpoint(addr)`,
   `option.WithoutAuthentication()`, `grpc.WithTransportCredentials(insecure.NewCredentials())`;
   call `Decrypt` with ciphertext = base64 stub; assert `resp.Plaintext == base64decode(input)`.
   (Proves SC-002.)
4. **Pub/Sub GetTopic + Publish** — seed the `gcp-pubsub-grpc` recipe; point a Pub/Sub client at
   Lyrebird (emulator host); `GetTopic(T)` returns a topic with `name == T` (not NotFound);
   `Publish` returns OK with ≥1 message id. (Proves SC-003.)
5. **Reset semantics** — an ephemeral gRPC mock is cleared by `POST /__lyrebird/reset`; a seeded
   gRPC recipe survives. (Proves FR-009.)
6. **Recipe library** — `list_examples` includes `gcp-kms-grpc` and `gcp-pubsub-grpc`; each full
   recipe's mock payload is accepted by `POST /__lyrebird/mocks`. (examples.feature 9 → 11.)

Run: `go test ./test/... ` (or `go test ./...`).

Expected: all new scenarios green; **all existing scenarios still green** (SC-004 — HTTP data
plane, control plane, spaces, seed, reset unchanged, whether or not the gRPC listener is enabled).

## 2. Manual smoke (optional)

```sh
# Seed the gRPC recipes into ./config as *.yaml, then:
LYREBIRD_GRPC_PORT=50051 go run ./cmd/lyrebird
# KMS: point a KMS client's endpoint at localhost:50051 (insecure, no auth) and Decrypt a stub.
# Pub/Sub: PUBSUB_EMULATOR_HOST=localhost:50051 <consumer> — confirm it boots past topic-exists.
```

## 3. Build / image gates

- `go vet ./...` and `golangci-lint run` clean.
- `docker build .` succeeds; the image `EXPOSE`s the gRPC port; multi-arch unchanged.

## Acceptance → SC mapping

| Scenario | Success Criterion |
|----------|-------------------|
| Generic echo, config-only | SC-001 |
| KMS Decrypt echo (real client) | SC-002 |
| Pub/Sub GetTopic + Publish (real client) | SC-003 |
| Existing suite still green w/ and w/o gRPC on | SC-004 |
| Drops fake-kms + pubsub-emulator (follow-up repoint) | SC-005 |
