# Script Sandbox API

Mocks may attach a sandboxed JavaScript hook (`script.match_src` and/or `script.respond_src`) that
decides matching and/or builds the response body dynamically from request data. The sandbox exposes
exactly these globals — nothing else (no filesystem, network, or environment access):

- `req` — the inbound request: `req.method`, `req.path`, `req.headers`, `req.query`, `req.body`
  (parsed JSON if the content type allows, else raw text; `null` if the request has no body).
  `req.body` reflects only the first `LYREBIRD_BODY_CAP_BYTES` (default 1 MiB) of the request body —
  a body larger than the cap may parse as raw text instead of JSON, or be silently truncated
  mid-token. Don't rely on `req.body` for bodies that may exceed the cap.
- `uuid()` — returns a random v4 UUID string.
- `now()` — returns the current time as an ISO-8601 string.
- `faker` — a small set of realistic fake-data generators: `faker.name()`, `faker.email()`.
- `jsonpath(value, path)` — evaluates a JSONPath-style expression against a value and returns the
  match (or `undefined` if not found). Same path dialect as a mock's declarative `body` conditions.

## Authoring convention

A script's value is its **last-evaluated expression** — there is no top-level `return` (that's only
valid inside a function). Write scripts as a single expression, wrapping object literals in
parentheses so they aren't parsed as a block: `({field: req.body.field})`, not
`return {field: req.body.field}`.

- `match_src` — truthy means the mock matches (ANDed with any declarative `match` conditions on the
  same mock). Example: `req.method == "GET" && req.query.debug == "1"`.
- `respond_src` — its value becomes the response body: a returned string is used verbatim; anything
  else (object, array, number, boolean, null) is JSON-encoded. Status/headers still come from the
  mock's declared `action.respond`. Example: `({echoed: req.body.field})`.

Scripts run with a bounded execution time (and a bounded call-stack depth); a script that times out,
throws, or loops forever fails safe — the request gets a synthesized error response, recorded with
decision `script_failed`, and the server keeps running normally for every other request.
