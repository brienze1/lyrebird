# Implementation Plan: Form-Encoded Body Matching & Traffic Filter by Mock

**Branch**: `form-body-matching` | **Date**: 2026-10-08 | **Spec**: [spec.md](./spec.md)

## Summary

One shared helper, `jsonpath.Document(headers, body)`, returns the bytes a body path is evaluated
against: the body verbatim, unless the declared media type is `application/x-www-form-urlencoded`
and the body is not valid JSON, in which case it returns a flat JSON object of key → first value
(or nil when the form does not decode — gjson then reports every path absent). Every body-path
consumer that knows the message's headers routes through it:

| Consumer | File |
| --- | --- |
| Mock body conditions (live, `match_test` dry-run, cadence overrides — all share `Matches`) | `internal/adapters/matcher/matcher.go` |
| Traffic `request_body_path` filter | `internal/usecase/traffic_query.go` |
| Decoded traffic view `form` field | `internal/adapters/dto/traffic.go` (via `jsonpath.FormFields`) |

`matched_mock_id` is a plaintext column already selected by `store.ListTraffic`, so the
new filter is one `AND matched_mock_id = ?` clause, threaded through `usecase.TrafficFilter`, the
REST query parser and the MCP `list_traffic` input.

Scripting (`jsonpath()` sandbox helper) and response templating keep reading the raw body — out of
scope.

## Technical Context

**Language/Version**: Go 1.26. **Dependencies**: none new (`mime`, `net/url`, existing gjson).
**Testing**: unit tests per package + one BDD scenario in `test/features/mock_override.feature`.

## Constitution Check

| Principle | Status |
| --- | --- |
| I. Generic-First | Pass — form encoding is a generic HTTP media type, no per-service branch. |
| II. Agent-First (MCP twin) | Pass — `matched_mock_id` added to REST and MCP together; tool description updated. |
| III. Spy by Default, Disposable | Pass — no new persisted state; recording unchanged. |
| IV. Clean Architecture & BDD | Pass — BDD scenario written red first; logic in adapters/usecase via the existing jsonpath funnel. |
| V. Secure Defaults | Pass — no auth or encryption change. |
| VI. Ship Continuously | Pass — additive, backward-compatible wire change. |

## Decisions

- **JSON wins over a form content type** so no existing JSON test can change behaviour (SC-002).
- **Truncated bodies omit `form`**: a truncated form still parses, but its last value is wrong; the
  live matcher has no truncation flag (bodies there are capped by `peekBody`), noted in code.
- **`matched_mock_id` validation**: empty means "not filtering", like every other string filter; a
  value that is blank or carries surrounding whitespace is rejected as an invalid traffic filter
  (in the use case, so REST and MCP reject it identically) — a mock id never contains whitespace, and
  a pasted trailing newline would otherwise silently report no traffic.
