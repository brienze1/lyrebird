# Lyrebird — Agent Guide

Lyrebird is a spy-by-default HTTP mock server. Point your client (or its SDK) at Lyrebird instead
of the real service. Any request that doesn't match a mock is recorded and transparently forwarded
to a real upstream you configure — nothing breaks by default, and everything is visible.

## Concepts

- **Spy passthrough**: unmatched requests are recorded and forwarded verbatim to the upstream you
  configure with `set_upstream`. This is the default; you only create mocks for the behavior you
  want to override.
- **Mock**: a named rule with a `match` (which requests it applies to) and an `action` (what
  happens when it fires: `respond`, `proxy`, or `fault`). Multiple mocks may target the same route;
  the highest-`priority` one wins, ties broken by newest-created.
- **Space**: an isolation boundary (multi-tenant partition). Omit `space` to use the default one.
- **Seeded vs ephemeral**: seeded mocks come from mounted config files and are immutable via the
  API; mocks you create with `create_mock` are ephemeral and can be updated/deleted.

## Minimal valid example

```json
{
  "name": "ping",
  "match": { "method": "GET", "path": "/ping" },
  "action": { "respond": { "status": 200, "body": "pong" } }
}
```

Create it with `create_mock`, verify it with `match_test` before sending real traffic, then send a
real `GET /ping` — it will return `pong` without ever reaching any upstream.

## Typical workflow

1. `set_upstream` — point spy passthrough at the real service (optional, but usually first).
2. `create_mock` — declare the behavior you want to override.
3. `match_test` — dry-run a sample request to confirm the mock fires and see per-condition detail,
   without sending anything onward.
4. Send the real request — the mock fires.
5. `promote_traffic` — turn any recorded real interaction into a persistent mock reproducing it, in
   one call, instead of hand-authoring the match/action.

## Matching

`match` supports `method`, `path` (exact string, glob like `/users/*`, or regex prefixed with `~`),
`headers`/`query` (map of name → `{equals|contains|regex|exists}`), and `body` (list of
`{jsonpath, equals|contains|regex|exists}`). An empty `match` matches every request — useful as a
catch-all fallback at low priority.

## Traffic & metrics

`list_traffic`/`get_traffic`/`inspect_requests` let you see what actually happened; `metrics`
aggregates counts and latency by mock/path/status; `reset` clears ephemeral mocks (and optionally
traffic) while preserving seeded fixtures.

## Other data planes (opt-in)

Lyrebird serves the same match→respond model over two more transports. Both are off unless the
operator sets their port, and neither needs a new control surface: rules on them are ordinary mocks,
so `create_mock`, `match_test`, `promote_traffic`, spaces, `reset` and TTL all apply as they do to
HTTP.

**gRPC** (`LYREBIRD_GRPC_PORT`): a unary call is matched by its method path (`/pkg.Service/Method`)
and its protobuf is parsed at the wire level, so a mock matches request fields as `$.f1`, `$.f2`, …
with no compiled schema. The respond body is a field-spec re-encoded to protobuf. See the
`gcp-kms-grpc` and `gcp-pubsub-grpc` recipes.

**Byte streams** (`LYREBIRD_STREAM_PORT`): for a peripheral that speaks framed bytes rather than
requests — a serial line, a serial-profile radio link, a pin-level control channel.

1. `create_endpoint` declares the boundary and its `framing` (exactly one of `delimiter`,
   `delimiter_hex`, `length`, or `prefix`), plus an optional default `projection` and `cadence`.
2. A stand-in process connects over TCP and sends one CRLF-terminated line,
   `LYREBIRD/1 <endpoint> [space=<name>]`. It gets `OK`, or an `ERR …` line naming the problem.
3. Rules are ordinary mocks with `match.method` `FRAME` and `match.path` `/<endpoint>`. Each frame
   is projected into an envelope your usual body matchers address:

   - `$.len`, `$.hex`, `$.text` — always present (`$.text` is latin-1, so every byte round-trips)
   - `$.fields.0`, `$.fields.1`, … — when the projection declares a `split`
   - `$.at.<name>` — for each declared `{name, offset, length, as: text|int|hex}`

   Use gjson-style paths (`$.fields.0`), not bracket indexes. A rule may carry its own `projection`
   to read the same bytes differently from every other rule on that endpoint.
4. The respond body is a list of parts: `[{"text":"OK"}]`, `[{"hex":"0d0a"}]`,
   `[{"int":7,"width":3,"pad":"0"}]`, `[{"copyFrom":"$.fields.1"}]` to echo part of the triggering
   frame, `[{"repeat":{"hex":"00"},"times":4}]`. The endpoint's terminator is appended unless you
   use the object form with `"raw": true`. `script.respond_src` works here too — return that JSON as
   a string.
5. `emit_frame` pushes a frame into a live connection with nothing having asked for it, and an
   endpoint's `cadence` emits a declared sequence on an interval. Both record as `EMIT`; an inbound
   frame records as `IN` and an answer as `OUT`.

Faults work as elsewhere: `delay` (a slow line), `reset` (the connection drops), `timeout` (silence,
distinguishable from an unmatched frame only by the recorded decision), `malformed` (bytes without
the framing terminator). `proxy` is **not** served on this plane — there is no real device to
forward to — and such a rule is rejected when you create it. Full walkthrough: `get_example` with
`byte-stream-endpoint`.
