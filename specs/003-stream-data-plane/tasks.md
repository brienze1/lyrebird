---
description: "Task list for 003 — Generic byte-stream data plane"
---

# Tasks: Generic Byte-Stream Data Plane

**Input**: Design documents in `/specs/003-stream-data-plane/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md),
[data-model.md](./data-model.md), [contracts/](./contracts/)

**Tests**: Not optional. Constitution Principle IV requires BDD feature files authored and
**failing** before implementation, so each story phase opens with its scenarios in
`test/features/stream_data_plane.feature`. A scenario is never weakened to make it pass.

**Organization**: Grouped by user story, in the spec's priority order (US1 P1 → US4 P4). Each phase
is an independently shippable increment; US1 alone is a usable plane.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on incomplete tasks)
- **[Story]**: which user story the task belongs to

---

## Phase 1: Setup

- [X] T001 Open `specs/003-stream-data-plane/` with spec, plan, research, data-model, contract,
  quickstart, tasks and checklist, gated on the constitution
- [X] T002 Add `StreamPlaneAddr` to `internal/infra/config/config.go` from `LYREBIRD_STREAM_PORT`,
  left empty when unset, mirroring the `LYREBIRD_GRPC_PORT` block (FR-001)
- [X] T003 [P] Add the config test proving an unset `LYREBIRD_STREAM_PORT` yields an empty
  `StreamPlaneAddr` in `internal/infra/config/config_test.go` (FR-026)
- [X] T004 [P] Create `internal/adapters/streamplane/doc.go` stating its role as the third data
  plane, peer to `grpcplane/`, and that nothing protocol-specific may ever live there

**Checkpoint**: the config seam exists and is proven inert when unset.

---

## Phase 2: Foundational (blocking prerequisites)

- [X] T005 Define `Endpoint`, `Framing`, `Projection`, `ProjectionField`, `Cadence`, `OnExhaustion`
  and `FramePart` in `internal/domain/endpoint.go` per data-model §1–§5, with no dependency on any
  other layer. Framing belongs to the endpoint, never to a rule (FR-005)
- [X] T006 [P] Implement the framing reader in `internal/adapters/streamplane/framing.go`: all three
  variants, holding an incomplete frame, abandoning the remainder past `LYREBIRD_BODY_CAP_BYTES`
  and resynchronising (FR-034)
- [X] T007 [P] Implement the projection in `internal/adapters/streamplane/project.go`: frame bytes →
  the envelope of data-model §4, treating an out-of-range declaration as an absent value rather than
  an error (FR-006, FR-027)
- [X] T008 [P] Implement the frame builder in `internal/adapters/streamplane/build.go`: the part list
  of data-model §5, appending the endpoint's terminator unless `raw` (FR-009)
- [X] T009 [P] Add fuzz targets for the projection and the frame builder — no input may panic or
  hang (FR-027)
- [X] T010 Add `EndpointRepo`, `ConnectionRegistry` and `BodyProjector` to `internal/usecase/ports.go`
- [X] T011 Add `MatchRequest.ExecuteProjected` in `internal/usecase/match_request.go`, keeping
  candidate ordering, scenario peeking and script gating in the use-case layer while asking the
  injected projector for each candidate's body (research §4). `Execute` untouched
- [X] T012 Implement declare/list/delete in `internal/usecase/endpoints.go`, enforcing data-model
  §10 — unique name per space, exactly one framing variant, no empty cadence, and rejection of a
  `proxy` action on a stream rule (FR-025)
- [X] T013 Add `domain.Mock.Projection` and `domain.Mock.FromCapture`, the `ephemeral_endpoints`
  table and the two additive columns, with the idempotent column migration in
  `internal/infra/store/migrate.go` (research §9)
- [X] T014 Extend seed loading with an `endpoints:` block and a rule-level `projection:` key in
  `internal/infra/seeds/seeds.go`, protected from reset/GC exactly as seeded mocks are (FR-022)
- [X] T015 Implement the connection registry in `internal/adapters/streamplane/registry.go`: live
  connections keyed by space and endpoint, one stand-in per endpoint (FR-030)
- [X] T016 Implement the connection in `internal/adapters/streamplane/conn.go`: the framing reader
  feeding the handler, and **one writer goroutine fed by a channel** (FR-033, research §6)
- [X] T017 Implement the handshake in `internal/adapters/streamplane/handshake.go` per the contract
  (FR-002, FR-003)
- [X] T018 Implement the listener in `internal/adapters/streamplane/server.go` and bind it in
  `internal/bootstrap/app.go` only when `cfg.StreamPlaneAddr` is set, drained on `App.Shutdown`
- [X] T019 Add the bootstrap test proving that with `LYREBIRD_STREAM_PORT` unset nothing binds and
  the existing planes are untouched (FR-026, SC-005)

---

## Phase 3: User Story 1 — answer a frame (P1) 🎯 MVP

- [X] T020 [US1] Write the US1 scenarios in `test/features/stream_data_plane.feature` and confirm
  they fail (constitution IV, red before green)
- [X] T021 [P] [US1] Implement the step definitions in `test/support/steps_stream.go` following the
  shape of `steps_grpc.go`
- [X] T022 [US1] Implement the per-frame handler in `internal/adapters/streamplane/handler.go`:
  project → `MatchInput{Method:"FRAME", …}` → `ExecuteProjected` → record every frame, under a
  recover guard (FR-004, FR-007, FR-021, FR-027)
- [X] T023 [US1] Serve a matched `respond` through `usecase.BuildRespondOutputWithScript`, so respond
  scripts run on this plane unlike the gRPC plane (FR-010, research §5)
- [X] T024 [US1] Implement the unmatched outcome: write nothing, record the not-configured decision,
  leave the connection usable (FR-032)
- [X] T025 [P] [US1] Add handler unit tests covering match, no-match, projection override, script
  gating and a malformed frame

---

## Phase 4: User Story 2 — emit without being asked (P2)

- [X] T026 [US2] Write the US2 scenarios and confirm they fail
- [X] T027 [US2] Implement the cadence in `internal/adapters/streamplane/cadence.go` with
  `repeat_last` (default), `loop` and `stop` (FR-011)
- [X] T028 [US2] Implement `internal/usecase/emit_frame.go`, returning an explanatory error when no
  stand-in holds the endpoint (FR-012, FR-015)
- [X] T029 [P] [US2] Expose `emit_frame` as an MCP tool in `internal/adapters/mcp/stream.go`
- [X] T030 [P] [US2] Expose `POST /__lyrebird/stream/emit` in `internal/adapters/httpadmin/stream.go`
- [X] T031 [US2] Record unprompted emissions with `method: EMIT` (FR-013, data-model §7)
- [X] T032 [P] [US2] Add the ordering test in `conn_test.go`: an injection racing a cadence tick and
  an answer never produces a spliced frame (FR-033, SC-006)

---

## Phase 5: User Story 3 — the failures a real line produces (P3)

- [X] T033 [US3] Write the US3 scenarios and confirm they fail
- [X] T034 [US3] Implement the fault mapping in `internal/adapters/streamplane/fault.go` (FR-016,
  SC-003)
- [X] T035 [US3] Ensure a `timeout` fault and an unmatched frame are distinguishable by decision
  alone (FR-017, FR-032)
- [X] T036 [P] [US3] Add fault unit tests, including that a `delay` is visible in the recorded
  latency and a `reset` leaves the endpoint reconnectable

---

## Phase 6: User Story 4 — captures and parity (P4)

- [X] T037 [US4] Write the US4 scenarios and confirm they fail
- [X] T038 [US4] Complete the traffic column mapping of data-model §7 and mark a body truncated past
  the cap (FR-018, FR-028)
- [X] T039 [US4] Add the nil-safe `ConnectionRegistry` collaborator to `internal/usecase/reset.go`
  (FR-022, FR-024, FR-031, research §7)
- [X] T040 [P] [US4] Expose `create_endpoint`, `list_endpoints`, `delete_endpoint` as MCP tools
- [X] T041 [P] [US4] Expose their REST twins under `/__lyrebird/stream/endpoints`, exposing nothing
  the MCP tools lack (FR-021, constitution II)
- [X] T042 [P] [US4] Add the endpoint DTOs in `internal/adapters/dto/endpoint.go` with round-trip
  and fuzz tests
- [X] T043 [US4] Confirm `promote_traffic` turns a captured stream exchange into a working rule
  (FR-019, SC-004)
- [X] T044 [US4] Mark promoted mocks as derived from captured traffic and emit the export warning
  (FR-035, SC-010, data-model §8)
- [X] T045 [P] [US4] Render endpoints in `export_config` output in the seed shape (FR-023)
- [X] T046 [P] [US4] Confirm stream traffic appears in `metrics` with no adapter change (FR-020)

---

## Phase 7: Polish

- [X] T047 [P] Add one **generic** recipe `byte-stream-endpoint` to
  `internal/adapters/examples/recipes/`, entirely invented (FR-008, FR-029, SC-010), and register it
  in `examples.go` with the recipe-count expectation in `test/features/examples.feature`
- [X] T048 [P] Document `LYREBIRD_STREAM_PORT` in the README env table, and add the missing
  `LYREBIRD_GRPC_PORT` row while there
- [X] T049 [P] `EXPOSE` the stream port in the `Dockerfile` and map it in `docker-compose.yml`
- [X] T050 [P] Add a byte-stream section to `docs/DESIGN.md` covering the handshake, the envelope,
  the single writer, and why `proxy` is not served (FR-025)
- [X] T051 [P] Extend `internal/adapters/mcp/content/guide.md` so an agent can author a stream rule
  from the guide alone (constitution II)
- [X] T052 Verify SC-006 (ten identical cadence sequences) and SC-008 (a hundred malformed inputs
  leave other connections served) as tests

---

## Dependencies

```text
Phase 1 → Phase 2 (blocks every story) → Phase 3 (US1, MVP)
                                       → Phase 4 (US2) → Phase 5 (US3) → Phase 6 (US4) → Phase 7
```

Every story after US1 extends `handler.go`, which US1 creates, so the order is a real dependency
chain rather than a preference.

## Out of scope

- **Forwarding to a real device** — deferred and documented (plan.md, Complexity Tracking).
- **Consumer-specific endpoint recipes** — Lyrebird is generic-first (constitution Principle I);
  a consumer's endpoints are that consumer's configuration, not Lyrebird's code or content.
