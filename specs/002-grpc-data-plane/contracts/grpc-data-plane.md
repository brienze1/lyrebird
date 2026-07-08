# Contract: gRPC Data Plane

Peer to `contracts/data-plane.md` (HTTP). Defines the plaintext-gRPC transport and how it maps
onto the existing mock model. No new control-plane surface; no new domain types.

## Listener

- Address: `cfg.GRPCPlaneAddr`, from `LYREBIRD_GRPC_PORT` (e.g. `:50051`). **Opt-in**: the
  listener is constructed and bound **only when the variable is set**. Unset → no gRPC surface,
  Lyrebird behaves exactly as today (FR-010).
- Transport: plaintext HTTP/2 (h2c). No TLS, no authentication (FR-011). Clients dial with
  insecure transport credentials + authentication disabled.
- Server: `grpc.NewServer(grpc.UnknownServiceHandler(handler), grpc.ForceServerCodec(rawCodec{}))`.
  `rawCodec` (un)marshals `[]byte` verbatim — no proto reflection, no registered services.
- Lifecycle: served from `bootstrap.Run` in a goroutine; `GracefulStop` on `App.Shutdown`
  (joined with the HTTP servers' drain).

## Request handling (per unary call)

1. Method path via `grpc.MethodFromServerStream(stream)` → e.g.
   `/google.cloud.kms.v1.KeyManagementService/Decrypt`.
2. `stream.RecvMsg(&raw)` → request message bytes. (Streaming methods: not supported → step 6
   returns `Unimplemented`.)
3. Decode + project the request (see data-model.md §1) into `usecase.MatchInput`:
   `{Method:"POST", Path: <method path>, Header: <metadata>, Body: <projected {fN:…} JSON>}`.
4. `matchReq.Execute(ctx, cfg.DefaultSpace, in)` — the **existing** match use case. gRPC calls
   carry no space header, so they always resolve in the default space.
5. Outcome:
   - **Matched `respond`** → build response wire bytes from the mock's respond body as a
     response field-spec (data-model.md §2), `stream.SendMsg(bytes)`, record traffic
     (`DecisionMocked`), return `nil` (grpc-go writes `grpc-status: 0`).
   - **No match** → record (`DecisionNotConfigured`/not-matched), return
     `status.Error(codes.Unimplemented, "no gRPC mock matched <method>")`.
   - **Matched `proxy` or `fault`** → not supported on the gRPC plane in this feature; return
     `status.Error(codes.Unimplemented, …)` (documented limitation; respond-only).
6. **Failure isolation**: a malformed request body, a bad response field-spec, or any internal
   error → `status.Error(codes.InvalidArgument|Internal, <explanatory message>)` and a recorded
   error decision. The handler NEVER panics or hangs (recover guard around the projection/encode).

## Response field-spec

See data-model.md §2 for the descriptor grammar (`string`/`bytes`/`int`/`bool`/`copyFrom`/`raw`,
arrays for repeated fields). The gRPC listener ALWAYS interprets a matched respond body as a
field-spec; the HTTP plane is unaffected (a gRPC recipe's `match.path` is a gRPC method path that
HTTP requests never carry).

## Mock authoring (recipe / seed / CRUD shape)

A gRPC mock is an ordinary mock. Example create_mock / seed payload (KMS Decrypt echo):

```json
{
  "name": "gcp-kms-decrypt-echo",
  "match": {
    "method": "POST",
    "path": "/google.cloud.kms.v1.KeyManagementService/Decrypt"
  },
  "action": {
    "respond": {
      "body": "{\"f1\":{\"copyFrom\":2},\"f2\":{\"copyFrom\":5},\"f3\":{\"bool\":true},\"f4\":{\"int\":1}}"
    }
  }
}
```

- `respond.status` may be omitted (defaults harmlessly; unused by gRPC).
- Matching on request fields uses the existing `match.body` JSONPath entries against the projected
  body, e.g. `{ "jsonpath": "f1", "exists": true }` (field 1 present) — optional; path-only match
  is the common case.
- Seed YAML uses the same shape as existing seed mocks (`internal/infra/seeds`), so the
  creator-ads stack pre-seeds these via `/config`.

## Recipe library entries (FR-012)

Two new embedded recipes surfaced over the existing `list_examples`/`get_example` (REST + MCP):
`gcp-kms-grpc` (Decrypt echo) and `gcp-pubsub-grpc` (Publisher GetTopic echo + Publish sink).
The recipe count in `examples.feature` moves from 9 → 11; the "gcp" query and the
"every recipe is control-plane-accepted" outline gain the two new ids.

## Reset / GC / spaces (FR-009)

Unchanged mechanisms: ephemeral gRPC mocks are removed by `POST /__lyrebird/reset` and expire
under `LYREBIRD_TRAFFIC_TTL`/GC; seeded gRPC recipes are protected. gRPC traffic is recorded and
GC'd like HTTP traffic.

## Config / image (FR-014)

- `LYREBIRD_GRPC_PORT` documented in the existing `LYREBIRD_*` style (README/docs + compose).
- `Dockerfile` `EXPOSE`s the port; `docker-compose.yml` maps it. Multi-arch build unchanged.
