# Tasks: Generic gRPC Data Plane

**Feature**: `002-grpc-data-plane` | **Spec**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md)

**Tests are REQUIRED** (Constitution Principle IV: BDD-first, red → green). Feature/scenario
tasks are authored failing before their implementation tasks.

**Conventions**: `[P]` = parallelizable (different files, no incomplete deps). `[USn]` maps to a
spec user story. Paths are repo-relative.

---

## Phase 1: Setup

- [ ] T001 Add `google.golang.org/grpc` to the module: `go get google.golang.org/grpc@latest`, then `go mod tidy`; confirm `go build ./...` still compiles and `CGO_ENABLED=0 go build ./cmd/lyrebird` produces a static binary. Update `go.mod`/`go.sum`.
- [ ] T002 [P] Add `GRPCPlaneAddr string` to `config.Config` and parse `LYREBIRD_GRPC_PORT` (opt-in: empty → `GRPCPlaneAddr` stays "" and no listener binds) in `internal/infra/config/config.go`; document it in the package doc comment alongside the other `LYREBIRD_*` vars.
- [ ] T003 [P] Create the adapter package skeleton `internal/adapters/grpcplane/doc.go` (package doc: generic plaintext-gRPC data plane, all gRPC-ness confined here, no per-service code).

## Phase 2: Foundational (blocking prerequisites for all stories)

- [ ] T004 [P] Implement the raw passthrough codec in `internal/adapters/grpcplane/codec.go`: a `grpc.Codec`/`encoding.Codec` (via `ForceServerCodec`) that marshals/unmarshals `[]byte` verbatim (name e.g. `"lyrebird.raw"`). Unit test round-trips bytes.
- [ ] T005 Implement schema-free protobuf projection + response builder in `internal/adapters/grpcplane/protowire.go` using `google.golang.org/protobuf/encoding/protowire`:
  - `projectRequest([]byte) (jsonBody []byte, fields map[int32][]rawField, err error)` per data-model.md §1 (keys `fN`; len-delimited → base64; repeated → arrays; keep raw values + wire types for echo).
  - `buildResponse(spec []byte, reqFields map[int32][]rawField) ([]byte, error)` per data-model.md §2 (descriptors `string`/`bytes`/`int`/`bool`/`copyFrom`/`raw`; arrays → repeated; `copyFrom` preserves source wire type and omits when absent; `{}` → empty message).
  - Guard against malformed input (return error, never panic).
- [ ] T006 [P] Unit-test `protowire.go` in `internal/adapters/grpcplane/protowire_test.go`: projection of each wire type + repeated; response build for each descriptor; `copyFrom` wire-type preservation and omit-when-absent; malformed request → error. (Red first, then T005 green.)
- [ ] T007 Implement the gRPC server + listener lifecycle in `internal/adapters/grpcplane/server.go`: constructor taking the match + record use cases (via narrow interfaces named at point of use, mirroring `proxy.Handler`), default space, body cap, clock, logger; `grpc.NewServer(UnknownServiceHandler(handler), ForceServerCodec(raw{}))`; `Serve(net.Listener)` and `GracefulStop()`.
- [ ] T008 Wire the listener into `internal/bootstrap/app.go`: when `cfg.GRPCPlaneAddr != ""`, bind a listener in `Run`, construct the grpcplane server from `c.matchRequestUC` + `c.recordTrafficUC` (+ deps), serve in a goroutine, add fields to `App`, and `GracefulStop` it inside `App.Shutdown`'s concurrent drain. No auth (data plane). When empty, nothing changes.
- [ ] T009 [P] Add stdio-mode parity warning for `LYREBIRD_GRPC_PORT` in `cmd/lyrebird/main.go` (mirror the existing `LYREBIRD_DATA_PORT`/`LYREBIRD_CONTROL_PORT` warning).

## Phase 3: User Story 1 — Generic unary gRPC mock (Priority: P1) 🎯 MVP

**Goal**: A plaintext gRPC client gets a mocked response for a matched unary method with zero
service-specific code; unmatched → clean `Unimplemented`.

**Independent test**: Seed a mock for an arbitrary method with a field-copy response; call it with
a raw gRPC client; assert the echoed field + OK; call an unseeded method → `Unimplemented`.

- [ ] T010 [US1] Author `test/features/grpc_data_plane.feature` with the US1 scenarios (generic unary echo; request-field match hit/miss; no-match → Unimplemented), mirroring existing feature style. Author BEFORE steps/impl (red).
- [ ] T011 [US1] Add `test/support/steps_grpc.go` with a raw-gRPC-client step helper (dial insecure h2c, invoke a method with a hand-built protobuf message, read the reply/ status) and register `RegisterGRPCSteps` in `test/support/main_test.go`. Boot Lyrebird with a gRPC port in the shared `appState`.
- [ ] T012 [US1] Implement the unknown-service stream handler in `internal/adapters/grpcplane/handler.go`: method path via `grpc.MethodFromServerStream`; `RecvMsg` raw bytes; project → `usecase.MatchInput{Method:"POST", Path, Header, Body}`; `matchReq.Execute(ctx, defaultSpace, in)`; matched respond → `buildResponse` + `SendMsg` + record `DecisionMocked` + return nil; no match → record + `status.Error(codes.Unimplemented, …)`; matched proxy/fault → `Unimplemented` (documented); malformed/internal → `InvalidArgument`/`Internal` + record; `recover` guard so it never panics.
- [ ] T013 [US1] Run the US1 scenarios green; fix projection/handler until pass. Confirm `go test ./test/...` US1 scenarios pass and existing suite unaffected.

**Checkpoint**: Generic gRPC mocking works end-to-end (SC-001).

## Phase 4: User Story 2 — KMS Decrypt echo recipe (Priority: P1)

**Goal**: KMS Decrypt echo expressible as a recipe; a real KMS client decrypts base64 stubs.

**Independent test**: Seed `gcp-kms-grpc`; real KMS client (endpoint override, no auth, insecure)
Decrypts a stub; `plaintext == base64decode(input)`.

- [ ] T014 [P] [US2] Create `internal/adapters/examples/recipes/gcp-kms-grpc.json`: id `gcp-kms-grpc`, provider `gcp`, service `kms`, description (endpoint-override + insecure gRPC), and a `mock` matching `POST /google.cloud.kms.v1.KeyManagementService/Decrypt` responding with the field-spec `{"f1":{"copyFrom":2},"f2":{"copyFrom":5},"f3":{"bool":true},"f4":{"int":1}}` (data-model.md §3).
- [ ] T015 [US2] Add the KMS scenario to `grpc_data_plane.feature` (red): seed the recipe, use `cloud.google.com/go/kms/apiv1` in `steps_grpc.go` (add `option.WithEndpoint`, `WithoutAuthentication`, insecure creds), Decrypt a base64 stub, assert plaintext round-trips.
- [ ] T016 [US2] Make the KMS scenario green (recipe + handler already generic; adjust field-spec/echo only if the real client requires it, e.g. crc echo of field 5). No KMS-specific Go code.

**Checkpoint**: SC-002 met.

## Phase 5: User Story 3 — Pub/Sub topic-exists + publish sink recipe (Priority: P2)

**Goal**: Pub/Sub GetTopic returns the topic and Publish returns OK, as a recipe.

**Independent test**: Seed `gcp-pubsub-grpc`; Pub/Sub client `GetTopic(T)` → topic name T (not
NotFound); `Publish` → OK + message ids.

- [ ] T017 [P] [US3] Create `internal/adapters/examples/recipes/gcp-pubsub-grpc.json`: id `gcp-pubsub-grpc`, provider `gcp`, service `pubsub`, description (emulator host, insecure). Include mock(s) for `POST /google.pubsub.v1.Publisher/GetTopic` → `{"f1":{"copyFrom":1}}` and `POST /google.pubsub.v1.Publisher/Publish` → `{"f1":[{"string":"1"}]}`. (If a single recipe carries one mock, prefer the GetTopic mock as the primary and document the Publish mock in the description / a second seed entry.)
- [ ] T018 [US3] Add the Pub/Sub scenario(s) to `grpc_data_plane.feature` (red): seed the recipe(s), use a Pub/Sub client (or raw gRPC) to GetTopic + Publish, assert topic returned + publish OK.
- [ ] T019 [US3] Make the Pub/Sub scenario green (generic engine + recipe). No Pub/Sub-specific Go code.

**Checkpoint**: SC-003 met.

## Phase 6: User Story 4 — Discoverable + reset-safe (Priority: P3)

**Goal**: gRPC recipes appear in the library and are control-plane-accepted; gRPC mocks honor reset.

- [ ] T020 [US4] Update `test/features/examples.feature`: recipe count 9 → 11; add `gcp-kms-grpc` and `gcp-pubsub-grpc` to the "every recipe's mock payload is accepted" scenario outline; verify the "gcp" query count.
- [ ] T021 [US4] Add a reset scenario to `grpc_data_plane.feature`: an ephemeral gRPC mock is removed by `POST /__lyrebird/reset`; a seeded gRPC recipe survives (FR-009). Make green (no new code expected — reuses existing reset).

**Checkpoint**: SC-001–SC-004 covered by the suite.

## Phase 7: Polish & Cross-Cutting

- [ ] T022 [P] Document `LYREBIRD_GRPC_PORT` and the gRPC data plane in `docs/` (mirror existing `LYREBIRD_*`/DESIGN docs); note unary-only + respond-only + default-space behavior.
- [ ] T023 [P] `Dockerfile`: `EXPOSE` the gRPC port (e.g. 50051) alongside 8080/9090; `docker-compose.yml`: map it and show `LYREBIRD_GRPC_PORT`. Confirm multi-arch build unchanged.
- [ ] T024 [P] Record the GCS deferral as a governance item: a short note in `docs/` (or `specs/002-grpc-data-plane/`) stating a stateful GCS store needs a constitution amendment before implementation; surface it to the team.
- [ ] T025 Full gates: `go vet ./...`, `golangci-lint run`, `go test ./...` (all green, zero warnings), and `docker build .` succeed. Confirm existing suite green with the gRPC listener both enabled and disabled (SC-004).

---

## Dependencies & order

- Phase 1 (T001–T003) → Phase 2 (T004–T009) → Phase 3 (US1) is the critical path and the MVP.
- Phase 2 blocks all user stories (shared projection/codec/server/wiring).
- US2 (T014–T016) and US3 (T017–T019) depend on US1's handler (T012) but are independent of each
  other → parallelizable once US1 is green.
- US4 (T020–T021) depends on the recipes (T014, T017) existing.
- Phase 7 is last.

## Parallel opportunities

- Setup: T002, T003 in parallel after/with T001.
- Foundational: T004, T006, T009 in parallel; T005 before T007; T007 before T008.
- After US1 green: US2 and US3 recipe files (T014, T017) and their scenarios in parallel.
- Polish: T022, T023, T024 in parallel.

## MVP scope

**User Story 1 only** (Phases 1–3): a working generic gRPC data plane. KMS (US2) and Pub/Sub
(US3) are then pure configuration/recipe additions proving the model on real consumers.

## Independent test criteria

- US1: seed arbitrary method mock → raw client gets echoed field + OK; unseeded → Unimplemented.
- US2: real KMS client Decrypts base64 stub → plaintext == base64decode(input).
- US3: Pub/Sub client GetTopic → topic (not NotFound); Publish → OK + ids.
- US4: library lists both gRPC recipes and accepts their payloads; reset clears ephemeral, keeps seeded.
