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
