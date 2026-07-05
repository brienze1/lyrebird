# Contract — MCP Tools (primary control plane)

All tools carry model-oriented descriptions with a minimal valid example and return explanatory
errors (FR-018–FR-020). Every tool accepts an optional `space` argument (default `default`).
Transport: Streamable HTTP (remote) + stdio (local). When auth is enabled, calls require a bearer
token (FR-031, shipped in T055-T057); the data plane is unaffected.

There is no MCP-native token-issuance tool. `/mcp` shares the same path-gated control-plane mux as
Admin REST (`internal/bootstrap/app.go`), and HTTP middleware can only exempt by URL path — not by
which MCP tool a request's JSON-RPC body happens to be calling. A caller therefore obtains a token via
the REST `POST /__lyrebird/auth/token` endpoint (a one-time unauthenticated bootstrap call,
contracts/admin-rest.md — the one route explicitly exempt from the auth gate) and attaches it as an
`Authorization: Bearer` header on every subsequent request to `/mcp` too, including the initial
`initialize` call. See tasks.md T056 for why an MCP-side equivalent isn't added.

> **As of pass 12 of the ongoing refactor**: `list_examples`/`get_example` below (FR-022) are not yet
> registered (tasks.md T058-T060, `internal/adapters/examples/doc.go` is a placeholder). Every other
> tool in this document IS implemented and registered exactly as documented — confirmed against the
> real `sdkmcp.AddTool` registrations in `internal/adapters/mcp/*.go`.

## Guide & scripting docs

| Tool | Input | Output | Requirement |
|------|-------|--------|-------------|
| `lyrebird_guide` | — | Concepts, composition, ≥1 minimal valid mock example. | FR-019 |
| `script_sandbox_api` | — | Docs of injected JS globals (`req`, `uuid()`, `now()`, `faker`, `jsonpath()`). | FR-017 |
| `list_examples` **(PLANNED, NOT YET IMPLEMENTED)** | `{ query? }` | Recipe index (AWS/GCP/third-party as plain HTTP). Content only. | FR-022 |
| `get_example` **(PLANNED, NOT YET IMPLEMENTED)** | `{ id }` | One recipe → ready-to-adapt `create_mock` payload. | FR-022 |

## Mocks

| Tool | Input (key fields) | Output | Requirement |
|------|--------------------|--------|-------------|
| `create_mock` | `{ space?, name, match, script?, action, priority?, lifetime?, ttl_seconds?, group? }` | Created mock (with `id`). | FR-007, FR-025/26 |
| `get_mock` | `{ space?, id }` | Mock. | FR-007 |
| `list_mocks` | `{ space?, group? }` | Mocks. | FR-007 |
| `update_mock` | `{ id, ...fields }` | Updated mock. | FR-007 |
| `delete_mock` | `{ space?, id }` | Ack. | FR-007 |
| `reset` | `{ space?, clear_traffic? }` | Counts removed; seeded mocks preserved. | FR-028 |
| `match_test` | `{ space?, sample_request }` | Which mock fires, per-condition pass/fail, resulting response — no forwarding. | FR-011 |

## Spy / traffic / metrics

| Tool | Input | Output | Requirement |
|------|-------|--------|-------------|
| `list_traffic` | `{ space?, host?, path?, status?, since?, limit? }` | Traffic summaries. | FR-021 |
| `get_traffic` | `{ space?, id }` | Full request + response (decrypted). | FR-002, FR-021 |
| `inspect_requests` | `{ space?, limit? }` | Recent requests (debug why a mock missed). | FR-021 |
| `metrics` | `{ space?, window? }` | Counts + latency aggregated by mock/path/status. | FR-021 |
| `clear_traffic` | `{ space? }` | Ack. | FR-027/28 |
| `promote_traffic` | `{ traffic_id, name?, lifetime? }` | New mock reproducing the recorded response. | FR-012 |

## Upstreams & partitions

| Tool | Input | Output | Requirement |
|------|-------|--------|-------------|
| `set_upstream` | `{ space?, match_host, target_url, tls_skip_verify? }` | Upstream. | FR-003 |
| `list_upstreams` | `{ space? }` | Upstreams. | FR-003 |
| `create_space` | `{ id, description? }` | Partition. | FR-023 |
| `list_spaces` | — | Partitions. | FR-023 |
| `delete_space` | `{ id }` | Cascade result; refuses `default`. | FR-024 |

## MITM (forward-proxy mode)

Only registered when `LYREBIRD_MITM_ENABLED=true` (T054/T067) — otherwise this tool does not exist.

| Tool | Input | Output | Requirement |
|------|-------|--------|-------------|
| `get_mitm_ca_cert` | `{}` | CA certificate, PEM-encoded (`pem` field + raw text content). Never the private key. | FR-033 |

## Error contract

Errors state **what** failed and **how to fix** it, e.g.:
`match.body[0] did not match: request JSON had no key "foo" (keys: ["bar","baz"])`.
