# Contract — Data Plane (proxy request lifecycle)

The data plane is the set of proxy listeners the system-under-test points at. It is **never
authenticated** (FR-030). Every request is recorded (FR-002). Behavior:

```
1. Receive request on a proxy listener.
2. Resolve partition from X-Lyrebird-Space header (default "default").
3. Record the incoming request (body truncated in the recording above cap, default 1 MB; R6).
4. Select candidate mocks for this partition; order by priority desc, created_at desc, id.
5. First mock whose declarative match AND script match(req) (if present) is true → WINS.
      action=respond → build response (template or respond(req) script) + optional latency;
                       record decision=mocked; return.
      action=fault   → apply delay/reset/timeout/malformed; record decision=faulted; return.
      action=proxy   → forward to resolved Upstream (see 6).
6. No matching mock (or action=proxy) → SPY passthrough:
      - Reverse mode: resolve Upstream by match_host; forward.
      - Forward/MITM mode (M6): destination derived from request; forward.
      - Upstream 2xx–5xx → return verbatim; record decision=proxied with full real response.
      - Upstream unreachable/timeout → synthesize 502/504; record decision=proxied (error).
      - No Upstream configured (reverse mode) → decision=not_configured; return defined 404-style body.
7. Bodies stream through unbounded regardless of the recording cap (R6).
```

## Guarantees

- **SC-001**: with an upstream and no mocks, 100% of calls forwarded + returned unchanged + recorded.
- **SC-003**: a matching mock returns without any upstream call (upstream sees zero calls).
- **SC-004**: partitions never leak mocks/traffic across each other.
- **SC-009**: added passthrough latency p95 < 10 ms at 100 concurrent in-flight requests.
- **SC-010**: a misbehaving script fails safe (timeout/abort), records the failure, never hangs/crashes.

## Non-goals

- No SigV4/authorization validation of incoming signed requests (we mock, not authenticate).
- No stateful business emulation (a "DynamoDB" mock returns what its rule says).
