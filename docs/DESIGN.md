# Lyrebird — Design & Plan

> A lyrebird mimics any sound it hears. **Lyrebird** mimics any HTTP(S) API — built to be driven
> by AI agents, deployed as a Docker image, run locally or as a shared HML service. One tool to
> rule them all.

Status: **Draft plan for review.** Nothing built yet. Decisions in §2 reflect your direction;
items marked *(proposed)* are my recommendation awaiting your nod.

---

## 1. Vision & positioning

Lyrebird is an **agent-first, spy-by-default, protocol-aware mock & proxy server**. An AI agent (or
CI job) points a dependency at lyrebird; by default lyrebird **records every call and transparently
passes it through to the real API** (spy mode), and the moment a mock rule exists for a call, it
**returns the mock instead**. Everything — creating rules, scripting responses, reading metrics,
promoting recorded traffic into permanent mocks — happens over an extensive **MCP** interface. No UI.

### The gap we fill

| Tool | Mock | Conditional proxy (spy) | Scripting | Cloud-aware | Agent-first (MCP) | Disposable state |
|------|:----:|:----:|:----:|:----:|:----:|:----:|
| WireMock | ✅ | ✅ | JVM | ❌ | ❌ | mixed |
| Mockoon | ✅ | partial | JS | ❌ | ❌ | ✅ |
| Hoverfly | ✅ | ✅ (SPY) | middleware | ❌ | ❌ | ✅ |
| LocalStack | emulates | ❌ | ❌ | full emulation | ❌ | ❌ stateful |
| **Lyrebird** | ✅ | ✅ **default** | ✅ **JS (goja)** | ✅ generic | ✅ **MCP** | ✅ **SQLite, ephemeral** |

Hoverfly proves the spy model; nothing combines spy-default + JS scripting + cloud wire-format
awareness + a first-class MCP control plane + recorded-traffic metrics. That's lyrebird.

### Explicit non-goal

We do **not** re-implement AWS/GCP, and we do **not** ship per-service code. There is no "SNS
preset" or "Rekognition tool" to build or maintain. AWS/GCP SDKs are just HTTP underneath, so they
are mocked by the *same generic engine* as any other API. We help the AI with **examples/recipes in
the MCP** (knowledge, not code) that show each wire format. State emulation is LocalStack's
treadmill; we don't run it.

---

## 2. Decisions

**Confirmed with you:**
1. **Stack: Go.** Light, fast, single static binary, matches your ecosystem + Go clean-arch agents.
2. **Delivery: Docker image.** Run locally or deployed as a long-lived HML service, called whenever
   needed. Tiny static image (see §9).
3. **Spy mode is the default.** Unmatched calls are recorded **and** proxied to the real upstream;
   matched calls return the mock. Metrics (what was called, when, with what payload/response,
   latency) are always captured.
4. **Disposable SQLite state.** An embedded SQLite DB holds the spy traffic log + ephemeral mocks.
   If the container dies and loses it, that's fine — it's a mock. A background **GC** prunes old
   records within a configurable retention window.
5. **Two mock lifetimes.** *Seeded* mocks load from mounted config files at boot (always-up,
   survive reset/GC); *ephemeral* mocks are created at runtime via MCP (optional TTL, GC-able).
6. **No UI. Extensive MCP tooling** covers 100% of customization.

**Proposed (my recommendation — confirm):**
7. **Scripting: embedded JavaScript via [goja](https://github.com/dop251/goja)** — pure-Go ES5.1+,
   no CGO, sandboxed. Rules are declarative for the easy case; drop to a JS `match(req)` /
   `respond(req)` hook for advanced logic. Chosen over Lua/Starlark/CEL because **AI authors JS most
   reliably** and it needs no external runtime. One scripting language, not two.
8. **Multi-tenancy via "spaces"** — a shared HML deployment is partitioned by an `X-Lyrebird-Space`
   header (or subpath) so parallel agents don't collide on one instance. Default space for simple
   local use.
9. **Interception: reverse-proxy + transparent forward-proxy (MITM), both first-class.** Reverse
   (endpoint-override) is the easy path; forward-proxy is the premium spy path (auto-knows the real
   upstream, zero client code change). See §5.
10. **Purely generic HTTP — no per-service code.** AWS/GCP/any SDK is mocked with the one generic
    engine. Cloud "awareness" ships as an **examples/recipe library surfaced over MCP** (wire-format
    tips + endpoint-injection how-to), never as service-specific mock tooling. Adding a new service
    to the docs = writing an example, not shipping a release.

---

## 3. Core concepts (domain model)

Everything is serializable JSON/YAML — round-trips cleanly over MCP, REST, and config files.

```
Space           Tenant partition. Isolates mocks + traffic + upstream config for one agent/session.
  ├── Upstream    Where "the real API" is, per route/host (needed for spy passthrough in reverse mode;
  │               auto-derived from the request in forward-proxy mode).
  ├── Mock        A named rule. lifetime = seeded (from config, protected) | ephemeral (runtime, TTL).
  │     ├── match      Declarative: method, path (glob/regex), headers, query, body(JSONPath).
  │     ├── script     Optional JS: match(req)->bool  and/or  respond(req)->response  (advanced).
  │     ├── action     respond | proxy | fault | capture.
  │     │     ├── respond  status, headers, body (templated or JS-built), latency.
  │     │     ├── proxy    forward to Upstream, optionally rewrite req/resp (see §5).
  │     │     └── fault    reset / timeout / malformed body (chaos testing).
  │     ├── priority   Ordering when multiple mocks could match.
  │     └── group      Optional label for bulk management (create/delete a set together).
  └── TrafficLog   Every call through this space (SQLite). For proxied/spy calls this stores the FULL
                   incoming request AND the FULL real upstream response (status, headers, body,
                   latency) — that recorded pair is what promote_traffic turns into a mock.
Scenario        Opt-in stateful-lite: Nth matching call returns response N (pagination, retry-then-ok).
```

**Mocks on the same route are an ordered array, not a conflict.** Many mocks can target the same
method+path; they're evaluated by `priority`, first match wins. So two agents mocking the same SNS
topic with different conditions simply coexist (e.g. a validation mock returning 4xx on a bad body,
then a happy-path mock). Spaces (above) handle only the rarer case of genuinely contradictory mocks
on the *same* route from concurrent agents.

### Request lifecycle (spy-by-default)

```
incoming ─► resolve Space ─► record request ─► evaluate Mocks by priority
                                                   │
                          first Mock where match (decl AND/OR script) is true
                                                   │
              ┌────────────────────────┬───────────┴───────────┐
        action=respond           action=proxy               no match
              │                        │                        │
     build (template|JS) + latency   forward to Upstream    SPY DEFAULT:
     record + return mock            record real resp,      proxy → Upstream,
                                     return it              record real resp, return it
                                                            (fallback if no upstream: 404)
```

The "if this flags, mock; else proxy" requirement falls out naturally: **the rule is the flag.**
Matched → mock. Unmatched → spy passthrough to the real API, recorded. Validation is a rule whose
action/JS returns 4xx on bad input.

---

## 4. Interfaces — MCP (primary) + Admin REST (thin twin)

Both are adapters over the same core. MCP is the headline; REST exists for curl/scripts/health.

### MCP tools (rich, LLM-oriented descriptions + JSON-schema args)

- `lyrebird_guide` — self-documenting overview: concepts, how to compose a mock, worked JS examples.
  (This is the "help MCP describing how to use the tools" you asked for.)
- **Mocks:** `list_mocks`, `get_mock`, `create_mock`, `update_mock`, `delete_mock`, `reset`
  (respects seeded-mock protection), with `lifetime` + optional `ttl`.
- **Spy / metrics:** `list_traffic` (filter by space/host/path/status/time), `get_traffic` (full
  payloads of one interaction), `metrics` (aggregate counts/latency by mock/path/status over a
  window), `clear_traffic`.
- **Capture → mock:** `promote_traffic` — turn a recorded real interaction into a persistent mock
  in one call. Record once, replay forever. *(agent superpower)*
- **Debugging:** `match_test` — dry-run a sample request → which mock fires, which matchers
  passed/failed, and the response. `inspect_requests` — recent requests, to see why a mock missed.
- **Scripting help:** `script_sandbox_api` — documents the JS globals available (`req`, helpers like
  `uuid()`, `now()`, `faker`, `jsonpath()`), so the agent writes valid scripts first try.
- **Recipes (knowledge, not code):** `list_examples`, `get_example` — a curated library teaching the
  AI how to mock common APIs/SDKs as plain HTTP: AWS (SNS query→XML, DynamoDB `X-Amz-Target` header,
  Secrets Manager, S3 REST-XML), GCP (Pub/Sub, GCS, KMS), plus how to point an SDK at lyrebird
  (`AWS_ENDPOINT_URL`, GCP emulator host vars). No per-service tooling — these produce ordinary
  `create_mock` calls.
- **Upstreams:** `set_upstream` (map a space/host to the real API for spy passthrough).
- **Spaces:** `create_space`, `list_spaces`, `delete_space`.

Design rule: **each description is written for a model, not a human** — include a minimal valid
payload, and make errors explanatory ("`body.foo` didn't match: request had no `foo`; keys=[...]").

### Admin REST (`/__lyrebird/...`)

CRUD mirror of the above + `POST /reset`, `GET /traffic`, `GET /metrics`, `POST /import|export`
(YAML bundle), `GET /healthz`, `GET /readyz`.

### Transport

MCP over **Streamable HTTP** (current remote transport) so a deployed HML instance is reachable by
remote agents; stdio also supported for local single-user runs.

---

## 5. The proxy (best-in-class, easy → advanced)

Spy passthrough is only the floor. The proxy engine supports, from simplest to most advanced:

- **Passthrough + record** (default spy) — forward unmatched calls, capture req+resp.
- **Upstream resolution:**
  - *Forward-proxy / MITM mode:* the real destination is in the request (CONNECT/Host), so
    passthrough is automatic — zero config, best spy UX. Needs the lyrebird CA trusted by clients.
  - *Reverse-proxy / endpoint-override mode:* client points at lyrebird (`AWS_ENDPOINT_URL`, GCP
    emulator host, or base-URL). We resolve the real upstream from `set_upstream` config — one small
    mapping per host/space.
- **Request rewrite** before forwarding — path/host/header/query/body edits (declarative or JS).
- **Response transform** after receiving — mutate status/headers/body (declarative or JS).
- **Latency & fault injection** on proxied or mocked responses (chaos: delay, reset, timeout, corrupt).
- **Selective spy** — allow/deny lists: which hosts may be proxied vs must be mocked (safety in HML).
- **TLS to upstream** with SNI; streaming/large bodies. (WebSockets/gRPC-web = later.)

This makes the proxy usable trivially (point-and-go spy) or deeply (full req/resp programmability).

---

## 6. State & persistence

- **Disposable by design.** SQLite (`modernc.org/sqlite`, pure-Go, no CGO → static image) stores:
  the **traffic log** (spy metrics + payloads) and **ephemeral mocks**. Losing it on crash is
  acceptable and expected.
- **Seeded mocks** come from mounted **config files** (`/config/*.yaml`) at boot — the "always-up"
  mocks you never want to lose. Protected from `reset`/GC/TTL. Version-controllable as fixtures.
- **Ephemeral mocks** are created at runtime via MCP, optionally with a **TTL** so an agent's
  throwaway fakes self-clean.
- **Garbage collector** — background sweeper prunes traffic rows older than a retention window
  (e.g. `LYREBIRD_TRAFFIC_TTL=24h`) and expired ephemeral mocks. Keeps the DB bounded.
- **Scenarios** — the only "lite" state (Nth-call responses); opt-in per mock, reset on `reset`.

---

## 7. Architecture (Go, clean architecture)

Layered to match your Go clean-arch conventions (so the Go build/review agents apply directly):

```
cmd/lyrebird/              main: wire spaces, proxy listeners, admin REST, MCP (HTTP+stdio)
internal/domain/           entities: Space, Mock, Rule, Action, Preset, Scenario, Traffic (no deps)
internal/usecase/          CreateMock, MatchRequest, DecideMockOrProxy, RenderResponse,
                           RecordTraffic, PromoteTraffic, RunGC, LoadSeeds
internal/adapters/
  ├── mcp/                 MCP tools (Streamable HTTP + stdio) — thin over usecases
  ├── http/admin/          Admin REST handlers
  ├── proxy/               reverse-proxy engine + forward-proxy/MITM (CA, cert-on-the-fly)
  ├── matcher/             declarative matching
  ├── scripting/           goja VM pool, injected sandbox API (req, uuid, now, faker, jsonpath)
  ├── template/            response templating
  └── examples/            embedded recipe library (markdown/JSON) served over MCP — docs, no logic
internal/infra/
  ├── store/sqlite/        traffic log + ephemeral mocks (modernc.org/sqlite)
  ├── seeds/               YAML config loader for seeded mocks
  └── gc/                  retention sweeper
test/features/             BDD feature files (matches your Tester/Reviewer agent workflow)
```

Core is dependency-free; proxy, admin, and MCP are three adapters over one usecase layer — no logic
duplicated. **goja VM pool** avoids per-request VM cost; scripts run with an interrupt timeout +
memory guard so a runaway/hostile script can't hang the server.

---

## 8. Roadmap (vertical slices — each independently useful)

- **M0 — Skeleton + Docker + CI/CD.** Go module, clean-arch layout, `/healthz`, SQLite store with
  **AES-256-GCM at-rest encryption** (§10b) + GC loop, multi-stage Dockerfile (scratch), BDD
  harness, **and both GitHub Actions workflows** (PR gate + auto-publish to GHCR on merge to main).
  CI/CD + at-rest encryption are live from commit #1 so every later milestone ships secured
  automatically. Control-plane **auth (§10a)** lands with the control plane in M3.
- **M1 — Spy core.** Reverse-proxy listener, record-all traffic to SQLite, `set_upstream`,
  passthrough. `list_traffic` / `metrics` over REST. → *A recording proxy you can point anything at.*
- **M2 — Mocks + match.** Declarative matchers + `respond` + templating + priority + seeded vs
  ephemeral (TTL). `match_test`. → *Spy-with-overrides: mock some, passthrough the rest.*
- **M3 — MCP control plane.** All tools + `lyrebird_guide`, rich LLM descriptions, `promote_traffic`
  (capture→mock). → *Agent-first story complete.*
- **M4 — JS scripting.** goja VM pool + sandbox API + `script_sandbox_api` tool; JS `match`/`respond`
  and request/response rewrite.
- **M5 — Recipe library + SDK wiring.** `list_examples`/`get_example` with curated recipes (AWS SNS/
  SQS/DynamoDB/Secrets Manager/S3, GCP Pub/Sub/GCS/KMS) and one end-to-end integration test proving
  a real SDK pointed at lyrebird via `AWS_ENDPOINT_URL` gets mocked correctly. Mostly docs + a test —
  small, since there's no per-service code.
- **M6 — Advanced proxy.** Forward-proxy/MITM (CA), req/resp rewrite, fault injection, allow/deny,
  scenarios.

Ship M1–M4 first — a spy-default, scriptable, agent-driven mock/proxy already beats everything out
there. M5 is now lightweight (knowledge, not code), so it can slot in early or alongside M3.

---

## 9. Docker & deployment

- **Multi-stage build**, `CGO_ENABLED=0` static binary (pure-Go SQLite), final stage `scratch` or
  distroless → image measured in single-digit MB.
- **Ports:** proxy listener(s) (e.g. `:8080`) + control plane (MCP HTTP + admin REST, e.g. `:9090`).
- **Volumes:** `/config` (mount seed YAMLs, read-only) and `/data` (SQLite, optional — fine to run
  fully ephemeral with no volume).
- **Config via env:** `LYREBIRD_TRAFFIC_TTL`, `LYREBIRD_DEFAULT_SPACE`, `LYREBIRD_ALLOW_PROXY_HOSTS`,
  `LYREBIRD_AUTH_KEYS` (presence enables control-plane auth), `LYREBIRD_TOKEN_TTL` (default `1h`),
  `LYREBIRD_DATA_KEY` (optional stable at-rest key), TLS/CA settings, etc. See §10.
- Ship a `docker-compose.yml` example (local) and a bare `docker run` one-liner; HML = same image as
  a long-lived service behind the NLB, agents connect to its MCP HTTP endpoint.

## 9a. CI/CD — public image, auto-published (GitHub Actions)

Public repo → **public image on GHCR** at `ghcr.io/brienze/lyrebird`, pullable by anyone, no login.
GHCR authenticates with the built-in `GITHUB_TOKEN` (no secrets to manage). Two workflows:

- **`.github/workflows/ci.yml`** — on pull requests + pushes: `go vet`, `golangci-lint`, `go test`
  (incl. BDD features), and a `docker build` (no push) to prove the image compiles. This is the
  merge gate.
- **`.github/workflows/release.yml`** — on push to `main` and on `v*` tags: build **multi-arch
  (linux/amd64 + linux/arm64)** with Buildx + QEMU and **push to GHCR**. Tags:
  - push to `main` → `:latest`, `:main`, `:sha-<short>`
  - tag `vX.Y.Z` → `:X.Y.Z`, `:X.Y`, `:X`, `:latest`
  Uses `docker/metadata-action` for tagging, `docker/build-push-action` with **GHA build cache**,
  `permissions: { contents: read, packages: write }`.

Notes: first publish requires flipping the GHCR package visibility to **public** once (repo/org
package settings) or setting it via the org default. Optionally add release automation
(`softprops/action-gh-release` or release-please) so `vX.Y.Z` tags cut GitHub Releases with notes.
So the flow you asked for is literally: **merge PR to `main` → `release.yml` rebuilds and republishes
`ghcr.io/brienze/lyrebird:latest` automatically.**

---

## 10. Security — auth & encryption at rest

Principle: **frictionless by default, hardened by setting env vars.** Same pattern for both features.

### 10a. Auth — off by default, env-activated (control plane only)

- **The data plane is never authed.** The mock/proxy listeners must stay open so the
  system-under-test can call mocks freely (it can't carry a lyrebird token). Auth *only* ever
  protects the **control plane**: MCP tools + admin REST.
- **Default: open.** No `LYREBIRD_AUTH_*` env → control plane is open (dev/local stays frictionless).
- **Set `LYREBIRD_AUTH_KEYS`** (one or more client secrets, comma-separated) → auth turns ON:
  - Auth endpoints appear: `POST /__lyrebird/auth/token` — present a valid client key, get back a
    short-lived **JWT (HS256, signed with a server key derived from the secret)**.
  - Token TTL defaults to **1h**, override with `LYREBIRD_TOKEN_TTL` (e.g. `15m`, `8h`).
  - All control-plane calls then require `Authorization: Bearer <jwt>` (exceptions: the token
    endpoint itself and `/healthz`/`/readyz`). For MCP-over-HTTP the bearer rides the transport header.
- Rationale: presence-activated, so flipping on hardening for a shared HML instance is a one-env-var
  change with no code/redeploy difference.

### 10b. Encryption at rest — on by default

- **All sensitive payloads in SQLite are encrypted with AES-256-GCM** before write and decrypted
  after read (app-level AEAD, per-record random nonce). App-level rather than SQLCipher so we stay
  **pure-Go / no-CGO** and keep the tiny static image.
- **Key: 32 random bytes from `crypto/rand` generated at startup** by default. The key lives only in
  memory → a leaked `.sqlite` file is unreadable, and post-restart old rows are naturally
  undecryptable — which is exactly right for disposable state.
- **Optional `LYREBIRD_DATA_KEY`** (base64, 32 bytes): supply a stable key to make a mounted `/data`
  volume survive restarts decryptably. Omit it → restart is a clean slate (fine for a mock).
- **Encrypted:** request/response bodies, headers, mock response bodies, JS scripts (the *content*).
  **Plaintext (needed for filtering/GC/indexing):** space, method, path, host, status, timestamps.
  Honest tradeoff — all payload content is encrypted; only minimal routing metadata stays in clear.
- Seeded mocks load from mounted config at boot (not secrets), so they're unaffected either way.

## 11. Remaining open questions

1. **Confirm the two *proposed* decisions** (§2.7 JavaScript/goja for scripting, §2.8 spaces model).
2. **Seed config format** — one YAML per mock, or a single bundle file? (Leaning: a `/config` dir of
   YAMLs, each a mock or a space.)
3. **Recipe library seed set** — which SDK examples to write first (illustrative only, no code):
   AWS SNS/SQS/DynamoDB/S3/Secrets Manager and GCP Pub/Sub/GCS/KMS is my starting list. Add any
   others your teams hit most.

*(All prior open questions — stack, interception, scripting, spaces, presets, auth — are now
resolved and captured above.)*
