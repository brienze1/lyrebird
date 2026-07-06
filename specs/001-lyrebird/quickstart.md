# Quickstart & Validation — Lyrebird

Runnable validation scenarios proving the feature works end-to-end. Each maps to a user story and its
success criteria. (Commands are illustrative of the intended interface; implementation lands via
tasks.md.)

## Prerequisites

- Docker, or Go 1.25+ for a local run.
- A reachable test upstream (the BDD suite ships an in-memory upstream double under `test/support`).

## Run

```bash
# Container (fully ephemeral — no volume needed)
docker run --rm -p 8080:8080 -p 9090:9090 ghcr.io/brienze1/lyrebird:latest

# With seed config + durable at-rest key
docker run --rm -p 8080:8080 -p 9090:9090 \
  -v "$PWD/config:/config:ro" -v "$PWD/data:/data" \
  -e LYREBIRD_DATA_KEY="$(openssl rand -base64 32)" \
  -e LYREBIRD_TRAFFIC_TTL=24h \
  ghcr.io/brienze1/lyrebird:latest
```

- `:8080` = data-plane proxy listener (point your SUT here).
- `:9090` = control plane (MCP Streamable HTTP + Admin REST under `/__lyrebird`).

## Scenario A — Spy & record (US1 → SC-001)

1. `POST /__lyrebird/upstreams { match_host: "example.test", target_url: "https://example.test" }`.
2. Send a request through `:8080` for `example.test`.
3. Expect: real response returned unchanged; `GET /__lyrebird/traffic` shows one record with the full
   request **and** full upstream response.

## Scenario B — Override with a mock (US2 → SC-003)

1. `POST /__lyrebird/mocks` for `POST /v1/charges` → respond 402.
2. Send a matching request → get 402 from the mock; the upstream receives **zero** calls.
3. Send a sibling request (`GET /v1/health`) → passed through (spy).

## Scenario C — Agent workflow over MCP (US3 → SC-002, SC-005)

1. Call `lyrebird_guide`, then `create_mock`.
2. Call `match_test` with a sample request → confirm the mock fires and see per-condition results.
3. Send the real request → mock fires.
4. Record a real interaction, then `promote_traffic` → new mock reproduces it with full fidelity.

## Scenario D — Scripting (US4 → SC-010)

1. Create a mock with `script.respond_src` echoing a body field.
2. Send two differing requests → each gets the correct scripted response.
3. Create a mock with an infinite-loop script → the call fails safe (timeout), is recorded, server
   stays up.

## Scenario E — Multi-tenant isolation (US5 → SC-004)

1. In space `a`: mock route `R` → 500. In space `b`: mock route `R` → 200.
2. Requests with `X-Lyrebird-Space: a` get 500; with `b` get 200; neither sees the other's traffic.
3. `DELETE /__lyrebird/spaces/b` cascades; `default` cannot be deleted.

## Scenario F — Lifetimes & GC (US6 → SC-006)

1. Seed a mock via `/config`; create an ephemeral mock with `ttl_seconds: 2`.
2. After 2s the ephemeral mock is gone; after `POST /__lyrebird/reset` the seeded mock remains.
3. Traffic older than `LYREBIRD_TRAFFIC_TTL` is purged within one GC cycle.

## Scenario G — Security defaults (SC-007)

1. Start with no `LYREBIRD_AUTH_KEYS` → control plane open.
2. Restart with `LYREBIRD_AUTH_KEYS=secret1` → control-plane calls without a bearer token are
   rejected; `POST /__lyrebird/auth/token { client_key: "secret1" }` yields a 1h JWT that works; the
   data plane (`:8080`) still serves with no token.

## Scenario H — Delivery (SC-008)

- Merge a PR to `main` → `release.yml` publishes updated `ghcr.io/brienze1/lyrebird:main` and
  `:sha-<short>` images with no manual steps (a `vX.Y.Z` tag additionally publishes `:X.Y.Z`, `:X.Y`,
  `:X`, and `:latest` — SC-008 only requires an automatically-published updated image, which the
  plain `main`-merge tags already satisfy).
