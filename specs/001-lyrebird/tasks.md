---
description: "Task list for Lyrebird implementation"
---

# Tasks: Lyrebird — Agent-Driven Mock & Spy-Proxy Server

**Input**: Design documents from `/specs/001-lyrebird/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: REQUIRED. The constitution (Principle IV — Clean Architecture & Test-First / BDD) makes
BDD feature tests mandatory. Each user story's tests are authored and MUST FAIL before its
implementation begins.

**Organization**: Grouped by user story (US1–US7) so each is independently implementable, testable,
and shippable. Story→milestone mapping: US1=M1, US2=M2, US3=M3, US4=M4, US5+US6 cross-cut M1–M3,
US7=M6, recipe library=M5. Setup+Foundational = M0.

## Format: `[ID] [P?] [Story] Description`

- **[P]** = can run in parallel (different files, no dependencies)
- **[Story]** = US1..US7, or SETUP/FOUND/POLISH
- File paths follow the plan's clean-architecture layout.

---

## Phase 1: Setup (Shared Infrastructure) — M0

- [x] T001 [SETUP] Initialize Go module `github.com/brienze1/lyrebird` (go 1.25+) and create the
  clean-arch directory skeleton (`cmd/lyrebird`, `internal/domain`, `internal/usecase`,
  `internal/adapters/{mcp,httpadmin,proxy,matcher,scripting,template,examples}`,
  `internal/infra/{store,seeds,crypto,auth,gc}`, `test/{features,support}`).
- [x] T002 [P] [SETUP] Add base dependencies to go.mod: goja, modernc.org/sqlite, golang-jwt/v5,
  yaml.v3, godog, official Go MCP SDK; run `go mod tidy`.
- [x] T003 [P] [SETUP] Configure `golangci-lint` (`.golangci.yml`), `go vet`, and `gofmt` settings.
- [x] T004 [P] [SETUP] Multi-stage `Dockerfile` (`CGO_ENABLED=0` build → `scratch`/distroless),
  `.dockerignore`, and `docker-compose.yml` local example exposing `:8080` (data) + `:9090` (control).
- [x] T005 [P] [SETUP] `.github/workflows/ci.yml` — PR gate: `go vet`, `golangci-lint`, `go test`
  (incl. godog), and a `docker build` (no push).
- [x] T006 [P] [SETUP] `.github/workflows/release.yml` — on push to `main`/`v*` tags: Buildx
  multi-arch (amd64+arm64) build + push to GHCR with metadata-action tags + GHA cache;
  `permissions: { contents: read, packages: write }`.
- [x] T007 [P] [SETUP] godog test harness under `test/support` (runner wiring + empty step registry)
  and a `README` note on running the BDD suite.

**Checkpoint**: `go build ./...` succeeds; empty CI is green; image builds.

---

## Phase 2: Foundational (Blocking Prerequisites) — M0

**⚠️ No user-story work may begin until this phase is complete.**

- [x] T008 [FOUND] Define dependency-free domain entities in `internal/domain/`: `Partition`,
  `Upstream`, `Mock`, `Match`, `Matcher`, `Action` (respond/proxy/fault), `Script`, `Scenario`,
  `TrafficRecord` per data-model.md (no imports from adapters/infra).
- [x] T009 [FOUND] Define repository/port interfaces in `internal/usecase/ports.go`
  (MockRepo, TrafficRepo, PartitionRepo, UpstreamRepo, ScenarioStateRepo, Clock, IDGen).
- [x] T010 [FOUND] Env/config loader in `cmd/lyrebird` + `internal/infra/config` (`LYREBIRD_*`:
  TRAFFIC_TTL, DEFAULT_SPACE, ALLOW_PROXY_HOSTS, AUTH_KEYS, TOKEN_TTL, DATA_KEY, ports, body cap).
- [x] T011 [FOUND] At-rest crypto in `internal/infra/crypto`: AES-256-GCM seal/open with per-record
  nonce; key = random-at-startup or base64 `LYREBIRD_DATA_KEY`; unit tests for round-trip + wrong-key.
- [x] T012 [FOUND] SQLite store in `internal/infra/store` (modernc.org/sqlite): schema/migrations for
  `partitions`, `upstreams`, `ephemeral_mocks`, `traffic`, `scenario_state`; encrypt 🔒 payload
  columns via T011; plaintext index columns (partition/method/host/path/status/timestamp).
- [x] T013 [P] [FOUND] Structured logging + error-handling infra that NEVER logs secret material
  (tokens/keys/client secrets) — Principle V.
- [x] T014 [FOUND] Partition-resolution middleware (`X-Lyrebird-Space` → default) shared by data and
  control planes.
- [x] T015 [FOUND] Seed loader in `internal/infra/seeds`: read `/config/*.yaml` at boot into in-memory
  seeded mocks/partitions/upstreams (protected from reset/GC); duplicate-id = startup error.
- [x] T016 [FOUND] GC loop in `internal/infra/gc`: prune traffic older than `LYREBIRD_TRAFFIC_TTL`
  and expired ephemeral mocks on an interval.
- [x] T017 [FOUND] Wire `cmd/lyrebird` main: start data-plane listener(s) + control-plane listener,
  load seeds, start GC; `/__lyrebird/healthz` + `/readyz` (never authed).
- [x] T066 [FOUND] Disposability test (write first, must fail): `test/features/disposability.feature`
  — booting against an empty DB, a wiped DB, or one written with a different at-rest key MUST start
  healthy and treat prior data as absent (never as corruption); seeded mocks still load from
  `/config`. Implement graceful-open behavior in the store to satisfy FR-029.

**Checkpoint**: server boots, health responds, seeds load, GC runs, DB round-trips encrypted rows,
and a wiped/undecryptable DB starts clean (T066).

---

## Phase 3: User Story 1 — Record & passthrough (spy) (P1) 🎯 MVP — M1

**Goal**: Point a client at Lyrebird; unmatched calls are recorded and forwarded verbatim; recordings
hold full request + full upstream response. **Independent test**: quickstart Scenario A.

### Tests (write first, must fail)

- [x] T018 [P] [US1] `test/features/spy_record.feature` covering FR-001/002/003 + SC-001 and the
  edge cases: upstream 5xx verbatim, upstream-unreachable → 502/504, no-upstream → not_configured,
  large body streamed but recording truncated at cap.
- [x] T019 [P] [US1] In-memory upstream test double + step defs in `test/support`.

### Implementation

- [x] T020 [US1] `RecordTraffic` use-case in `internal/usecase` (persist request; truncate recorded
  body above cap with marker; stream body through unbounded).
- [x] T021 [US1] Upstream resolution + reverse-proxy passthrough in `internal/adapters/proxy`
  (`httputil.ReverseProxy`): forward, capture full real response, verbatim return; synthesize 502/504
  on transport failure; not_configured path.
- [x] T022 [US1] `DecideMockOrProxy` skeleton (no mocks yet → always spy) wiring proxy + RecordTraffic
  in the data-plane handler.
- [x] T023 [US1] `set_upstream`/`list_upstreams` (usecase + Admin REST `/__lyrebird/upstreams`).
- [x] T024 [US1] Traffic read path: `list_traffic` + `get_traffic` (decrypt) over Admin REST.

**Checkpoint**: spy works end-to-end; T018 passes; a recording proxy you can point anything at.

---

## Phase 4: User Story 2 — Override selected calls with mocks (P1) — M2

**Goal**: Declarative mocks intercept matching calls; the rest spy through; many mocks/route resolved
by priority (ties → newest wins). **Independent test**: quickstart Scenario B.

### Tests (write first, must fail)

- [x] T025 [P] [US2] `test/features/mock_override.feature`: FR-007/008/009/009a/010/013 + SC-003
  (mock fires, upstream sees zero calls), priority ordering, tie-break newest-wins, validation-as-rule.

### Implementation

- [x] T026 [P] [US2] Declarative matcher in `internal/adapters/matcher` (method/path exact|glob|regex/
  headers/query/body-JSONPath; AND semantics).
- [x] T027 [P] [US2] Response templating in `internal/adapters/template` (inject request values).
- [x] T028 [US2] `MatchRequest` use-case: select candidates by partition, order priority desc →
  created_at desc → id; first match wins (FR-009a).
- [x] T029 [US2] Extend `DecideMockOrProxy`: matched respond/fault → build+return; else spy (US1).
- [x] T030 [US2] Mock CRUD use-cases + Admin REST (`/__lyrebird/mocks`), seeded vs ephemeral + TTL
  field; persist ephemeral to SQLite.
- [x] T031 [US2] `match_test` use-case + Admin REST endpoint (per-condition pass/fail, resulting
  response, no forwarding) — FR-011.

**Checkpoint**: mock-some/passthrough-rest works; T025 passes.

---

## Phase 5: User Story 3 — Manage everything as an agent over MCP (P1) — M3

**Goal**: Full control plane over MCP with model-oriented descriptions, dry-run, inspection, and
capture→mock. **Independent test**: quickstart Scenario C.

### Tests (write first, must fail)

- [x] T032 [P] [US3] `test/features/mcp_control.feature`: guide returns a usable example; create→
  match_test→fire in ≤5 ops (SC-002); promote_traffic reproduces recording with full fidelity (SC-005);
  invalid request → explanatory error (FR-020).

### Implementation

- [x] T033 [US3] MCP server adapter in `internal/adapters/mcp` (Streamable HTTP + stdio) wired over
  the SAME use-cases as REST (no duplicated logic — Principle II).
- [x] T034 [P] [US3] MCP mock/upstream tools: `create_mock`,`get_mock`,`list_mocks`,`update_mock`,
  `delete_mock`,`reset`,`match_test`,`set_upstream`,`list_upstreams` with rich descriptions + examples.
- [x] T035 [P] [US3] MCP spy/metrics tools: `list_traffic`,`get_traffic`,`inspect_requests`,`metrics`,
  `clear_traffic` (FR-021) + `metrics` aggregation use-case.
- [x] T036 [US3] `promote_traffic` use-case + MCP/REST (recorded interaction → persistent mock) — FR-012.
- [x] T037 [P] [US3] `lyrebird_guide` + `script_sandbox_api` content tools (FR-017/019).
- [x] T038 [US3] Explanatory-error formatter used across MCP + REST adapters (FR-020).

**Checkpoint**: an agent can drive 100% of management over MCP; T032 passes. Agent-first story done.

---

## Phase 6: User Story 4 — Advanced logic via scripting (P2) — M4

**Goal**: Sandboxed JS `match`/`respond` hooks; safe, bounded, documented. **Independent test**:
quickstart Scenario D.

### Tests (write first, must fail)

- [x] T039 [P] [US4] `test/features/scripting.feature`: branch on body field (FR-014); no fs/net/env
  access (FR-015); infinite-loop/error → fail safe + recorded, server survives (FR-016, SC-010).

### Implementation

- [x] T040 [US4] goja VM pool in `internal/adapters/scripting` with interrupt timeout + memory guard.
- [x] T041 [P] [US4] Injected sandbox API (`req`, `uuid()`, `now()`, `faker`, `jsonpath()`) — nothing
  else exposed.
- [x] T042 [US4] Integrate script `match`/`respond` into `MatchRequest`/response build; record script
  failures as traffic; extend `create_mock`/`update_mock` to accept `script`.

**Checkpoint**: scripted mocks work and fail safe; T039 passes.

---

## Phase 7: User Story 5 — Multi-tenant isolation (P2)

**Goal**: Partitions isolate mocks/traffic/upstreams; cascade delete; default protected.
**Independent test**: quickstart Scenario E.

### Tests (write first, must fail)

- [x] T043 [P] [US5] `test/features/partitions.feature`: contradictory same-route mocks per space each
  return correctly (SC-004); no cross-space leakage; delete cascades; `default` non-deletable (FR-024).

### Implementation

- [x] T044 [US5] Enforce partition scoping in all repo queries (mocks/traffic/upstreams/scenario).
- [x] T045 [US5] Partition use-cases + MCP/REST: `create_space`,`list_spaces`,`delete_space` (cascade
  ephemeral mocks+traffic+upstreams; refuse `default`).

**Checkpoint**: concurrent agents are isolated; T043 passes.

---

## Phase 8: User Story 6 — Lifetimes & bounded storage (P2)

**Goal**: Seeded mocks survive reset; ephemeral TTL auto-clean; traffic bounded by retention.
**Independent test**: quickstart Scenario F.

### Tests (write first, must fail)

- [x] T046 [P] [US6] `test/features/lifetimes.feature`: seeded survives reset (FR-025/028); ephemeral
  TTL removed (FR-026); traffic older than window purged within one GC cycle (FR-027, SC-006).

### Implementation

- [x] T047 [US6] `reset` use-case (remove ephemeral mocks; optional clear_traffic; preserve seeded).
- [x] T048 [US6] Enforce TTL expiry + retention purge in the GC loop (T016) with configurable window;
  metrics/log of purged counts.

**Checkpoint**: storage stays bounded; seeded fixtures durable; T046 passes.

---

## Phase 9: User Story 7 — Advanced proxy & fault injection (P3) — M6

**Goal**: Request rewrite, response transform, latency/fault injection, allow/deny, transparent
forward-proxy (MITM), and opt-in scenarios. **Independent test**: quickstart Scenario (US7 flows).

### Tests (write first, must fail)

- [x] T049 [P] [US7] `test/features/advanced_proxy.feature`: rewrite (FR-004), fault/latency (FR-005),
  allow/deny blocked + recorded (FR-006), scenario sequential responses.

### Implementation

- [x] T050 [P] [US7] Request rewrite + response transform (declarative + JS) in the proxy path.
- [x] T051 [P] [US7] Fault/latency injection actions (delay/reset/timeout/malformed).
- [x] T052 [P] [US7] Proxy allow/deny host policy (`LYREBIRD_ALLOW_PROXY_HOSTS`) + record outcome.
- [x] T053 [US7] Scenario sequential responses + `scenario_state` (reset on reset).
- [x] T054 [US7] Transparent forward-proxy / MITM: on-the-fly cert signing from a Lyrebird CA;
  destination derived from CONNECT/Host; same record+decide pipeline.
- [x] T067 [US7] MITM CA key lifecycle (per data-model.md): generate a disposable CA at startup by
  default OR load a stable CA from mounted files/env; expose `ca_cert` (public) for clients to trust;
  store `ca_key` encrypted at rest (reuse `internal/infra/crypto`), NEVER log/export it; mint + cache
  per-host leaf certs in memory only. Add a test asserting `ca_key` is never returned by any endpoint
  or written to logs (FR-033).

**Checkpoint**: best-in-class proxy features incl. safe MITM CA handling; T049 + T067 pass.

---

## Phase 10: Cross-Cutting Security (control-plane auth)

### Tests (write first, must fail)

- [x] T055 [P] [SEC] `test/features/auth.feature`: no `LYREBIRD_AUTH_KEYS` → control plane open;
  with keys → unauth control-plane calls rejected, data plane still open (SC-007); token endpoint
  issues 1h JWT; expired/missing token rejected with non-sensitive error (FR-030/031).

### Implementation

- [x] T056 [SEC] JWT issue/verify in `internal/infra/auth` (HS256, `LYREBIRD_TOKEN_TTL` default 1h);
  `/__lyrebird/auth/token` endpoint (no separate MCP tool — since `/mcp` shares the same path-gated
  control-plane mux as REST, a caller obtains one token via REST and reuses it as a Bearer header for
  MCP calls too; see contracts/mcp-tools.md); secrets never logged/persisted (FR-033).
- [x] T057 [SEC] Control-plane auth middleware (guards MCP + Admin REST except token + health);
  data-plane listeners explicitly exempt (FR-030).

**Checkpoint**: one env var hardens the shared deployment; T055 passes.

---

## Phase 11: Recipe Library — M5

- [x] T058 [P] [POLISH] Embed recipe content in `internal/adapters/examples` (AWS SNS query→XML, SQS,
  DynamoDB `X-Amz-Target`, Secrets Manager, S3 REST-XML; GCP Pub/Sub, GCS, KMS; endpoint-injection
  how-to). Content only — no engine branches (Principle I).
- [x] T059 [POLISH] `list_examples`/`get_example` MCP + REST returning ready-to-adapt `create_mock`
  payloads (FR-022).
- [x] T060 [POLISH] End-to-end integration test: a real AWS SDK client pointed at Lyrebird via
  `AWS_ENDPOINT_URL` gets a mock response shaped by a recipe.

---

## Phase 12: Polish & Cross-Cutting

- [ ] T061 [P] [POLISH] `POST /__lyrebird/import` + `GET /__lyrebird/export` (YAML round-trip, seed
  schema) — FR-034.
- [ ] T062 [P] [POLISH] Performance test proving SC-009 (p95 < 10ms added overhead @ 100 concurrent).
- [ ] T063 [P] [POLISH] Update `docs/DESIGN.md` cross-links + author `README.md` (run, MCP connect,
  env vars, GHCR pull).
- [ ] T064 [POLISH] Run full `quickstart.md` validation (Scenarios A–H) against the built image.
- [ ] T065 [POLISH] Security hardening pass: confirm no secrets in logs, script sandbox escape tests,
  body-cap enforcement, CA key handling for MITM.

---

## Dependencies & Execution Order

- **Setup (P1)** → **Foundational (P2)** blocks everything.
- **US1 (P3)** is the MVP and precedes US2 (US2's `DecideMockOrProxy` extends US1's spy path).
- **US3 (P5)** depends on US1+US2 use-cases existing (it exposes them over MCP).
- **US4/US5/US6** depend on Foundational + the mock/traffic use-cases (US2/US3); can proceed in
  parallel by different developers after US3.
- **US7 (P9)** and **Security (P10)** depend on the proxy + control-plane being in place.
- **Recipe library (P11)** depends only on `create_mock` + MCP (after US3) — can slot in early.
- **Polish (P12)** last.

### Within each story
Tests authored and FAILING first (Principle IV) → domain/models → matcher/template/store →
use-cases → adapters (MCP/REST/proxy) → integration. Commit after each task or logical group.

### Parallel opportunities
- Setup T002–T007 all [P]. Foundational T013 [P] alongside store work.
- Within a story, [P] tasks touch different files (e.g., matcher vs template; MCP tool groups).
- After US3, US4/US5/US6 and the recipe library can be built concurrently.

## Implementation Strategy

- **MVP** = Phases 1–3 (Setup + Foundational + US1): a spy-recording proxy. Ship it.
- **Incremental**: add US2 (mock override) → US3 (MCP) → US4 (scripting). Each is an independently
  shippable image published automatically on merge (Principle VI).
- Milestone mapping: M0=Ph1–2, M1=US1, M2=US2, M3=US3, M4=US4, M5=recipes, M6=US7; US5/US6 fold in
  across M1–M3.

## Notes
- [P] = different files, no dependencies. [Story] gives traceability to spec.md user stories.
- Every story is independently completable and testable; verify tests fail before implementing.
- No per-service code, no UI, no data-plane auth — enforced by the constitution at every step.
