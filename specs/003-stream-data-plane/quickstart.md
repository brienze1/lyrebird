# Quickstart: Generic Byte-Stream Data Plane

**Feature**: [spec.md](./spec.md) · **Contract**: [contracts/stream-data-plane.md](./contracts/stream-data-plane.md)

Every value below is invented. Nothing here is specific to any real device or protocol.

## 0. Enable the plane

```bash
LYREBIRD_STREAM_PORT=7070 lyrebird
```

Unset the variable and nothing in this document exists — the plane is opt-in (FR-001).

## 1. Declare an endpoint

```bash
curl -sS -X POST localhost:9090/__lyrebird/stream/endpoints \
  -H 'Content-Type: application/json' -d '{
    "name": "widget",
    "framing": {"delimiter": "\r\n"},
    "projection": {"split": ","}
  }'
```

Over MCP the same thing is `create_endpoint`.

## 2. Author a rule

```bash
curl -sS -X POST localhost:9090/__lyrebird/mocks \
  -H 'Content-Type: application/json' -d '{
    "name": "widget-read",
    "match": {"path": "/widget", "body": [{"jsonpath": "$.fields.0", "equals": "READ"}]},
    "action": {"respond": {"body": "[{\"text\":\"VALUE,\"},{\"copyFrom\":\"$.fields.1\"}]"}}
  }'
```

An ordinary mock — `create_mock` over MCP, or a seed file. Nothing new to learn (FR-021).

## 3. Connect a stand-in and exchange a frame

```bash
printf 'LYREBIRD/1 widget\r\nREAD,7\r\n' | nc localhost 7070
# OK
# VALUE,7
```

## 4. Validation scenarios

| # | What to do | Expected |
| --- | --- | --- |
| 1 | The exchange above | `OK`, then `VALUE,7\r\n` |
| 2 | Send `WRITE,7\r\n` with no matching rule | nothing written back; connection still usable; one `IN` record with the not-configured decision (FR-032) |
| 3 | Handshake `LYREBIRD/1 nosuch` | `ERR unknown endpoint nosuch`, connection closed (FR-003) |
| 4 | Two stand-ins handshake `widget` in the same space | second gets `ERR endpoint widget already connected` (FR-030) |
| 5 | Add a rule with its own `projection` `at` block and a condition on `$.at.kind` | that rule matches on its own decomposition; other rules still see the endpoint's default (FR-006) |
| 6 | Declare a `ticker` endpoint with a cadence and connect a stand-in that sends nothing | frames arrive in order at the interval; after exhaustion the last repeats (FR-011) |
| 7 | `POST /__lyrebird/stream/emit` while the cadence runs | the injected frame arrives whole, never spliced, recorded as `EMIT` (FR-012, FR-033) |
| 8 | Emit to an endpoint with no stand-in connected | explanatory error, not a silent success (FR-015) |
| 9 | A rule with `action.fault.kind: delay` and `delay_ms: 200` | the answer arrives ~200 ms late and the latency is visible in the record |
| 10 | A rule with `fault.kind: reset` | the connection closes; reconnecting is served normally |
| 11 | A rule with `fault.kind: timeout` | nothing written, but the record's decision differs from an unmatched frame's (FR-017, FR-032) |
| 12 | A rule with `fault.kind: malformed` | the bytes written differ from a well-formed answer in the declared way |
| 13 | `GET /__lyrebird/traffic?path=/widget` | both directions, with `IN`/`OUT`/`EMIT`, timing and the answering rule (FR-018) |
| 14 | `POST /__lyrebird/traffic/{id}/promote`, then repeat the frame | the promoted rule answers it (FR-019) |
| 15 | `POST /__lyrebird/reset` while a stand-in is connected | cadence stops, connection closes, session rules gone, seeded ones remain (FR-022, FR-031) |
| 16 | The same endpoint name declared in two spaces | neither sees the other's rules or captures (FR-024) |
| 17 | `GET /__lyrebird/export` after promoting a capture | the file opens with the captured-traffic warning (FR-035) |
| 18 | Send 2 MiB with no terminator, then a well-formed frame | the remainder is abandoned with an explanatory record and the next frame is served (FR-034) |

## 5. Try the shipped recipe

```bash
curl -sS localhost:9090/__lyrebird/examples/byte-stream-endpoint
```

`list_examples` / `get_example` over MCP carry the same content — a complete, invented endpoint with
framing, a projection, a `copyFrom` answer and a cadence, to copy and adapt.
