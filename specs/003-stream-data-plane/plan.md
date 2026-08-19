# Implementation Plan: Generic Byte-Stream Data Plane

**Branch**: `003-stream-data-plane` | **Date**: 2026-08-19 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/003-stream-data-plane/spec.md`

## Summary

Add a generic, opt-in **byte-stream data plane** that turns an inbound frame into the **existing**
match→respond decision, with all stream-ness confined to one adapter (`internal/adapters/
streamplane/`, peer of `grpcplane/`). A stand-in dials TCP, names an endpoint in a one-line
handshake, and every frame in either direction is projected into a JSON envelope the existing
JSONPath body matchers address, matched by `usecase.MatchRequest`, answered through
`usecase.BuildRespondOutputWithScript`, faulted through the existing `domain.FaultKind`s, and
recorded into the existing `traffic` table.

Framing and projection are **declared as configuration**, so no protocol decoder of any kind enters
Lyrebird — the direct analogue of the gRPC plane's schema-free `{fN}` projection. One genuinely new
capability is added: **originate**, emitting bytes with no inbound frame to provoke them.

## Technical Context

**Language/Version**: Go 1.26 (existing module `github.com/brienze1/lyrebird`)

**Primary Dependencies**: **none new.** TCP via `net`; JSONPath via the existing
`internal/adapters/jsonpath`; matching, scripting, templating, recording all via existing use cases.

**Storage**: existing embedded SQLite. One new table (`ephemeral_endpoints`) and two additive
columns on `ephemeral_mocks` (`projection_blob`, `from_capture`), applied by an idempotent
column migration since `CREATE TABLE IF NOT EXISTS` cannot add a column to an existing file.

**Testing**: `go test ./...` (unit + godog BDD). New `test/features/stream_data_plane.feature` and
`test/support/steps_stream.go`, plus a fuzz target on the projection.

**Target Platform**: Linux server (distroless static, multi-arch), `CGO_ENABLED=0`.

**Project Type**: Single Go project, Clean Architecture (domain / usecase / adapters / infra).

**Performance Goals**: an answer within 10 ms at p99 on a developer machine (SC-007). Local
test-fixture scale.

**Constraints**: static pure-Go binary; the data plane is never authenticated; the listener is
opt-in (binds only when `LYREBIRD_STREAM_PORT` is set) so unset means today's behaviour exactly.

## Constitution Check

| Principle | Gate | Verdict |
| --- | --- | --- |
| I. Generic-First — No Per-Service Code | Does any protocol knowledge enter the codebase? | **PASS.** Framing, projection and frame-building are declarative grammars. No decoder for any protocol exists in `streamplane/`, and the one shipped recipe is an invented illustrative protocol. |
| II. Agent-First Control Plane (MCP) | Is every new capability an MCP tool first, with a thin REST twin and logic in the use-case layer? | **PASS.** `create_endpoint`, `list_endpoints`, `delete_endpoint`, `emit_frame` are MCP tools with example payloads; `/__lyrebird/stream/…` is a thin twin; all logic sits in `usecase/endpoints.go` and `usecase/emit_frame.go`. No UI. |
| III. Spy by Default, Disposable by Design | Is captured state disposable, and seeded state protected? | **PASS.** Frames land in the existing `traffic` table under the existing TTL/GC. Seeded endpoints live in memory only, exactly as seeded mocks do. |
| IV. Clean Architecture & Test-First (BDD) | Feature files authored and failing before implementation? | **PASS.** `stream_data_plane.feature` is written and proven red per user story before each phase's code. Dependencies point inward only: `streamplane/` imports `usecase` and `domain`, never the reverse. |
| V. Secure & Frictionless Defaults | Does a new surface appear without being asked for? | **PASS.** Unset `LYREBIRD_STREAM_PORT` binds nothing. The plane, like every data plane, is never authenticated — stated, not implied. |
| VI. Ship Continuously | Is it in the image and documented? | **PASS.** `EXPOSE` in the `Dockerfile`, mapped in `docker-compose.yml`, documented in the README env table. |

### Complexity Tracking — one recorded deviation

**`domain.ActionProxy` is not served on this plane.** The other planes forward to a real upstream;
here there is no upstream to forward to — the stand-in exists precisely so that no device is
attached. Rather than invent a half-working forward, a `proxy` action on a stream endpoint is
**rejected at rule-creation time** with an explanatory error, and documented as deferred. This is
the same resolution `002-grpc-data-plane` reached for proxy and fault on the gRPC plane.

## Design decisions requiring care

### 1. Projection is endpoint-default with a per-rule override

`usecase.MatchRequest.Execute` takes **one** `MatchInput` and iterates candidates internally. A rule
overriding the projection needs a *different* `MatchInput.Body` for that candidate alone.

Re-implementing candidate ordering, scenario peeking and script gating in the adapter would be a
layering violation. Instead `MatchRequest` gains a sibling method that keeps all of it in the
use-case layer and asks an injected port for the per-candidate body:

```go
type BodyProjector interface { ProjectFor(m domain.Mock) ([]byte, error) }

func (uc *MatchRequest) ExecuteProjected(
    ctx context.Context, partition string, base MatchInput, p BodyProjector,
) (domain.Mock, MatchInput, bool, error)
```

It returns the `MatchInput` that actually won, so the responder's `copyFrom` resolves against the
right envelope. `Execute` is untouched: the HTTP and gRPC planes carry zero risk from this feature.

### 2. One writer goroutine per connection

Answers, cadence ticks and injections all reach the socket through a single channel-fed writer, so
a frame is never spliced and emission order is delivery order (FR-033, SC-006). Nothing writes to
the connection directly.

### 3. Recording never fails a served frame

As in `proxy.Handler` and `grpcplane.handler`, a recording failure is logged, never propagated —
losing traffic-log data is acceptable (Principle III); failing an already-served frame is not.

## Project Structure

```text
internal/
├── domain/
│   ├── endpoint.go          # NEW — Endpoint, Framing, Projection, Cadence, FramePart
│   └── mock.go              # + Projection *Projection, FromCapture bool
├── usecase/
│   ├── endpoints.go         # NEW — declare / list / delete, validation
│   ├── emit_frame.go        # NEW — injection
│   ├── match_request.go     # + ExecuteProjected
│   ├── ports.go             # + EndpointRepo, ConnectionRegistry, BodyProjector
│   ├── reset.go             # + nil-safe ConnectionRegistry
│   ├── promote_traffic.go   # + FromCapture
│   └── import_export.go     # + endpoints in the bundle, + capture warning
├── adapters/
│   ├── streamplane/         # NEW — the whole plane
│   ├── dto/endpoint.go      # NEW
│   ├── mcp/stream.go        # NEW
│   └── httpadmin/stream.go  # NEW
├── infra/
│   ├── config/config.go     # + StreamPlaneAddr
│   ├── store/               # + endpoints.go, migrate.go, schema.sql
│   └── seeds/seeds.go       # + endpoints:, + rule-level projection:
└── bootstrap/app.go         # + opt-in listener, routes, drain

test/
├── features/stream_data_plane.feature   # NEW
└── support/steps_stream.go              # NEW
```

## Phasing

1. **Setup** — config seam, package skeleton. Proven inert when unset.
2. **Foundational** — domain types, framing reader, projection, frame builder, ports, endpoint use
   case, store, seeds, registry, connection, handshake, listener.
3. **US1 (P1, MVP)** — the per-frame handler: match, respond, unmatched-and-silent.
4. **US2 (P2)** — cadence, `emit_frame`, `EMIT` recording, ordering guarantees.
5. **US3 (P3)** — the four faults.
6. **US4 (P4)** — traffic mapping, reset wiring, endpoint CRUD over MCP + REST, promotion
   provenance, export.
7. **Polish** — a generic recipe, README/Dockerfile/compose, `DESIGN.md`, the MCP guide, and the
   performance and resilience checks.

MVP is phases 1–3: a plane a stand-in can connect to and be answered on, already recording.
