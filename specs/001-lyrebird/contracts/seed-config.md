# Contract — Seed Configuration (mounted at boot)

Seeded mocks/partitions/upstreams load from files mounted at `/config` at startup. They are
in-memory, protected from `reset`/GC/TTL (FR-025), and version-controllable as fixtures.

## Format & granularity

- A `/config` directory of one-or-more YAML files. Each file may declare a partition and any number
  of mocks/upstreams. (Planning decision: dir-of-YAMLs, not a single monolithic bundle — easier
  diffs and per-fixture ownership.)
- Files are read once at boot in lexical order; later files may add but not silently override earlier
  seeded mock ids (a duplicate id is a startup error surfaced in logs).

## Schema (example)

```yaml
# /config/payments.yaml
space: payments-team            # optional; default "default"
upstreams:
  - match_host: "api.stripe.com"
    target_url: "https://api.stripe.com"
  # Optional match_path narrows an upstream to matching request paths, so one
  # host can front several providers by path. Empty = any path (host-only); a
  # leading "~" is a regexp (match-only); otherwise a plain path PREFIX that is
  # a "route prefix" — it is stripped at INGRESS, before mock-matching and
  # upstream resolution, so both the mocks AND the forwarded request see the
  # clean path. i.e. one route prefix per provider, everything behind it is
  # that provider's (mock or passthrough): match_path "/graph-fb" + target
  # graph.facebook.com turns "/graph-fb/v23.0/x" into a mock lookup for
  # "/v23.0/x" and, if unmatched, a passthrough to graph.facebook.com/v23.0/x.
  #
  # A prefix route with an EMPTY target_url is strip-only: the prefix is still
  # stripped (so mocks match clean paths) but there is no passthrough — an
  # unmatched request answers not_configured (404). Use this for a fully-mocked
  # provider that has no real backend to fall through to.
  - match_host: "graph-proxy.internal"
    match_path: "/graph-fb"
    target_url: "https://graph.facebook.com"
  - match_host: "graph-proxy.internal"
    match_path: "/graph-ig"
    target_url: "https://graph.instagram.com"
  - match_host: "graph-proxy.internal"
    match_path: "/mocked-only"    # strip-only: mocks match clean paths, unmatched -> 404
    target_url: ""
mocks:
  - name: charge-declined
    lifetime: seeded            # implied for config mocks; explicit for clarity
    priority: 100
    match:
      method: POST
      path: /v1/charges
      body:
        - jsonpath: "$.amount"
          equals: "666"
    action:
      respond:
        status: 402
        headers: { Content-Type: application/json }
        body: '{"error":{"code":"card_declined"}}'
```

## Validation rules

- `space` slug; `match` and `action` required per mock; `priority` defaults to 0.
- Unknown top-level keys → startup error (fail fast, logged, no secret material echoed).
- A script block, if present, is validated for parse errors at boot.
- Import/export (`/__lyrebird/import|export`) uses this same schema for runtime round-tripping.
