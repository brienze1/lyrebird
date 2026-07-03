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
