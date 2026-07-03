# Phase 1 Data Model — Lyrebird

Serializable JSON/YAML entities that round-trip over MCP, Admin REST, and seed config. Sensitive
fields (marked 🔒) are encrypted at rest; index/routing fields stay plaintext for filtering + GC.

## Partition (Space)

Isolation boundary owning mocks, traffic, and upstream config for one agent/session/tenant.

| Field | Type | Notes |
|-------|------|-------|
| `id` | string | Slug; `default` is reserved and non-deletable. |
| `created_at` | timestamp | |
| `description` | string? | Optional. |

- Selected per-request via `X-Lyrebird-Space` header; absent → `default`.
- **Delete** cascades ephemeral mocks, traffic, upstreams in this partition. `default` cannot be
  deleted (FR-024).

## Upstream

Real target for passthrough within a partition.

| Field | Type | Notes |
|-------|------|-------|
| `partition` | string | FK → Partition.id |
| `match_host` | string | Incoming host/prefix this rule applies to (glob allowed). |
| `target_url` | string | Real base URL to forward to. |
| `tls_skip_verify` | bool | Default false. |

- In forward-proxy/MITM mode the target is derived from the request; Upstream config is only required
  for reverse-proxy mode (FR-003).

## Mock

A rule that may intercept matching calls. Many mocks may share a route (FR-009).

| Field | Type | Notes |
|-------|------|-------|
| `id` | string | Server-assigned. |
| `partition` | string | FK → Partition.id (default `default`). |
| `name` | string | Human/agent label. |
| `lifetime` | enum | `seeded` \| `ephemeral`. Seeded = from config, protected from reset/GC (FR-025). |
| `ttl_seconds` | int? | Ephemeral only; auto-remove after elapsed (FR-026). |
| `priority` | int | Higher wins; ties → most-recently-created wins (FR-009a). |
| `group` | string? | Bulk-management label. |
| `match` | Match | Declarative conditions (below). |
| `script` | Script? | 🔒 Optional JS `match`/`respond` hooks (FR-014). |
| `action` | Action | `respond` \| `proxy` \| `fault` (below). |
| `scenario` | Scenario? | Opt-in sequential responses. |
| `created_at` | timestamp | Tie-break ordering. |

### Match (declarative)

| Field | Type | Notes |
|-------|------|-------|
| `method` | string? | e.g. `GET`, `POST`; omitted = any. |
| `path` | string? | exact \| glob \| regex (prefix `~` for regex). |
| `headers` | map<string,Matcher>? | header name → matcher. |
| `query` | map<string,Matcher>? | query param → matcher. |
| `body` | list<BodyMatcher>? | JSONPath/regex/contains conditions. |

`Matcher` = `{ equals? , contains? , regex? , exists? }`. All present conditions must hold (AND).

### Action

- `respond`: `{ status, headers, body 🔒, template?: bool, latency_ms? }` — body may template request
  values or be built by the response script (FR-010).
- `proxy`: `{ rewrite_request?, transform_response?, latency_ms? }` — forward to Upstream; verbatim
  upstream response unless transformed; 502/504 synthesized on unreachable/timeout (R6, FR-004).
- `fault`: `{ kind: delay|reset|timeout|malformed, delay_ms? }` (FR-005).

### Script 🔒

| Field | Type | Notes |
|-------|------|-------|
| `match_src` | string? | JS returning bool. |
| `respond_src` | string? | JS returning a response object. |

Runs in goja sandbox; no fs/net/env except injected helpers; bounded by timeout + memory guard
(FR-015, FR-016).

### Scenario

| Field | Type | Notes |
|-------|------|-------|
| `responses` | list<Action.respond> | Returned in order on successive matching calls. |
| `on_exhaust` | enum | `repeat_last` \| `wrap` \| `fallthrough`. |

State kept in `scenario_state`; reset on `reset`.

## Traffic Record

Every call through a partition (FR-002). Subject to retention GC (FR-027).

| Field | Type | Notes |
|-------|------|-------|
| `id` | string | |
| `partition` | string | plaintext (filter). |
| `timestamp` | timestamp | plaintext (filter + GC). |
| `method`, `host`, `path` | string | plaintext (filter). |
| `request` | blob 🔒 | Full request: headers + body (truncated > cap, default 1 MB, with marker). |
| `matched_mock_id` | string? | Which mock fired, or null for pure passthrough. |
| `decision` | enum | `mocked` \| `proxied` \| `faulted` \| `not_configured`. |
| `response` | blob 🔒 | Full returned response; for proxied calls, the real upstream response. |
| `status` | int | plaintext (filter/metrics). |
| `latency_ms` | int | plaintext (metrics). |

## MITM Certificate Authority (forward-proxy mode only)

Used only when the transparent forward-proxy/MITM mode is enabled (US7 / M6). Not present in
reverse-proxy mode.

| Field | Type | Notes |
|-------|------|-------|
| `ca_cert` | PEM | Lyrebird root cert; exposed for clients to trust. Public — not secret. |
| `ca_key` | PEM 🔒 | Root private key. Sensitive: encrypted at rest with the same AES-256-GCM key path; NEVER logged or exported over any interface. |
| `leaf_cache` | in-memory | Per-host leaf certs signed on the fly; ephemeral, never persisted. |

- **Lifecycle**: by default the CA is generated at startup (in-memory, disposable — regenerated each
  boot). Operators MAY supply a stable CA via mounted files/env so trust survives restarts; when
  supplied, `ca_key` is treated as sensitive material under the same handling rules as tokens/keys
  (Principle V, FR-033). Leaf certs are minted per-host into the in-memory cache and discarded on
  restart.

## Token / Client Credential (auth-enabled only)

- `LYREBIRD_AUTH_KEYS` holds accepted client secrets (never persisted, never logged).
- Token endpoint returns a JWT (HS256), default 1h TTL. Verified statelessly; no storage (FR-031).

## Relationships

```
Partition 1──* Upstream
Partition 1──* Mock ──0..1 Script
                   └──0..1 Scenario ──1 scenario_state
Partition 1──* TrafficRecord ──0..1 (matched_mock_id → Mock)
```

## Lifecycle & invariants

- Seeded mocks: loaded at boot from `/config`, in-memory, immune to reset/GC/TTL.
- Ephemeral mocks: in SQLite; removed by TTL expiry, `reset`, or partition delete.
- Traffic: append-only; pruned by retention window (`LYREBIRD_TRAFFIC_TTL`, default 24h).
- Disposability: total loss of SQLite on restart is valid state, not corruption (FR-029).
- Determinism: matching order = priority desc, then created_at desc, then id (FR-009a).
