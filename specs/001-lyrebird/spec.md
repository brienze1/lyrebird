# Feature Specification: Lyrebird — Agent-Driven Mock & Spy-Proxy Server

**Feature Branch**: `001-lyrebird`

**Created**: 2026-07-03

**Status**: Draft

**Input**: User description: "Lyrebird: a generic, agent-driven HTTP(S) mock and spy-proxy server controlled entirely over MCP, delivered as a public Docker image"

## Overview

Lyrebird is a generic mock and proxy server for any HTTP(S) dependency. A caller points a dependency
at Lyrebird; by default Lyrebird records every call and transparently forwards it to the real service
(spy mode). When a mock rule matches a call, Lyrebird returns the mock instead of forwarding. All
management — defining mocks, scripting responses, reading recorded traffic, promoting recordings into
permanent mocks — happens through an agent-facing control interface intended to be driven by AI
agents. There is no graphical user interface. The same generic engine mocks third-party APIs and
cloud-provider SDK calls alike; provider-specific knowledge is supplied as documented examples, not
as provider-specific behavior.

## Clarifications

### Session 2026-07-03

- Q: During spy passthrough, when the real upstream errors or is unreachable, what does the caller get? → A: Pass upstream responses through verbatim (including 4xx/5xx); synthesize a gateway error (502/504) only when the upstream is unreachable or times out. Record both cases.
- Q: When an agent deletes a partition that still contains mocks/traffic, what happens? → A: Cascade-delete its ephemeral mocks, traffic, and upstream config in one call; the default partition cannot be deleted (seeded mocks reload from config on restart).
- Q: When two same-partition mocks match with equal priority, which wins? → A: Most-recently-created wins (created-at, then id) so a newer mock overrides an older/seeded one without renumbering.
- Q: What proxy-overhead target should SC-009 commit to? → A: p95 added latency < 10 ms at 100 concurrent in-flight requests (local/dev + shared HML load).
- Q: How are very large or streaming bodies handled? → A: Bodies of any size stream through the proxy unchanged; only the stored recording is bounded — bodies above a configurable cap (default 1 MB) are truncated in the traffic log with a marker.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Record and passthrough real traffic (spy) (Priority: P1)

An engineer or agent points a system-under-test at Lyrebird instead of a real dependency. Without
defining anything, every request is transparently forwarded to the real service and both the request
and the real response are recorded. The agent can then read back exactly what was called, with what
payload, and what came back.

**Why this priority**: Spy-by-default makes Lyrebird useful the instant it is running, with zero
setup. It is the foundation every other capability builds on (recordings feed mocks and metrics).

**Independent Test**: Point a client at Lyrebird with an upstream configured, issue several requests,
and confirm each is forwarded, the real responses are returned unchanged, and every request/response
pair is retrievable through the control interface.

**Acceptance Scenarios**:

1. **Given** an upstream is configured for a host and no mock matches, **When** a client sends a
   request, **Then** Lyrebird forwards it to the real upstream and returns the real response unchanged.
2. **Given** a call was forwarded, **When** the agent lists recorded traffic, **Then** it finds one
   entry containing the full request and the full upstream response, with timing.
3. **Given** no upstream is configured for an unmatched call, **When** the request arrives, **Then**
   Lyrebird returns a clear, well-defined not-configured response and records the attempt.

---

### User Story 2 - Override selected calls with mocks (Priority: P1)

The agent defines a mock that matches specific calls (by method, path, headers, query, or body) and
returns a chosen response. Matching calls are answered by the mock; everything else continues to be
spied/passed through. Multiple mocks may target the same route and are tried in priority order.

**Why this priority**: Selective mocking on top of spy is the core value proposition — mock the few
calls a test needs to control, let the rest hit reality.

**Independent Test**: Create a mock for one endpoint, then exercise both that endpoint (served by the
mock) and a sibling endpoint (passed through), and confirm each behaves correctly.

**Acceptance Scenarios**:

1. **Given** a mock matching an endpoint exists, **When** a matching request arrives, **Then**
   Lyrebird returns the mock response and records the interaction as mocked.
2. **Given** two mocks target the same route with different match conditions and priorities, **When**
   a request arrives, **Then** the highest-priority matching mock wins and the decision is inspectable.
3. **Given** a mock does not match a request, **When** the request arrives, **Then** Lyrebird falls
   through to spy/passthrough behavior.

---

### User Story 3 - Manage everything as an agent over the control interface (Priority: P1)

An AI agent discovers and drives all capabilities through the control interface: it reads a
self-describing guide, lists/creates/updates/deletes mocks, dry-runs a sample request to see which
mock would fire and why, inspects recorded traffic, and promotes a recorded real interaction into a
persistent mock in one step.

**Why this priority**: The product exists to be operated by agents. If an agent cannot reliably
author and verify mocks unaided, the product has failed its primary purpose.

**Independent Test**: Using only the control interface, an agent creates a mock, verifies it via a
dry-run match test, triggers it, reads the recorded result, and promotes a separate recording into a
new mock — all without human help or a UI.

**Acceptance Scenarios**:

1. **Given** the control interface, **When** an agent requests the guide, **Then** it receives
   concepts, a minimal valid example, and enough detail to compose a valid mock on the first attempt.
2. **Given** a candidate request, **When** the agent runs a dry-run match test, **Then** it learns
   which mock would fire, which match conditions passed or failed, and the resulting response.
3. **Given** a recorded real interaction, **When** the agent promotes it, **Then** a persistent mock
   is created that reproduces that response for matching calls.
4. **Given** any invalid management request, **When** it is rejected, **Then** the error explains what
   was wrong and how to fix it.

---

### User Story 4 - Author advanced conditional logic with scripting (Priority: P2)

For logic beyond declarative matchers, the agent attaches a sandboxed script that decides whether a
request matches and/or builds the response dynamically (e.g., echo a field, branch on body content,
return validation errors for malformed input).

**Why this priority**: Declarative matching covers most cases; scripting is the escape hatch that
makes Lyrebird "extremely customizable" for the remainder, including request validation.

**Independent Test**: Attach a script that returns different responses based on a request body field,
then send two differing requests and confirm each gets the correct scripted response; confirm a
script cannot access the host filesystem or network beyond provided helpers.

**Acceptance Scenarios**:

1. **Given** a mock with a matching script, **When** a request satisfies the script condition, **Then**
   the mock is selected; otherwise it is skipped.
2. **Given** a mock with a response script, **When** it runs, **Then** the response is built from
   request data using only the provided sandbox helpers.
3. **Given** a script that runs too long or misbehaves, **When** it executes, **Then** it is aborted
   safely without hanging the server, and the failure is reported.

---

### User Story 5 - Reuse a shared deployment safely (multi-tenant) (Priority: P2)

Several agents use one shared Lyrebird deployment concurrently. Each works in an isolated partition so
their mocks, recorded traffic, and upstream configuration do not interfere with others'.

**Why this priority**: Lyrebird is meant to run as a shared service that anyone can use whenever
needed; without isolation, concurrent agents corrupt each other's results.

**Independent Test**: Two partitions define contradictory mocks for the same route; requests tagged
for each partition receive that partition's response and see only that partition's traffic.

**Acceptance Scenarios**:

1. **Given** two partitions with different mocks for the same route, **When** requests are tagged for
   each, **Then** each receives its own partition's response.
2. **Given** a request with no partition tag, **When** it arrives, **Then** it is handled in the
   default partition.
3. **Given** traffic and mocks in one partition, **When** another partition is inspected or reset,
   **Then** the first partition is unaffected.

---

### User Story 6 - Persist always-on mocks; auto-clean throwaway ones (Priority: P2)

Some mocks must always be present (seeded from mounted configuration at startup). Others are created
ad hoc by agents and should clean themselves up. Recorded traffic is bounded so storage never grows
without limit.

**Why this priority**: Teams need durable baseline fixtures that survive resets, while agents need
disposable scratch mocks — and the service must stay bounded when deployed long-term.

**Independent Test**: Seed a mock via configuration, create an ephemeral mock with a short lifetime,
run a reset, and confirm the seeded mock survives while the ephemeral one and old traffic are cleaned.

**Acceptance Scenarios**:

1. **Given** a mock defined in mounted configuration, **When** the service starts, **Then** the mock
   is active; **And When** a reset occurs, **Then** the seeded mock remains.
2. **Given** an ephemeral mock with a lifetime, **When** the lifetime elapses, **Then** it is removed
   automatically.
3. **Given** recorded traffic older than the retention window, **When** the cleaner runs, **Then**
   that traffic is purged and recent traffic is retained.

---

### User Story 7 - Advanced proxy control and fault injection (Priority: P3)

The agent shapes proxy behavior: rewrite a request before it is forwarded, transform a real response
before returning it, inject latency or faults for resilience testing, and restrict which hosts may be
proxied. Optionally, sequential scenarios return different responses on successive matching calls.

**Why this priority**: These make Lyrebird a best-in-class proxy for advanced testing, but they build
on the P1/P2 foundations and are not required for the core value.

**Independent Test**: Configure a request rewrite and a response transform for one route, add an
injected delay, and confirm forwarded requests and returned responses are modified as specified and
the delay is observed.

**Acceptance Scenarios**:

1. **Given** a rewrite rule, **When** a request is forwarded, **Then** the upstream receives the
   modified request.
2. **Given** a fault/latency rule, **When** a matching call occurs, **Then** the configured fault or
   delay is applied.
3. **Given** a host not on the allow-list, **When** an unmatched request targets it, **Then** it is
   not proxied and the policy outcome is recorded.

---

### Edge Cases

- **Upstream unreachable or slow during spy**: upstream responses (including 4xx/5xx) are returned
  verbatim; a synthesized gateway error (502/504) is returned only when the upstream is unreachable or
  times out. Both outcomes are recorded.
- **Large or streaming bodies**: bodies of any size stream through the proxy unchanged; only the
  stored recording is bounded — bodies above a configurable cap (default 1 MB) are truncated in the
  traffic log with a marker.
- **Contradictory mocks in the same partition on the same route**: resolved deterministically by
  priority; ties MUST have a defined, stable resolution.
- **Reset scope**: a reset clears ephemeral mocks and (optionally) traffic but never seeded mocks;
  the exact traffic-clearing behavior of reset MUST be explicit.
- **Deleting a partition**: what happens to its mocks and recorded traffic MUST be defined (cascade
  vs. block).
- **Auth enabled but token expired/missing** on a control-plane call: request is rejected with a
  clear, non-sensitive error; data-plane calls remain unaffected.
- **Restart without a stable at-rest key**: previously stored data becomes unreadable by design and
  is treated as absent, not as corruption.
- **Script errors** (throwing, timeout, invalid response shape): fail safe, record the error, do not
  crash the server or leak internals.

## Requirements *(mandatory)*

### Functional Requirements

**Spy, proxy, and recording**

- **FR-001**: System MUST, by default, record every received request and forward unmatched requests
  to the configured real upstream, returning the upstream response unchanged (spy mode).
- **FR-002**: System MUST record, for each call, the full request and — for proxied calls — the full
  real upstream response, including status, headers, body, and timing.
- **FR-003**: System MUST allow configuring the real upstream target per host/partition so passthrough
  knows where to forward.
- **FR-004**: System MUST support rewriting a request before forwarding and transforming a real
  response before returning it.
- **FR-005**: System MUST support injecting latency and faults (e.g., delay, connection reset,
  timeout, malformed body) on matching calls.
- **FR-006**: System MUST support restricting which hosts may be proxied (allow/deny policy) and
  record the policy outcome for blocked calls.

**Mocking and matching**

- **FR-007**: Users MUST be able to create, read, update, and delete mocks through the control
  interface.
- **FR-008**: A mock MUST support declarative matching on method, path (exact/glob/regex), headers,
  query parameters, and request body content.
- **FR-009**: System MUST allow multiple mocks to target the same route and MUST resolve which one
  applies deterministically by an explicit priority, first match wins.
- **FR-009a**: When multiple mocks tie on priority for a request, the most-recently-created mock MUST
  win (ordered by creation time, then mock id), so a newer mock overrides an older or seeded one
  without renumbering priorities. Resolution MUST be deterministic and stable across restarts.
- **FR-010**: A mock MUST be able to respond with a chosen status, headers, and body, with optional
  templating that injects values from the request.
- **FR-011**: System MUST provide a dry-run "match test" that, given a sample request, reports which
  mock would fire, which match conditions passed or failed, and the resulting response — without
  sending the request onward.
- **FR-012**: System MUST let users promote a recorded real interaction into a persistent mock in a
  single operation.
- **FR-013**: A mock MUST support validation behavior (e.g., returning a client-error response for
  malformed input) expressed as an ordinary matching + response rule (realized via FR-008 matching
  and FR-010 response — no separate validation mechanism).

**Scripting**

- **FR-014**: System MUST support attaching a sandboxed script to a mock that decides matching and/or
  builds the response dynamically from request data.
- **FR-015**: Scripts MUST NOT access the host filesystem, network, or environment except through an
  explicitly provided, documented helper interface.
- **FR-016**: Script execution MUST be bounded (time and resources); a misbehaving script MUST be
  aborted safely, recorded as a failure, and MUST NOT hang or crash the server.
- **FR-017**: System MUST expose documentation of the available scripting helpers through the control
  interface so an agent can author valid scripts without external references.

**Agent control interface**

- **FR-018**: The agent-facing control interface MUST be the primary control plane and MUST expose
  100% of management capabilities; any secondary administrative interface MUST NOT exceed it.
- **FR-019**: System MUST provide a self-describing guide covering concepts, composition, and at least
  one minimal valid example.
- **FR-020**: Every management operation MUST return explanatory errors stating what failed and how to
  correct it.
- **FR-021**: System MUST let agents inspect recorded traffic (filter by partition/host/path/status/
  time) and read aggregate metrics (counts and latency by mock/path/status over a window).
- **FR-022**: System MUST provide a curated example/recipe library, accessible through the control
  interface, that teaches how to mock common third-party APIs and cloud-provider SDK calls as plain
  HTTP, including how to point such SDKs at Lyrebird. This library MUST be knowledge/content only and
  MUST NOT introduce provider-specific behavior in the engine.

**Multi-tenancy, lifetimes, and cleanup**

- **FR-023**: System MUST isolate mocks, recorded traffic, and upstream configuration by partition;
  requests without a partition tag MUST be handled in a default partition.
- **FR-024**: System MUST support creating, listing, and deleting partitions. Deleting a partition
  MUST cascade-remove its ephemeral mocks, recorded traffic, and upstream configuration in one
  operation. The default partition MUST NOT be deletable.
- **FR-025**: System MUST support seeded mocks loaded from mounted configuration at startup that are
  protected from reset and automatic cleanup.
- **FR-026**: System MUST support ephemeral mocks created at runtime, optionally with a lifetime after
  which they are automatically removed.
- **FR-027**: System MUST bound stored recorded traffic by a configurable retention window and purge
  older records automatically.
- **FR-028**: A reset operation MUST remove ephemeral mocks (and, per configuration, recorded traffic)
  while preserving seeded mocks.
- **FR-029**: All stored state MUST be disposable: loss on restart MUST be acceptable and MUST NOT
  leave the system in a corrupt state.

**Security**

- **FR-030**: The data plane (mock/proxy listeners) MUST NEVER require authentication.
- **FR-031**: Control-plane authentication MUST be disabled by default and MUST activate only when the
  operator supplies the relevant configuration; when active, control-plane operations MUST require a
  short-lived token (default lifetime 1 hour, configurable).
- **FR-032**: Sensitive stored payload content MUST be encrypted at rest by default using a key
  generated at startup; operators MAY supply a stable key to make stored data survive restarts.
- **FR-033**: System MUST NOT write secret material (tokens, keys, client secrets) to logs or persist
  it in cleartext.

**Delivery**

- **FR-034**: System MUST be deliverable as a container image that runs locally or as a long-lived
  shared service, configurable via environment variables and mounted configuration.
- **FR-035**: Merging changes to the main branch MUST automatically publish an updated public image;
  proposed changes MUST be gated by automated checks before merge.

### Key Entities *(include if feature involves data)*

- **Partition (Space)**: An isolation boundary owning a set of mocks, recorded traffic, and upstream
  configuration for one agent/session/tenant. Identified by a tag on incoming requests; a default
  partition exists. **Terminology**: "partition" and "space" refer to the same concept — "partition"
  is used in internal/data descriptions and "space" is the external request-tag and tool name
  (e.g. `X-Lyrebird-Space`, `create_space`). The two terms are interchangeable throughout.
- **Upstream**: The real target for a host/route within a partition, used for passthrough.
- **Mock**: A named rule with a lifetime (seeded or ephemeral), match conditions (declarative and/or
  scripted), an action (respond, proxy, or fault), a priority, and an optional group label. Many mocks
  may share a route.
- **Traffic Record**: A recorded interaction — the full request and, for proxied calls, the full real
  response, plus the match decision and timing. Subject to the retention window.
- **Scenario**: An optional ordered/stateful-lite behavior where successive matching calls return
  successive responses; reset on reset.
- **Recipe/Example**: Documentation content teaching how to mock a given API/SDK as plain HTTP.
- **Token / Client Credential**: Control-plane auth material used only when auth is enabled.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With an upstream configured and no mocks defined, 100% of requests are forwarded and
  their real responses returned unchanged, and 100% are retrievable as traffic records.
- **SC-002**: An agent, using only the control interface and its guide (no human help, no UI), can
  create a working mock, verify it via match-test, and confirm it fires — in under 5 management
  operations for a simple case.
- **SC-003**: When a mock matches, the caller receives the mock response and no request is forwarded
  upstream (verifiable by the upstream receiving zero calls for mocked routes).
- **SC-004**: Two agents using one shared deployment concurrently never observe each other's mocks or
  traffic; contradictory same-route mocks in different partitions each return correctly in 100% of
  tagged requests.
- **SC-005**: A recorded real interaction can be promoted to a mock that thereafter reproduces the
  recorded response for matching calls with 100% fidelity of status, headers, and body.
- **SC-006**: Stored recorded traffic never exceeds the configured retention window; records older
  than the window are gone after at most one cleaner cycle.
- **SC-007**: Enabling control-plane auth via configuration alone (no code change, no different image)
  causes 100% of unauthenticated control-plane calls to be rejected while 100% of data-plane calls
  continue to succeed.
- **SC-008**: A merge to the main branch results in an updated public image being published with no
  manual steps per merge (a one-time registry setup, e.g. making the package public, is excluded).
- **SC-009**: The added latency Lyrebird introduces on a passthrough call, versus calling the upstream
  directly, stays at or below a p95 of 10 ms with 100 concurrent in-flight requests.
- **SC-010**: A misbehaving script (infinite loop or error) never hangs or crashes the server; the
  affected call fails safely and is recorded, in 100% of such cases.

## Assumptions

- Callers can be pointed at Lyrebird via standard endpoint/base-URL configuration (including provider
  SDK endpoint-override mechanisms); a fully transparent intercept mode is an advanced option, not the
  default.
- Lyrebird does not emulate stateful business data; a "database" mock returns whatever its rule
  specifies rather than maintaining real records.
- Default retention window and default ephemeral-mock lifetime are operator-configurable; sensible
  defaults are chosen when unset (e.g., a 24-hour traffic retention default) and documented.
- The initial recipe library targets commonly used cloud SDK calls (e.g., AWS SNS/SQS/DynamoDB/S3/
  Secrets Manager and GCP Pub/Sub/GCS/KMS) as illustrative examples; the set is extensible as content.
- Seed configuration is provided as mounted files read at startup; the exact file granularity is a
  planning detail (one file per mock vs. a single bundle).
- Recorded data and mock definitions are not treated as long-term systems of record; disposability is
  intentional.
- Expected deployment environments are local developer machines and shared non-production (e.g.,
  homolog/HML) environments.
