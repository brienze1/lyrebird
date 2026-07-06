# Contract — Admin REST (thin twin of MCP)

A thin HTTP mirror of the MCP control plane over the **same use-case layer** — it MUST NOT expose
anything MCP lacks (Principle II, FR-018). For curl/scripts/health and CI. Base path `/__lyrebird`.
Partition via `X-Lyrebird-Space` header (default `default`). When auth is enabled, all routes except
the token endpoint and health require `Authorization: Bearer` (FR-031).

> **As of Phase 12 (tasks.md T061)**: import/export now ships (`GET /__lyrebird/export`,
> `POST /__lyrebird/import`), registered unconditionally in `internal/bootstrap/app.go` — no longer
> PLANNED. Every row below is implemented and registered exactly as documented (the auth-token flow
> shipped in T055-T057, the examples/recipe endpoints shipped in T058-T060) except
> `GET /__lyrebird/metrics`: `metrics` exists as an MCP tool (`internal/adapters/mcp/traffic.go`) but
> has no REST twin — this is allowed under FR-018 (MCP may have capabilities REST lacks; only the
> reverse is forbidden), but the row below is aspirational, not live, until a REST handler is added.

| Method & Path | Body / Query | Maps to | Requirement |
|---------------|--------------|---------|-------------|
| `GET /__lyrebird/mocks` | `?group=` | list_mocks | FR-007 |
| `POST /__lyrebird/mocks` | Mock | create_mock | FR-007 |
| `GET /__lyrebird/mocks/{id}` | — | get_mock | FR-007 |
| `PUT /__lyrebird/mocks/{id}` | Mock | update_mock | FR-007 |
| `DELETE /__lyrebird/mocks/{id}` | — | delete_mock | FR-007 |
| `POST /__lyrebird/match-test` | sample request | match_test | FR-011 |
| `POST /__lyrebird/reset` | `{ clear_traffic? }` | reset | FR-028 |
| `GET /__lyrebird/traffic` | filters | list_traffic | FR-021 |
| `GET /__lyrebird/traffic/{id}` | — | get_traffic | FR-002 |
| `GET /__lyrebird/metrics` **(PLANNED, NOT YET IMPLEMENTED)** | `?window=` | metrics | FR-021 |
| `POST /__lyrebird/traffic/{id}/promote` | `{ name?, ttl_seconds? }` | promote_traffic | FR-012 |
| `GET/POST /__lyrebird/upstreams` | Upstream | list/set_upstream | FR-003 |
| `GET/POST/DELETE /__lyrebird/spaces[/{id}]` | Partition | space tools | FR-023/24 |
| `GET /__lyrebird/mitm/ca-cert` | — (raw `application/x-pem-file` body, not JSON) | get_mitm_ca_cert; only registered when `LYREBIRD_MITM_ENABLED=true` (T054/T067) | FR-033 |
| `GET /__lyrebird/examples[/{id}]` | `?query=` (list only) | list/get_example | FR-022 |
| `GET /__lyrebird/export` | — (returns `application/x-yaml`) | export_config; ephemeral mocks + upstreams only, current space | tasks.md T061 |
| `POST /__lyrebird/import` | YAML bundle (same shape `export_config` returns, or a `contracts/seed-config.md`-shaped file) | import_config; additive, current space | tasks.md T061 |
| `POST /__lyrebird/auth/token` | `{ client_key }` | issue JWT (auth-enabled only; exempt from the auth gate itself) | FR-031 |
| `GET /__lyrebird/healthz` `GET /__lyrebird/readyz` | — | liveness/readiness (never authed) | FR-034 |

Ports: data-plane proxy listener(s) separate from the control-plane listener (MCP HTTP + this REST).
