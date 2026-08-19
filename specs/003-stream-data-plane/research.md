# Research: Generic Byte-Stream Data Plane

**Feature**: [spec.md](./spec.md) · **Plan**: [plan.md](./plan.md) · **Date**: 2026-08-19

Every decision below was taken against read source in this repository, with the alternative
rejected recorded alongside it.

## 1. Why a third plane rather than a new product

`internal/adapters/grpcplane/` is the working precedent: a whole second data plane whose entire
service-specific surface is zero. `handler.handle` reads one message, projects it, calls
`usecase.MatchRequest.Execute`, writes the answer and records — 163 lines, no business logic.
`grpcplane/doc.go` states the rule explicitly: *"Everything gRPC-specific lives here and NOTHING
here is service-specific."*

A byte-stream plane is the same shape a third time. **Rejected**: a separate mock server for stream
protocols — it would duplicate spaces, seeds, reset, TTL/GC, promotion, scripting, metrics and the
whole MCP control plane, which is precisely the cost extending Lyrebird avoids.

## 2. Why TCP with a handshake, not a PTY or a virtual serial device

A stand-in is an ordinary process on the same machine. Giving it a TCP socket costs nothing and is
portable; a PTY or a virtual serial device is OS-specific, needs privileges on some platforms, and
buys nothing the stand-in cannot get from `net.Dial`.

The handshake solves an addressing problem the gRPC plane could not: a gRPC client cannot carry
Lyrebird's space header, so `grpcplane` serves the **default space only** (`handler.go`'s
`h.defaultSpace`). A stream handshake is Lyrebird's own line, so `space=` fits naturally and the
stream plane gets the space selection the gRPC plane lacks.

**Rejected**: one listening port per endpoint — it turns endpoint declaration into an operator
concern (ports must be allocated and mapped before Lyrebird boots), which defeats declaring an
endpoint at runtime over MCP.

## 3. Why the envelope, and why no protocol decoder

`domain.Match.Body` is a `[]BodyMatcher` of JSONPath conditions. The cheapest way for a stream frame
to be matchable by the **existing** matcher is to present it as JSON. Hence the envelope:
`$.len`, `$.hex` and `$.text` always present, `$.fields.n` and `$.at.<name>` populated from a
declared projection.

`$.text` is decoded **latin-1**, not UTF-8: every byte 0x00–0xFF maps to exactly one rune, so an
arbitrary binary frame round-trips and `contains`/`regex` still work on the ASCII parts. UTF-8
decoding would replace invalid sequences with U+FFFD and silently destroy binary content.

**Rejected**: parsing known protocols in the engine. That is a constitution Principle I violation
and an unbounded maintenance treadmill — exactly the argument the constitution already makes
against per-service emulation.

## 4. Where the projection lives

The upstream design put the projection on the rule. `usecase.MatchRequest.Execute` takes one
`MatchInput` and loops candidates internally, so a per-rule projection means a different `Body` per
candidate — which the adapter cannot supply without re-implementing candidate ordering, scenario
peeking and script gating.

**Decision**: endpoint-level default, rule-level override, served by a new
`MatchRequest.ExecuteProjected` that keeps the loop in the use-case layer and asks an injected
`BodyProjector` port for each candidate's body. `Execute` is left untouched so neither existing
plane can regress.

**Rejected**: endpoint-only projection (simplest, but a rule that needs a different decomposition of
the same bytes would have to fall back to raw `$.hex` regexes). **Rejected**: re-projecting inside
the adapter (a layering violation, and it would duplicate `loadSortedCandidates`).

## 5. Why respond goes through `BuildRespondOutputWithScript`

`usecase.BuildRespondOutputWithScript` (`match_request.go:126`) is what makes a mock's
`Script.RespondSrc` run. `proxy/handler.go:314` calls it; `grpcplane/handler.go` does **not**, which
is why scripted responses do not work on the gRPC plane. Routing this plane through it closes that
gap for byte streams at no extra cost, since the function already takes a plain `MatchInput`.

## 6. Why one writer goroutine per connection

Three sources can write to a live connection: an answer to an inbound frame, a cadence tick, and an
injection. Left unsynchronised they interleave and a frame is spliced. A single goroutine draining
one buffered channel makes emission order the delivery order for free, which is what SC-006's
"ten identical sequences" requires. A mutex around each write would prevent splicing but would not
give a deterministic order.

## 7. Why reset closes connections

`usecase.Reset` deletes ephemeral mocks and restarts scenarios. If a live connection and its cadence
survived that, a stand-in would keep receiving frames from rules that no longer exist. Closing the
connection makes the stand-in observe exactly what it would observe if the peripheral were
unplugged, and forces it to reconnect into clean state. The registry is injected **nil-safe** so a
Lyrebird with the plane disabled is unaffected.

## 8. Why the traffic table needs no migration

`traffic` stores `method`, `host`, `path`, `status`, `latency_ms`, `decision`, `matched_mock_id`,
`request_blob`, `response_blob` — all free-form enough to carry `method` as a direction (`IN`/`OUT`/
`EMIT`), `host` as `stream`, `path` as `/<endpoint>` and `status` as `0`. The gRPC plane already
writes `status: 0` for the same reason. `list_traffic`, `get_traffic`, `promote_traffic`,
`clear_traffic` and `metrics` therefore work unchanged.

## 9. Why `ephemeral_mocks` does need two columns

The rule-level projection override and FR-035's capture provenance are both per-mock facts with
nowhere to live: `domain.Mock` has no such fields and `schema.sql`'s `CREATE TABLE IF NOT EXISTS`
cannot add a column to an existing database file. An idempotent `ALTER TABLE … ADD COLUMN`, guarded
by `PRAGMA table_info`, runs in `store.openAndMigrate`. Both columns are additive and nullable, so
an older file is migrated in place rather than invalidated.

**Rejected** for provenance: inferring it from `promote_traffic`'s default `promoted-<id>` name —
it breaks silently the moment a caller supplies its own name, and FR-035 is a data-protection
requirement that must not fail quietly.

## 10. Why the unmatched frame is silent

HTTP answers an unmatched request with a status; a byte stream has no such vocabulary. Writing
anything at all would be inventing protocol. Writing nothing, recording the frame, and leaving the
connection usable is exactly what a silent peripheral looks like, and it keeps "no rule authored"
and "peripheral unplugged" distinguishable — by the recorded decision, which is why both are
recorded.

## 11. Why `proxy` is refused at creation, not at serve time

A rule with `action.proxy` on a stream endpoint can never be served: there is no upstream, by
design. Failing at serve time would surface as a mysterious silent frame long after the mistake.
Rejecting it when the rule is created states the limitation at the moment the author can act on it —
the same discipline `MockCRUD.validate` already applies to every other malformed action.

## 12. Framing variants

Three cover the field: a **delimiter** (line-oriented ASCII protocols), a fixed **length**, and a
**length prefix**. All three are declarative and none implies a protocol. A frame that has not
completed is held until it does, bounded by `LYREBIRD_BODY_CAP_BYTES` — reusing the cap Lyrebird
already has rather than inventing a second one — after which the remainder is abandoned with an
explanatory record and the reader resynchronises at the next boundary (FR-034).
