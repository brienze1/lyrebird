# Phase 0 Research — Lyrebird

All spec-level `[NEEDS CLARIFICATION]` items were resolved in the Clarifications session (2026-07-03).
This document records the technology decisions that back the plan, each with rationale and rejected
alternatives.

## R1. Language & architecture

- **Decision**: Go, clean architecture (dependency-free domain → use-cases → adapters), static
  `CGO_ENABLED=0` binary.
- **Rationale**: Matches the team's ecosystem and Go clean-arch agents; stdlib `net/http` +
  `httputil.ReverseProxy` make the proxy nearly free; a single static binary is the ideal
  sidecar/CI/HML artifact.
- **Alternatives**: Node/TS (best MCP SDK, weaker as a TLS proxy, heavier runtime); Rust (top perf,
  slower to build, less mature MCP tooling). Rejected for fit and delivery simplicity.

## R2. Embedded scripting engine

- **Decision**: `github.com/dop251/goja` (pure-Go ES5.1+ JavaScript). Mocks expose optional
  `match(req)->bool` and `respond(req)->response` hooks. A VM pool avoids per-request construction;
  each execution runs under an interrupt-based timeout and a memory guard.
- **Rationale**: AI authors JavaScript most reliably; pure-Go keeps the static build; sandbox is
  closed by default (no fs/net/env unless we inject a helper). One scripting language only.
- **Alternatives**: CEL (expressions only — insufficient for response building); Lua/Starlark (less
  natural for LLMs, Starlark can't easily build arbitrary HTTP responses); embedded V8 (CGO,
  bloats image). Rejected. CEL may return later purely as a fast matcher optimization.

## R3. Disposable persistence

- **Decision**: `modernc.org/sqlite` (pure-Go, no CGO). Stores `traffic`, `ephemeral_mocks`,
  `partitions`, `upstreams`, `scenario_state`. Seeded mocks live in memory (from `/config`).
- **Rationale**: Pure-Go preserves the static/tiny image; SQLite gives queryable traffic + bounded
  storage; disposability is acceptable per Principle III.
- **Alternatives**: `mattn/go-sqlite3` (CGO — breaks static build); pure in-memory (loses queryable
  traffic + restart-durable option); external DB (violates disposability/simplicity). Rejected.

## R4. Encryption at rest

- **Decision**: App-level AES-256-GCM (per-record random nonce) on sensitive payload columns
  (request/response bodies, headers, mock response bodies, scripts). Key = 32 random bytes at startup
  by default; `LYREBIRD_DATA_KEY` (base64) supplies a stable key for restart-durable volumes. Routing
  metadata (partition, method, path, host, status, timestamps) stays plaintext for filtering/GC.
- **Rationale**: SQLCipher would require CGO. App-level AEAD keeps the pure-Go static build; the
  ephemeral-key default means a leaked DB file is unreadable and matches disposability.
- **Alternatives**: SQLCipher (CGO); full-file encryption (breaks queryability); no encryption
  (fails Principle V). Rejected.

## R5. Proxy interception strategy

- **Decision**: Reverse-proxy / endpoint-override first (clients point at Lyrebird via
  `AWS_ENDPOINT_URL`, GCP emulator host vars, or base-URL; upstream resolved from `set_upstream`).
  Transparent forward-proxy with a Lyrebird CA (MITM) added later (M6) for zero-code-change intercept.
- **Rationale**: Reverse mode is deterministic and needs no TLS tricks — the fastest path to a
  working spy. Forward-proxy is the premium zero-config spy but needs CA trust; deferring it keeps
  early milestones simple. Both share one proxy engine.
- **Alternatives**: MITM-only (more operational friction up front, harder determinism). Rejected as
  the default.

## R6. Upstream error & body handling (from clarifications)

- **Decision**: Pass upstream responses (incl. 4xx/5xx) through verbatim; synthesize 502/504 only on
  unreachable/timeout. Stream bodies of any size; truncate only the *recording* above a configurable
  cap (default 1 MB) with a marker.
- **Rationale**: Maximum spy fidelity while bounding storage and memory. Matches SC-001/SC-006.
- **Alternatives**: Always-synthesize errors (loses fidelity); hard body cap at the data plane
  (breaks large-payload calls); unbounded recording (storage blowup). Rejected.

## R7. MCP transport & tool ergonomics

- **Decision**: Official Go MCP SDK over Streamable HTTP (remote HML) + stdio (local). Every tool
  carries a model-oriented description with a minimal valid example; errors are explanatory. Provide
  agent-superpower tools `match_test` and `inspect_requests`, plus `promote_traffic`.
- **Rationale**: Streamable HTTP is the current remote transport, letting a deployed HML instance
  serve remote agents; rich descriptions + dry-run tools let an LLM self-correct (SC-002).
- **Alternatives**: stdio-only (not remotely reachable); bespoke JSON-RPC (reinvents MCP). Rejected.

## R8. Multi-tenancy (partitions / "spaces")

- **Decision**: Partition selected by an `X-Lyrebird-Space` request header (default partition when
  absent). Every SQLite row carries a `partition` column; seeded mocks may declare a partition.
  Deleting a partition cascades its ephemeral mocks/traffic/upstreams; the default partition is
  protected. Equal-priority mock ties resolve most-recently-created-wins.
- **Rationale**: Cheapest isolation that makes a shared HML deployment safe for concurrent agents
  (SC-004); invisible for single-user local use.
- **Alternatives**: One instance per agent (heavier ops); no isolation (concurrent corruption).
  Rejected.

## R9. Control-plane auth

- **Decision**: Off unless `LYREBIRD_AUTH_KEYS` is set. When set, a token endpoint mints short-lived
  JWTs (HS256, default 1h TTL via `LYREBIRD_TOKEN_TTL`); all control-plane calls require
  `Authorization: Bearer`. The data plane is never authenticated. Secrets never logged.
- **Rationale**: Frictionless-by-default, one-env-var hardening (Principle V, SC-007); JWT keeps
  verification stateless and disposable.
- **Alternatives**: Always-on auth (hurts local adoption); API-key-per-call without expiry (weaker).
  Rejected.

## R10. Delivery & CI/CD

- **Decision**: Multi-stage Dockerfile → `scratch`/distroless static image. `ci.yml` gates PRs (vet,
  lint, `go test` incl. godog, `docker build`); `release.yml` builds multi-arch (amd64+arm64) and
  pushes to GHCR on merge to `main` and on `v*` tags; auth via built-in `GITHUB_TOKEN`.
- **Rationale**: Public auto-published image is a product requirement (Principle VI, SC-008); GHCR
  needs no extra secrets for a public repo.
- **Alternatives**: Docker Hub (extra account/secrets, pull limits); manual release (violates
  "auto-publish on merge"). Rejected.
