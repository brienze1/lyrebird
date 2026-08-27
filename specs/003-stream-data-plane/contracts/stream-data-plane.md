# Contract: byte-stream data plane

Peer to `contracts/data-plane.md` (HTTP) and `002-grpc-data-plane/contracts/grpc-data-plane.md`.
Defines the transport, the handshake and how a byte stream maps onto the existing mock model. No new
control-plane surface; one new domain entity (Endpoint).

## Listener

- Address: `cfg.StreamPlaneAddr`, from `LYREBIRD_STREAM_PORT` (e.g. `7070` → `:7070`). **Opt-in**:
  the listener is constructed and bound only when the variable is set. Unset → no byte-stream
  surface, Lyrebird behaves exactly as today (FR-001, FR-026).
- Transport: TCP. No TLS, **no authentication ever** — the data plane is never authenticated
  (constitution Principle V).
- Lifecycle: served from `bootstrap.Run` in a goroutine, drained on `App.Shutdown` alongside the
  HTTP and gRPC servers.

## Handshake

The first line a client sends, terminated CRLF, before any frame bytes:

```
LYREBIRD/1 <endpoint> [space=<name>] [key=value ...]
```

The server replies with one CRLF-terminated line and then either streams or closes:

| Reply | When |
| --- | --- |
| `OK` | The endpoint exists in the resolved space and is unoccupied. The stream begins. |
| `ERR unknown endpoint <name>` | No endpoint by that name in that space (FR-003). Connection closes. |
| `ERR endpoint <name> already connected` | Another stand-in holds it (FR-030). Connection closes. |
| `ERR malformed handshake` | The first line is not the form above. Connection closes. |
| `ERR handshake timeout` | Nothing arrived within the read deadline. Connection closes. |

`space=` is optional; absent, the default space applies. This is the space selection the gRPC plane
could not have, because a gRPC client cannot carry Lyrebird's space header. Any further `key=value`
pairs become `MatchInput.Header` entries, so a rule can condition on them.

## Per-frame handling

1. Accumulate bytes until the endpoint's declared framing completes a frame (data-model §2), or
   until `LYREBIRD_BODY_CAP_BYTES` is exceeded → abandon the remainder, record an explanatory error
   decision, resynchronise (FR-034).
2. Project the frame into the envelope (data-model §4) using the endpoint's default projection, and
   per candidate rule using that rule's override when it declares one.
3. Build `usecase.MatchInput{Method:"FRAME", Path:"/<endpoint>", Host:"stream", Header:<handshake
   keys>, Body:<envelope>}` and call `usecase.MatchRequest.ExecuteProjected` with the resolved space
   — which brings scripted matching along for free.
4. Record the inbound frame (`method: IN`).
5. Outcome:
   - **Matched `respond`** → build the frame through `usecase.BuildRespondOutputWithScript` (so
     respond scripts run, unlike the gRPC plane), queue it on the connection's writer, record
     (`method: OUT`, `decision: mocked`, `matched_mock_id`).
   - **Matched `fault`** → apply the mapping below, record the fault decision.
   - **Matched `proxy`** → not served. Record an explanatory error decision and leave the connection
     usable. Creation of such a rule is rejected up front (data-model §10), so reaching this at
     serve time means the endpoint was renamed under an existing rule.
   - **No match** → write nothing, record the not-configured decision, leave the connection usable
     (FR-032). This is what a silent peripheral looks like; the consumer's own timeout governs.
6. **Failure isolation**: a projection failure, a bad frame spec or any internal error produces a
   recorded error decision and leaves the connection usable. The handler never panics and never
   hangs (FR-027) — a recover guard wraps projection and encoding, as `grpcplane/handler.go` does.

## Fault mapping

| `FaultKind` | On a byte stream |
| --- | --- |
| `delay` | The answer is queued after the declared delay. The line is slow, not broken. |
| `reset` | The connection is closed. The stand-in observes the peripheral going away; a reconnect is served normally. |
| `timeout` | Nothing is written. Distinguished from an unmatched frame only by the recorded decision — which is exactly why both are recorded. |
| `malformed` | The built frame is corrupted in the declared way: the framing terminator is omitted (`raw: true` in the frame spec), or the declared bytes are themselves wrong. The consumer's own validation is what rejects it. |

## Emission without a triggering frame

- **Cadence** — declared on the endpoint (data-model §3). Starts when a stand-in occupies the
  endpoint, stops on disconnect or reset.
- **Injection** — `emit_frame` over MCP, `POST /__lyrebird/stream/emit` over Admin REST. Takes the
  space, the endpoint and a frame spec. Returns an explanatory error when no stand-in holds the
  endpoint (FR-015).

Both go through the connection's single writer goroutine, so an emission never interleaves with an
answer or with another emission, and the emitted order is the delivered order (FR-033). Both are
recorded with `method: EMIT` (FR-013).

## Runtime cadence override

An ordinary mock may carry a third action kind, `cadence`, that overrides — at runtime and
reversibly — what an endpoint's **already-declared, already-running** cadence sustains, instead of
its own declared content. Motivating case: a seeded cadence with no clock of its own (e.g. a 2ms
"immediate-ish" repeater) cannot be redeclared or out-shouted by a one-shot injection once a
stand-in occupies it; a runtime override is the only way to make a scenario-chosen value the
*sustained* content of a cadence that is already ticking.

```json
{
  "name": "gps-override",
  "priority": 100,
  "match": { "method": "FRAME", "path": "/cb5/gps" },
  "action": {
    "cadence": {
      "frames": ["[{\"text\":\"$GPRMC,...*hh\\r\"}]"]
    }
  }
}
```

- **Resolution**: per tick, not per connection — the connection's single writer resolves the
  winning `cadence`-action mock for the endpoint fresh each time (same total order as every other
  mock kind: priority desc, `created_at` desc, id — a runtime ephemeral outranks a seed at equal
  priority), and falls back to the endpoint's own declared cadence when none matches. Picked up by
  an already-open connection — no re-occupancy, no reconnect.
- **Content-only by default**: `action.cadence.frames` is the one field an override always sets.
  `interval`/`on_exhaustion` are optional and inherit the endpoint's declared cadence's values when
  omitted — the common case changes only WHAT is sustained, never the pacing or exhaustion rule.
- **Frames are spec STRINGS, not structured parts** (`action.cadence.frames: []string`, each a
  frame spec — the same grammar a respond body, `emit_frame` and a cadence declaration's own frames
  already use), unlike a cadence *declaration*'s structured `[][]{text,hex,int,...}` shape. This is
  forced by MCP: `action` is the exact same `dto.ActionDTO` `create_mock`/`update_mock` already
  expose (the Admin-REST-is-a-thin-twin-of-MCP rule), and `FramePartDTO` is self-referential through
  `repeat` — the MCP SDK's schema generator rejects a recursive Go type outright, exactly the
  constraint `CadenceIn.Frames` and `EmitFrameIn.Frame` already work around.
- **Reverting**: deleting the override mock (`delete_mock` / `DELETE /__lyrebird/mocks/{id}`) hands
  the endpoint straight back to its declared cadence on the very next tick, on the SAME connection —
  no reconnect. `reset` also clears the ephemeral override, but — being a whole-space reset — it
  additionally closes every live connection and removes every session-created endpoint in that space
  exactly as it always has ("Reset, GC, spaces" below); a scenario that wants the connection itself
  to survive reverting the content uses delete, not reset.
- **Recording is unchanged**: an overridden tick is recorded exactly like a seeded one
  (`method: EMIT`, `decision: mocked`) — overriding what a cadence emits changes the content, never
  the traffic shape.
- **Only meaningful on a stream rule**: `action.cadence` on a rule whose `match.method` is
  explicitly something other than `FRAME` is rejected at write time, mirroring the existing `proxy`
  refusal above.

## Control-plane surface

No new surface — new operations on the existing MCP server and its Admin REST twin, business logic
in the use-case layer only (constitution Principle II).

| Operation | MCP tool | Admin REST |
| --- | --- | --- |
| Declare an endpoint | `create_endpoint` | `POST /__lyrebird/stream/endpoints` |
| List endpoints and their occupancy | `list_endpoints` | `GET /__lyrebird/stream/endpoints` |
| Remove an endpoint | `delete_endpoint` | `DELETE /__lyrebird/stream/endpoints/{name...}` |
| Inject a frame | `emit_frame` | `POST /__lyrebird/stream/emit` |

Rules themselves need nothing new: a stream rule is an ordinary mock, so `create_mock`,
`update_mock`, `delete_mock`, `list_mocks`, `get_mock`, `match_test` and `promote_traffic` all apply
as they are (FR-021).

## Rule authoring

```json
{
  "name": "widget-status-ok",
  "match": {
    "method": "FRAME",
    "path": "/widget",
    "body": [
      { "jsonpath": "$.at.kind", "equals": "A" },
      { "jsonpath": "$.at.count", "regex": "^[0-9]+$" }
    ]
  },
  "projection": {
    "at": [
      { "name": "kind",  "offset": 0, "length": 1, "as": "text" },
      { "name": "count", "offset": 2, "length": 2, "as": "int" }
    ]
  },
  "action": {
    "respond": {
      "body": "[{\"text\":\"&\"},{\"copyFrom\":\"$.at.kind\"},{\"text\":\"OK\"}]"
    }
  }
}
```

`match.method` may be omitted; `FRAME` is the only value a stream frame ever carries, so a path-only
match is the common case — the same shape gRPC mocks use. `projection` is optional: without it the
rule inherits the endpoint's default.

## Seed config

```yaml
space: default
endpoints:
  - name: widget
    framing: {delimiter: "\r\n"}
    projection:
      split: ","
  - name: ticker
    framing: {delimiter: "\n"}
    cadence:
      interval: 100ms
      on_exhaustion: repeat_last
      frames:
        - [{text: "TICK,0001"}]
        - [{text: "TICK,0002"}]
mocks:
  - name: widget-read
    match:
      path: "/widget"
      body: [{jsonpath: "$.fields.0", equals: "READ"}]
    action:
      respond:
        body: '[{"text":"VALUE,"},{"copyFrom":"$.fields.1"}]'
```

Seeded endpoints and mocks are protected from `reset`, GC and TTL exactly as seeded HTTP mocks are.
Every value above is invented (FR-029).

## Reset, GC, spaces

- `reset` on a space stops every cadence on its endpoints, closes every connection to them, removes
  session-created endpoints and mocks, and leaves seeded ones (FR-031, FR-022).
- Stream traffic is recorded, retained and GC'd under `LYREBIRD_TRAFFIC_TTL` like every other plane.
- Endpoint names are scoped to a space: the same name in two spaces is two endpoints with no shared
  rules and no shared captures (FR-024).

## Export

`export_config` renders a space's endpoints alongside its mocks and upstreams, in the seed shape
above. When the export contains any mock derived from captured traffic, the file opens with a
warning comment saying so (FR-035).

## Deferred

**Forwarding to a real device is not served.** There is nothing to forward to: a stand-in exists
precisely so no hardware is attached. A `proxy` rule on a stream endpoint is rejected at creation
with an explanatory error. Documented as deferred, exactly as `002` deferred proxy and fault on the
gRPC plane.

## Config and image

- `LYREBIRD_STREAM_PORT` documented in the README's `LYREBIRD_*` table, `EXPOSE`d in the
  `Dockerfile`, mapped in `docker-compose.yml` (constitution Principle VI).
- The README's env table was missing `LYREBIRD_GRPC_PORT`; it is added in the same pass.
