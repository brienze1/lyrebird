# Implementation Plan: Generic gRPC Data Plane

**Branch**: `002-grpc-data-plane` | **Date**: 2026-07-07 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/002-grpc-data-plane/spec.md`

## Summary

Add a generic plaintext-gRPC (h2c) data plane that turns an incoming unary gRPC call into the
**existing** HTTP-side match→respond decision, with all gRPC-ness confined to one adapter. The
request protobuf is parsed at the wire level (field-number → value) and projected into the
existing `usecase.MatchInput.Body` so the current matcher/templater apply unchanged; the matched
mock's response body is a declarative field spec re-encoded to protobuf wire. KMS Decrypt echo
and Pub/Sub GetTopic/Publish are delivered as **seed recipes** (field-copy + constants) — zero
per-service code. GCS is out of scope (governance deferral). No domain or use-case changes are
expected.

## Technical Context

**Language/Version**: Go 1.26 (existing module `github.com/brienze1/lyrebird`)

**Primary Dependencies**: NEW `google.golang.org/grpc` (+ transitive `google.golang.org/protobuf`,
`golang.org/x/net`) for the gRPC server via `UnknownServiceHandler` + a raw `[]byte` codec.
Existing: goja (scripting), modernc.org/sqlite (pure-Go store), godog (BDD), mcp go-sdk.
Protobuf wire encode/decode uses `google.golang.org/protobuf/encoding/protowire` (low-level,
schema-free) — NOT generated message types.

**Storage**: existing embedded SQLite (disposable traffic + ephemeral mocks). No new storage.

**Testing**: `go test ./...` (unit + godog BDD). New feature file + step defs; real GCP KMS/
Pub/Sub Go clients OR a minimal raw gRPC client in step defs to prove acceptance.

**Target Platform**: Linux server (distroless static, multi-arch amd64+arm64), `CGO_ENABLED=0`.

**Project Type**: Single Go project, Clean Architecture (domain / usecase / adapters / infra).

**Performance Goals**: Local test-fixture scale (not a perf feature). Unary calls only.

**Constraints**: Static pure-Go binary (no CGO); data plane never authenticated; new listener
opt-in (binds only when `LYREBIRD_GRPC_PORT` set) so unset = today's behavior exactly.

**Scale/Scope**: One new adapter package, config + bootstrap wiring, two recipes, one BDD feature.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Assessment |
|-----------|------------|
| **I. Generic-First — No Per-Service Code** | ✅ **PASS.** The gRPC engine is a single generic transport that routes by method path and matches/builds messages by protobuf field number, with no compiled schema and no service branches. KMS + Pub/Sub live entirely in recipes/seed config. Same "generic engine + recipes" model as the HTTP plane. Explicitly rejects the alternative (typed KMS/Pub/Sub handlers), which would be a direct violation. |
| **II. Agent-First Control Plane (MCP)** | ✅ **PASS.** No new control surface: gRPC mocks are created/read/updated/deleted through the existing MCP + REST mock CRUD and seed loader. Recipes surfaced over the existing `list_examples`/`get_example`. |
| **III. Spy by Default, Disposable by Design** | ✅ **PASS** (bounded note). gRPC mocks are ordinary seeded/ephemeral mocks; ephemeral ones honor reset/GC, seeded ones are protected. Traffic recorded and disposable. **Note:** unmatched gRPC has no "real upstream" to spy-proxy to (no CONNECT/host semantics for arbitrary gRPC), so unmatched → clean `Unimplemented`. Does not regress HTTP spy behavior; it is the sensible gRPC analogue. |
| **IV. Clean Architecture & Test-First (BDD)** | ✅ **PASS.** All gRPC code is an inward-depending adapter; domain + use cases unchanged. BDD feature authored failing first, then green. |
| **V. Secure & Frictionless Defaults** | ✅ **PASS.** Listener is opt-in via env var; the data plane carries no auth (as mandated). No secrets logged. |
| **VI. Ship Continuously** | ⚠️ **PASS with recorded cost.** Adds `google.golang.org/grpc` — a larger (still static, pure-Go, multi-arch) image. Accepted by the user as the correct trade for protocol correctness over a hand-rolled h2c/trailer implementation. CI gates (vet/lint/test/build) unchanged. See Complexity Tracking. |

**GCS deviation (recorded):** Capability #4 (a stateful GCS object store) is **excluded** from
this feature. A store that persists uploaded bytes and serves them back would violate Principle I
("no working tables"/"MUST NOT contain per-service emulation") and Principle III ("MUST NOT
emulate stateful business data"). Per Governance it requires a constitution amendment (version
bump) before any implementation. This plan does not implement it and flags it for a separate
governance decision. See Complexity Tracking.

**Result: PASS.** No unjustified violations. Proceed.

## Project Structure

### Documentation (this feature)

```text
specs/002-grpc-data-plane/
├── plan.md              # This file
├── research.md          # Field numbers + transport decision (done)
├── data-model.md        # gRPC projection + response spec model
├── quickstart.md        # End-to-end validation (KMS + Pub/Sub)
├── contracts/
│   └── grpc-data-plane.md   # The gRPC transport + recipe contract
├── checklists/
│   └── requirements.md  # Spec quality checklist (done)
└── tasks.md             # /speckit-tasks output (next command)
```

### Source Code (repository root)

```text
internal/
├── adapters/
│   ├── grpcplane/                 # NEW — the entire gRPC transport lives here
│   │   ├── server.go              # grpc.Server w/ UnknownServiceHandler + raw codec + listener lifecycle
│   │   ├── codec.go               # raw []byte ForceServerCodec (no proto reflection)
│   │   ├── handler.go             # unknown-service stream handler → match→respond→record
│   │   ├── protowire.go           # wire ⇄ field-number-keyed projection (protowire), response builder
│   │   └── doc.go
│   └── examples/recipes/
│       ├── gcp-kms-grpc.json      # NEW recipe
│       └── gcp-pubsub-grpc.json   # NEW recipe
├── bootstrap/app.go               # MODIFY — bind gRPC listener in Run, add to Shutdown + App
├── infra/config/config.go         # MODIFY — LYREBIRD_GRPC_PORT (opt-in), GRPCPlaneAddr
└── ...                            # domain/ + usecase/ UNCHANGED (goal)

cmd/lyrebird/main.go               # MODIFY — stdio-mode warning parity for LYREBIRD_GRPC_PORT
test/
├── features/grpc_data_plane.feature   # NEW BDD feature (authored failing first)
└── support/steps_grpc.go              # NEW step defs; register in main_test.go
Dockerfile                          # MODIFY — EXPOSE gRPC port
docker-compose.yml                  # MODIFY — map gRPC port
go.mod / go.sum                     # MODIFY — add grpc
```

**Structure Decision**: Single Go project, existing Clean Architecture. The new
`internal/adapters/grpcplane` package is a peer of `internal/adapters/proxy` (the HTTP data
plane) — an inward-depending adapter that consumes existing use cases (`MatchRequest`,
`RecordTraffic`). No new layer, no domain/usecase edits.

## Design overview (detail in contracts/ + data-model.md)

1. **Listener**: `grpc.NewServer(grpc.UnknownServiceHandler(h), grpc.ForceServerCodec(raw{}))`
   on `cfg.GRPCPlaneAddr`; served in a goroutine from `bootstrap.Run`, gracefully stopped in
   `App.Shutdown`. Only constructed/bound when `LYREBIRD_GRPC_PORT` is set.
2. **Handler** (`handler.go`): `grpc.MethodFromServerStream` → path; `RecvMsg(&raw)` → request
   bytes; project wire → `{"fN": <value>}` JSON (bytes b64, varints as numbers, keep raw for
   re-encode); build `usecase.MatchInput{Method:"POST", Path: methodPath, Body: projectedJSON}`;
   `matchReq.Execute(ctx, defaultSpace, in)`. On respond → build response wire from the mock's
   response body (field spec), `SendMsg`, `RecordTraffic`, return nil. Else `Unimplemented`.
   Malformed/internal → `status.Error(codes.Internal/InvalidArgument, …)`. Never panics.
3. **Projection & response builder** (`protowire.go`): schema-free encode/decode via
   `protowire`. Match projection keys fields `fN`; length-delimited kept as base64 for the
   existing string matchers. Response spec: a JSON object of `fN` → typed value or an echo
   reference to a request field (copy raw bytes across field numbers). Covers KMS (1←2, 2←5,
   consts 3/4) and Pub/Sub (1←1; message_ids literal) with no computation.
4. **Recipes**: JSON files embedded like existing ones; `match.path` = `/pkg.Service/Method`,
   `action.respond.body` = the field-spec JSON, plus a marker so the gRPC plane treats the body
   as a proto field-spec (exact encoding in contracts).

## Complexity Tracking

| Violation / Cost | Why Needed | Simpler Alternative Rejected Because |
|------------------|------------|--------------------------------------|
| Add `google.golang.org/grpc` dependency (Principle VI image-size cost) | Correct HTTP/2 framing, `grpc-status` trailers, and protocol edge-cases for real GCP clients dialing insecure gRPC | Hand-rolled `x/net/http2/h2c` + manual trailers is smaller but owns fragile protocol details; user chose correctness over minimal image |
| GCS (#4) deferred, not implemented | A stateful object store is in the original brief but conflicts with Principles I & III | Building it now would violate the constitution without a governance decision; deferral + escalation is the compliant path |
