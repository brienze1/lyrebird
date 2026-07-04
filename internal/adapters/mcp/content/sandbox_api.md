# Script Sandbox API

Mocks may attach a sandboxed JavaScript hook that decides matching and/or builds the response
dynamically from request data. The sandbox exposes exactly these globals — nothing else (no
filesystem, network, or environment access):

- `req` — the inbound request: `req.method`, `req.path`, `req.headers`, `req.query`, `req.body`
  (parsed JSON if the content type allows, else raw text/bytes).
- `uuid()` — returns a random v4 UUID string.
- `now()` — returns the current time as an ISO-8601 string.
- `faker` — a small set of realistic fake-data generators (e.g. `faker.name()`, `faker.email()`)
  for building varied response bodies.
- `jsonpath(value, path)` — evaluates a JSONPath expression against a value and returns the match
  (or `undefined` if not found).

Scripts run with a bounded execution time and memory limit; a script that times out, throws, or
loops forever fails safe — the call is recorded as a failure and the server keeps running.

Note: the scripting engine itself lands in a later milestone. This document describes the sandbox
API surface agents can already rely on when authoring `script` fields ahead of that.
