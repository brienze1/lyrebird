# Data Model: Generic Byte-Stream Data Plane

**Feature**: [spec.md](./spec.md) · **Plan**: [plan.md](./plan.md) · **Date**: 2026-08-19

What this feature adds to the domain, what it reuses unchanged, and the exact shape of the two
declarative grammars a rule author writes.

## 1. New entity — Endpoint

A named boundary. Declared in seed config or created during a session; a stand-in connects to one.

| Field | Type | Rules |
| --- | --- | --- |
| `Name` | string | Unique within a space. What a handshake names and a rule's `match.path` targets as `/<name>`. May contain internal `/` segments (a namespaced family, e.g. `cb5/spp`); must not start or end with `/`, contain an empty (`//`) or dot (`.`/`..`) segment, or contain whitespace. |
| `Partition` | string | The space it belongs to. |
| `Framing` | `Framing` | How the stream divides into frames. Exactly one variant (§2). Required. |
| `Projection` | `*Projection` | The default field decomposition for every rule on this endpoint (§4). Optional. |
| `Cadence` | `*Cadence` | Unprompted emission (§3). Optional. |
| `Lifetime` | `domain.Lifetime` | `seeded` survives `reset` and never expires; `ephemeral` does neither. The same type `domain.Mock` uses. |

**Occupancy** is runtime state, not a field: at most one connection may hold an endpoint at a time
within a space (FR-030). A second handshake for an occupied endpoint is refused; a handshake for an
undeclared name is refused (FR-003).

**Lifecycle**: declared → idle → occupied → idle. `reset` stops any cadence, closes the connection,
and removes the endpoint if it is not seeded (FR-031).

## 2. Framing

Where one frame ends. Declared once per endpoint; no rule may override it (FR-005).

| Variant | Fields | Meaning |
| --- | --- | --- |
| `delimiter` | `bytes`, e.g. `"\r\n"` | A frame ends at the first occurrence. The delimiter is part of the frame as recorded, and is appended to a built answer unless the answer declares itself `raw`. |
| `length` | `n` | Every frame is exactly `n` bytes. |
| `prefix` | `width`, `endian` | The first `width` bytes carry the length of what follows. |

A run of bytes that has not completed a frame is held until it does, up to `LYREBIRD_BODY_CAP_BYTES`
(FR-034); beyond that the remainder is abandoned with an explanatory record and the reader
resynchronises at the next frame boundary.

## 3. Cadence

Unprompted emission (FR-011). Belongs to an endpoint.

| Field | Type | Rules |
| --- | --- | --- |
| `Interval` | duration | Time between emissions. Must not be negative. `0` is "immediate" (below). |
| `Frames` | `[][]FramePart` | Emitted in order, one per interval. Must be non-empty. |
| `OnExhaust` | `repeat_last` \| `loop` \| `stop` | Default `repeat_last` — what a stationary source does. |

A cadence starts when a stand-in occupies the endpoint and stops when the connection ends or the
space is reset. Its emissions go through the connection's single writer, so they never interleave
with an answer or an injection (FR-033).

**`Interval: 0` — immediate mode.** Every frame is queued back-to-back with no wait at all, paced
only by the connection's own backpressure (the outbound queue's bounded channel, and beneath it the
peer's TCP receive window) — never by a clock. This is for a source whose bytes are simply *there*
for whoever reads them, with nothing about the result allowed to depend on how much real time has
passed since occupancy (CB5-1 WI-13: a `100ms`+ cadence used to fake a continuously-streaming
peripheral made what was available depend on the host's wall clock, unrelated to a scenario's own
substituted clock — exactly the load-dependence a cadence with `Interval > 0` is fine to have for a
genuinely time-based source, and exactly wrong for one that is not).

## 4. Frame envelope — what a rule matches against

Every inbound frame is projected into one JSON document, which `domain.Match.Body`'s existing
JSONPath matchers address. This is the byte-stream counterpart of the gRPC plane's `{fN}`
projection, and the reason no protocol decoder lives in Lyrebird (FR-006, FR-008).

```json
{
  "len": 12,
  "hex": "412c31322c4f4b0d0a",
  "text": "A,12,OK\r\n",
  "fields": ["A", "12", "OK\r\n"],
  "at": { "kind": "A", "count": 12, "marker": "412c" }
}
```

| Path | Always present | Built from |
| --- | --- | --- |
| `$.len` | yes | the frame's byte count |
| `$.hex` | yes | the whole frame, lowercase hex |
| `$.text` | yes | the whole frame decoded byte-for-byte as latin-1, so every byte round-trips |
| `$.fields.n` | only when a `split` is declared | the frame split on that separator |
| `$.at.<name>` | only for names declared | a run of bytes at an offset |

### Projection declaration

Declared on the **endpoint** as the default, and overridable by an individual **rule** (FR-006).
A rule that declares one replaces the endpoint's entirely for that rule; a rule that declares none
inherits the endpoint's.

```yaml
projection:
  split: ","                       # optional — populates $.fields
  at:                              # optional — populates $.at
    - {name: kind,   offset: 0, length: 1, as: text}
    - {name: count,  offset: 2, length: 2, as: int}
    - {name: marker, offset: 0, length: 2, as: hex}
```

`as` is one of `text`, `int`, `hex`; omitted means `text`. A declaration addressing bytes the frame
does not have yields an **absent** value rather than an error, so an `exists: false` condition can
match on it (FR-027).

## 5. Frame spec — what a rule builds

An ordered list of parts, concatenated. Generalises the gRPC plane's response descriptor
(`string`/`bytes`/`int`/`copyFrom`/`raw`) from protobuf fields to raw bytes (FR-009). It is carried
as the JSON text of `domain.RespondAction.Body`, so nothing new stores it.

| Part | Produces |
| --- | --- |
| `{"text": "OK"}` | those bytes, latin-1 |
| `{"hex": "0d0a"}` | those bytes |
| `{"int": 7, "width": 3, "pad": "0"}` | the number as text, left-padded to `width` |
| `{"copyFrom": "$.at.kind"}` | the value at that path in the triggering frame's envelope |
| `{"repeat": {"hex": "00"}, "times": 4}` | that part, repeated |

The endpoint's framing terminator is appended automatically unless the spec sets `"raw": true` —
which is how the `malformed` fault produces a frame with its terminator missing.

```json
{"parts": [{"text": "&"}, {"copyFrom": "$.at.kind"}, {"text": "OK"}]}
```

A bare JSON array is accepted as shorthand for `{"parts": [...]}`.

## 6. Reused unchanged

| Existing type | How this plane uses it |
| --- | --- |
| `domain.Mock` | A stream rule **is** a mock. `match.path` = `/<endpoint>`, `match.method` = `FRAME`, `match.body` = JSONPath conditions over the envelope. Two additive fields (§9), no new type. |
| `domain.Match` | `Method`, `Path`, `Headers`, `Query`, `Body` — all as they are. `Headers` carries the handshake keys. |
| `domain.Action` | `respond` (the frame spec of §5), `fault`, and `cadence` (WI-02: a runtime, reversible override of an endpoint's already-declared, already-running cadence — see the "Runtime cadence override" section of `contracts/stream-data-plane.md`). `proxy` is rejected at creation. |
| `domain.FaultKind` | `delay` → slow line · `reset` → dropped connection · `timeout` → silence · `malformed` → corrupted bytes. All four map without addition (FR-016). |
| `domain.Script` | `MatchSrc` gates the match; `RespondSrc` builds the frame. Both under `LYREBIRD_SCRIPT_TIMEOUT`. |
| `usecase.MatchInput` | `{Method:"FRAME", Path:"/<endpoint>", Host:"stream", Header:<handshake>, Body:<envelope>}` |
| `traffic` table | `host` = `stream`, `path` = `/<endpoint>`, `method` = direction, `status` = `0`. No migration ([research.md §8](./research.md)). |
| `domain.Decision` | Existing vocabulary — `mocked` on an answer, the not-configured one on an unmatched frame, the internal-error one on a malformed frame. |

## 7. Traffic record, per frame

One record per frame, each direction (FR-018).

| Column | Value |
| --- | --- |
| `method` | `IN` (stand-in → Lyrebird) · `OUT` (answer to a frame) · `EMIT` (cadence or injection — unprompted, FR-013) |
| `host` | `stream` |
| `path` | `/<endpoint>` |
| `status` | `0` |
| `latency_ms` | for `OUT`, the time from its `IN` frame; `0` otherwise |
| `decision` | which outcome applied |
| `matched_mock_id` | the rule that answered, when one did |
| `request_blob` / `response_blob` | the frame bytes, capped by `LYREBIRD_BODY_CAP_BYTES` and marked truncated beyond it (FR-028), encrypted at rest like every other body |

## 8. Provenance, for the export warning

`promote_traffic` marks the mock it creates as derived from captured traffic; `export_config` emits
a warning header in any file containing such a mock (FR-035). The marker is deliberately
plane-agnostic — a promoted HTTP mock carries captured bytes just as a promoted stream mock does —
so the warning is honest wherever it appears.

## 9. Schema changes

| Change | Why |
| --- | --- |
| `ephemeral_endpoints` table | session-created endpoints, mirroring `ephemeral_mocks`. Seeded endpoints never reach it. |
| `ephemeral_mocks.projection_blob` | the rule-level projection override (§4). Sealed, like the other per-mock blobs. |
| `ephemeral_mocks.from_capture` | the provenance marker (§8). |

Applied by an idempotent `ALTER TABLE … ADD COLUMN` guarded by `PRAGMA table_info`, since
`CREATE TABLE IF NOT EXISTS` cannot add a column to an existing file.

## 10. Validation rules

- An endpoint name must be unique within its space, non-empty, and free of whitespace; it may
  contain internal `/` segments but must not start or end with `/`, contain an empty (`//`)
  segment, or contain a `.`/`..` segment (`http.ServeMux` cleans those out of the request path
  before pattern matching, so a name containing one would be undeletable through
  `DELETE /__lyrebird/stream/endpoints/{name...}`).
- `framing` is required and must set exactly one variant; `length` and `prefix.width` must be
  positive; `delimiter` must be non-empty.
- A `cadence` must have a non-negative interval (`0` is "immediate", §3) and a non-empty frame
  list — an empty one is an error, not a silent no-op.
- A projection field must have a name, a non-negative offset, a positive length and a known `as`.
- A rule whose `match.path` names an endpoint that does not exist is accepted (rules are authored
  before boundaries exist in some orders) but reported by the endpoint listing as unreachable.
- A rule with `action.proxy` whose `match.method` is `FRAME` is rejected at creation with an
  explanatory error, rather than failing at serve time (FR-025).
- A rule with `action.cadence` must itself declare a non-negative `interval` (when set — omitted
  inherits the endpoint's), a non-empty `frames` list, and a known `on_exhaustion` (when set) — the
  same rules a cadence declaration enforces (§10 above), plus a bound on a `repeat` part's `times`
  the endpoint-level declaration does not need (WI-02). A rule with `action.cadence` whose
  `match.method` is explicitly something other than `FRAME` is rejected at creation, mirroring the
  `proxy` rule above.
