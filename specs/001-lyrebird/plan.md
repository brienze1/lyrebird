# Implementation Plan: Lyrebird — Agent-Driven Mock & Spy-Proxy Server

**Branch**: `001-lyrebird` (working on `main`) | **Date**: 2026-07-03 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/001-lyrebird/spec.md`

## Summary

Lyrebird is a generic HTTP(S) mock and spy-proxy server driven entirely over MCP and delivered as a
public, static Docker image. It records all traffic and passes unmatched calls through to the real
upstream by default (spy mode); matching mocks override passthrough. It is generic (no per-service
code), disposable (embedded SQLite + retention GC), multi-tenant (partitions/"spaces"), scriptable
(sandboxed JavaScript), and secure-by-default-when-configured (control-plane auth off unless env is
set; encryption at rest on by default). Technical approach: Go clean architecture with a
dependency-free domain and use-case core; MCP, Admin REST, and the proxy engine are adapters over
that core; persistence is pure-Go SQLite so the binary stays static (`CGO_ENABLED=0`).

## Technical Context

**Language/Version**: Go 1.26 (toolchain present; target `go 1.25`+ for module compatibility)

**Primary Dependencies**:
- MCP server SDK (official Go MCP SDK) — Streamable HTTP + stdio transports
- `net/http` + `net/http/httputil.ReverseProxy` — proxy core (stdlib)
- `github.com/dop251/goja` — embedded, sandboxed JavaScript (pure-Go, no CGO)
- `modernc.org/sqlite` — pure-Go SQLite driver (no CGO) for disposable state
- `github.com/golang-jwt/jwt/v5` — short-lived control-plane tokens (only when auth enabled)
- `crypto/aes` + `crypto/cipher` (stdlib) — AES-256-GCM at-rest encryption
- YAML parser (`gopkg.in/yaml.v3`) — seed config loading
- BDD: `github.com/cucumber/godog` — feature files (Principle IV)

**Storage**: Embedded SQLite (pure-Go). Tables: `traffic`, `ephemeral_mocks`, `partitions`,
`upstreams`, `scenario_state`. Sensitive payload columns encrypted at rest (AES-256-GCM). Seeded
mocks are held in memory from mounted `/config` YAML (not in SQLite).

**Testing**: `go test` + `godog` BDD feature files (contract/integration/unit); a live-upstream test
double for spy/proxy scenarios; a Docker build step in CI.

**Target Platform**: Linux container (multi-arch linux/amd64 + linux/arm64), static binary on
`scratch`/distroless. Runs locally or as a long-lived HML service.

**Project Type**: Single Go service (network daemon) with two control-plane adapters (MCP, REST) and
a proxy data plane.

**Performance Goals**: Proxy passthrough overhead p95 < 10 ms at 100 concurrent in-flight requests
(SC-009). Bodies stream through unbounded; recordings truncate above a configurable cap (default 1 MB).

**Constraints**: `CGO_ENABLED=0` static build (pure-Go deps only, incl. SQLite); tiny image
(single-digit MB target); data plane never authenticated; encryption at rest on by default;
sandboxed scripts with execution timeout + memory guard.

**Scale/Scope**: Dev machines and shared non-production (HML) environments; tens of concurrent
agents across partitions; disposable state bounded by retention GC.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

| # | Principle | Plan compliance | Status |
|---|-----------|-----------------|--------|
| I | Generic-First — No Per-Service Code | One generic match/respond/proxy engine; AWS/GCP support is an MCP-served recipe library (content only), never engine branches. | ✅ PASS |
| II | Agent-First Control Plane (MCP) | MCP is primary; Admin REST is a thin twin over the same use-cases; no UI; model-oriented tool descriptions + explanatory errors. | ✅ PASS |
| III | Spy by Default, Disposable by Design | Spy passthrough + full req/resp recording by default; SQLite state disposable; retention GC; no stateful business emulation. | ✅ PASS |
| IV | Clean Architecture & Test-First (BDD) | Dependency-free domain, use-case layer, inward-only adapters; godog feature files authored failing before implementation. | ✅ PASS |
| V | Secure & Frictionless Defaults | Data plane never authed; control-plane auth env-activated (1h tokens); AES-256-GCM at rest on by default; no secrets in logs. | ✅ PASS |
| VI | Ship Continuously | Static multi-arch image; CI PR gate + auto-publish to GHCR on merge to main from M0. | ✅ PASS |

**Result**: PASS — no violations. Complexity Tracking below is empty (nothing to justify).

## Project Structure

### Documentation (this feature)

```text
specs/001-lyrebird/
├── plan.md              # This file
├── research.md          # Phase 0 output — decisions, rationale, alternatives
├── data-model.md        # Phase 1 output — entities, fields, relationships
├── quickstart.md        # Phase 1 output — runnable validation scenarios
├── contracts/           # Phase 1 output — MCP tools, Admin REST, config & data-plane contracts
│   ├── mcp-tools.md
│   ├── admin-rest.md
│   ├── seed-config.md
│   └── data-plane.md
└── tasks.md             # Phase 2 output (/speckit-tasks — not created by /speckit-plan)
```

### Source Code (repository root)

```text
cmd/lyrebird/                 # main: config load, wire listeners (proxy + control plane), start GC
internal/domain/             # entities: Partition, Upstream, Mock, Match, Action, Scenario, Traffic (no deps)
internal/usecase/            # CreateMock, MatchRequest, DecideMockOrProxy, RenderResponse,
                             #   RecordTraffic, PromoteTraffic, ManagePartition, RunGC, LoadSeeds, IssueToken
internal/adapters/
  ├── mcp/                   # MCP tools (Streamable HTTP + stdio) — thin over usecases
  ├── httpadmin/             # Admin REST handlers (thin twin)
  ├── proxy/                 # reverse-proxy engine (M1/M2) + forward-proxy/MITM (M6)
  ├── matcher/               # declarative matching (method/path/header/query/body)
  ├── scripting/             # goja VM pool + injected sandbox API (req, uuid, now, faker, jsonpath)
  ├── template/              # response templating
  └── examples/              # embedded recipe library (markdown/JSON) served over MCP — content only
internal/infra/
  ├── store/                 # SQLite (modernc) repositories + AES-GCM at-rest crypto
  ├── seeds/                 # YAML config loader for seeded mocks/partitions
  ├── crypto/                # at-rest key mgmt (startup random or LYREBIRD_DATA_KEY)
  ├── auth/                  # JWT issue/verify (only active when LYREBIRD_AUTH_KEYS set)
  └── gc/                    # retention sweeper (traffic + expired ephemeral mocks)
test/
  ├── features/              # godog .feature files (one per user story)
  └── support/               # step defs, in-memory upstream test double
Dockerfile                   # multi-stage, CGO_ENABLED=0 → scratch/distroless
.github/workflows/ci.yml     # PR gate: vet + lint + test + docker build
.github/workflows/release.yml# push to main / tags: multi-arch build + push to GHCR
docker-compose.yml           # local example
```

**Structure Decision**: Single Go service in clean-architecture layers. The domain has no imports
from adapters/infra; use-cases orchestrate domain + repository interfaces; MCP, REST, and proxy are
adapters. This satisfies Principle IV and keeps the three interfaces (MCP/REST/proxy) sharing one
use-case layer with zero duplicated logic (Principle II).

## Phase 0 — Research

See [research.md](./research.md). All spec clarifications are resolved; research records the key
technology decisions (goja, modernc SQLite, reverse+MITM proxy staging, app-level AES-GCM, MCP
Streamable HTTP, header-based partitions, JWT auth) with rationale and rejected alternatives. No
`NEEDS CLARIFICATION` items remain.

## Phase 1 — Design & Contracts

- [data-model.md](./data-model.md) — entities, fields, relationships, validation, lifecycle.
- [contracts/](./contracts/) — MCP tool contracts, Admin REST endpoints, seed-config schema, and the
  data-plane (proxy) request-lifecycle contract.
- [quickstart.md](./quickstart.md) — runnable validation scenarios mapping to the user stories.

Agent-context update step: the optional `update-agent-context` script is not bundled in this Spec Kit
install; skipped. Project guidance lives in the constitution and `docs/DESIGN.md`.

**Post-design Constitution re-check**: PASS — the data model and contracts introduce no per-service
code, keep MCP as the primary control plane, and preserve disposability and security defaults.

## Complexity Tracking

> No constitution violations. No entries required.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| (none)    | —          | —                                   |
