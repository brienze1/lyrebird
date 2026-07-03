# Contract — Admin REST (thin twin of MCP)

A thin HTTP mirror of the MCP control plane over the **same use-case layer** — it MUST NOT expose
anything MCP lacks (Principle II, FR-018). For curl/scripts/health and CI. Base path `/__lyrebird`.
Partition via `X-Lyrebird-Space` header (default `default`). When auth is enabled, all routes except
the token endpoint and health require `Authorization: Bearer` (FR-031).

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
| `GET /__lyrebird/metrics` | `?window=` | metrics | FR-021 |
| `POST /__lyrebird/traffic/{id}/promote` | `{ name?, lifetime? }` | promote_traffic | FR-012 |
| `GET/POST /__lyrebird/upstreams` | Upstream | list/set_upstream | FR-003 |
| `GET/POST/DELETE /__lyrebird/spaces[/{id}]` | Partition | space tools | FR-023/24 |
| `GET /__lyrebird/examples[/{id}]` | — | list/get_example | FR-022 |
| `POST /__lyrebird/import` / `GET /__lyrebird/export` | YAML bundle | seed round-trip | FR-034 |
| `POST /__lyrebird/auth/token` | `{ client_key }` | issue JWT (auth-enabled only) | FR-031 |
| `GET /__lyrebird/healthz` `GET /__lyrebird/readyz` | — | liveness/readiness (never authed) | FR-034 |

Ports: data-plane proxy listener(s) separate from the control-plane listener (MCP HTTP + this REST).
