# Feature Specification: Generic Byte-Stream Data Plane

**Feature Branch**: `003-stream-data-plane`

**Created**: 2026-08-19

**Status**: Draft

**Input**: User description: "Lyrebird serves request/response traffic over HTTP and gRPC. Some
boundaries a consumer needs to substitute are neither: they are framed byte streams — a serial
line, a serial-profile radio link, a pin-level control channel. Add a third, opt-in data plane
that carries framed byte streams. A stand-in connects to it, declares in the handshake which
endpoint it is standing in for (and optionally which space), and from then on every frame crossing
that connection in either direction is recorded and answered from exactly the same match→respond
rules, spaces, traffic log, promotion, scripting, fault injection, reset, retention, metrics, seed
config and export the existing planes provide — over the same control-plane surfaces, with no new
parallel control surface and no UI. Nothing about any specific device or protocol may be built into
Lyrebird. Where a frame ends, and how its bytes decompose into addressable fields, is declared as
configuration, so any framed protocol is just framed bytes with a declared shape — matched and
built the same generic way the gRPC plane already matches and builds protobuf by field number
without a compiled schema. The plane must also emit bytes nothing asked for: a source that streams
continuously into a buffer its consumer drains on its own schedule cannot be modelled as a reply to
a read, so a stand-in must receive a declared sequence on an interval and a test must be able to
inject bytes into a live connection at a chosen moment. Fault injection must cover the failure
modes a physical line actually has — slow, dropped, silent, corrupted. Forwarding on to a real
device is explicitly not part of this."

## Clarifications

### Session 2026-08-19

- Q: Where is a frame's boundary declared — once per endpoint, or per rule? → A: Once per endpoint.
  Every rule on it reads the stream the same way, the way a real wire has one framing.
- Q: Where is field projection declared? → A: **On the endpoint as the default, overridable per
  rule.** The common shape belongs with the endpoint next to its framing; a rule that needs a
  different decomposition of the same bytes declares its own and it wins for that rule only.
- Q: What happens when two stand-ins claim the same endpoint in the same space? → A: The second is
  refused, naming the conflict. A wire carries one peripheral, and a silent second claimant is
  almost always a stale process from a previous run.
- Q: What happens to a live connection and a running cadence when the space is reset? → A: Both
  end. Cadences stop and connections close, so nothing survives a reset by being mid-stream.
- Q: A frame arrives and no rule matches — a byte stream has no status code, so what happens? → A:
  Nothing is written back and the frame is recorded as unmatched; the connection stays usable and
  the consumer's own timeout governs. That is what a silent peripheral looks like.
- Q: A stand-in names an endpoint nothing has been declared for — refused, or accepted and served
  nothing? → A: Refused, naming the unrecognised endpoint, so a typo surfaces at bring-up rather
  than as silence three scenarios later.
- Q: What does a cadence do when its sequence runs out? → A: The cadence declares it — repeat the
  last frame, loop the whole sequence, or stop — defaulting to repeating the last frame, which is
  what a stationary source does.
- Q: How does an export that may carry captured bytes stay safe to commit? → A: Everything shipped
  with the feature is invented, and an export containing any mock derived from captured traffic
  says so in the file itself.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Substitute a byte-stream boundary and answer its frames (Priority: P1)

A developer standing up a local stack needs a device's byte-stream link to exist without the
device. They declare what the endpoint is called, how its frames end, and what an answer to a given
frame looks like. A stand-in connects, says which endpoint it is, sends a frame, and gets the
declared answer back — with no code in Lyrebird that knows anything about that device.

**Why this priority**: This is the feature. Without an endpoint that accepts framed bytes and
answers them from a rule, no byte-stream peripheral can be substituted at all. It is the smallest
slice that delivers value on its own: one endpoint, one rule, one answer.

**Independent Test**: Enable the plane, declare one endpoint and one rule, connect a stand-in, send
a matching frame and confirm the declared answer comes back; send a non-matching frame and confirm
the documented no-rule outcome. Neither step requires anything device-specific in Lyrebird.

**Acceptance Scenarios**:

1. **Given** a rule declared for a named endpoint, **When** a stand-in connects to that endpoint and
   sends a matching frame, **Then** the declared answer is written back to that stand-in.
2. **Given** a rule that matches on a declared field rather than the whole frame, **When** a frame
   arrives whose field holds the expected value, **Then** the rule matches; **When** it holds a
   different value, **Then** it does not.
3. **Given** an endpoint with a default projection and a rule declaring its own, **When** a frame
   arrives, **Then** the rule's own projection is what its conditions are evaluated against, and
   every other rule on that endpoint still sees the endpoint's default.
4. **Given** an answer declared to copy bytes out of the frame that triggered it, **When** such a
   frame arrives, **Then** the answer carries those bytes verbatim.
5. **Given** no rule matches an arriving frame, **When** it arrives, **Then** nothing is written
   back, the frame is recorded as having matched nothing, and the connection remains usable.
6. **Given** a second endpoint whose frames are shaped completely differently, **When** it is
   declared, **Then** it is served without any change to Lyrebird itself.
7. **Given** a stand-in already holds an endpoint, **When** a second claims the same endpoint in the
   same space, **Then** the second is refused naming the conflict, and the first continues.

---

### User Story 2 - Serve a source that talks without being asked (Priority: P2)

A test author needs a streaming source to behave like the real thing: it emits continuously into a
buffer its consumer drains on its own schedule, and it never answers a read. They declare the
sequence and the interval, and — at a chosen moment — push one specific frame into the live
connection to produce the state under test.

**Why this priority**: A request/response-only plane cannot model a source that talks first, so
without this such a peripheral can only be faked as a reply to a read — a different system from the
one that ships. It depends on P1 but delivers independently.

**Independent Test**: Declare a sequence and cadence for one endpoint, connect a stand-in that
sends nothing, and confirm it receives the sequence at that cadence. Inject one chosen frame from
the control surface and confirm the stand-in receives exactly that, whole and in order.

**Acceptance Scenarios**:

1. **Given** an endpoint declared to emit a sequence on a cadence, **When** a stand-in connects and
   sends nothing, **Then** it receives the sequence in order at that cadence.
2. **Given** an exhausted sequence and a cadence that did not declare otherwise, **When** the
   cadence continues, **Then** the last frame keeps being emitted; **Given** a cadence declaring
   loop or stop instead, **Then** it does that, identically on every run.
3. **Given** a live connection, **When** a test injects a chosen frame through the control surface,
   **Then** the stand-in receives exactly those bytes, whole and never spliced into another frame,
   and the injection is recorded like any other emission.
4. **Given** a stand-in sends bytes the endpoint is declared not to answer, **When** they arrive,
   **Then** they are absorbed and recorded, and no answer and no error is produced.
5. **Given** a test injects into a connection that has already closed, **When** it does so, **Then**
   it is told so plainly rather than silently succeeding.

---

### User Story 3 - Produce the failures a real line produces (Priority: P3)

A tester needs the states a bench cannot produce on demand: a line gone slow, a peripheral
unplugged mid-run, a source gone silent, and bytes that arrived corrupted. They declare which
frames provoke which failure, and the endpoint produces it.

**Why this priority**: These are the states that cause field incidents, and they are the reason a
substituted peripheral beats a physical one. The endpoint and its rules must exist first (P1), and a
stack is already useful serving only well-behaved traffic.

**Independent Test**: Declare one rule per failure mode, provoke each from a stand-in, and confirm
each produces its documented effect — delayed answer, dropped connection, no answer, altered bytes.

**Acceptance Scenarios**:

1. **Given** a rule declaring a slow line, **When** a matching frame arrives, **Then** the answer is
   delayed by the declared amount and the delay is visible in what was recorded.
2. **Given** a rule declaring a dropped connection, **When** a matching frame arrives, **Then** the
   connection is broken and a reconnect is served normally.
3. **Given** a rule declaring silence, **When** a matching frame arrives, **Then** nothing is
   written back and the stand-in's own timeout governs.
4. **Given** a rule declaring corrupted bytes, **When** a matching frame arrives, **Then** the bytes
   written back differ from a well-formed answer in the declared way.

---

### User Story 4 - Keep every capture, and turn one into a rule (Priority: P4)

Someone debugging a scenario needs to see exactly what crossed each endpoint and in which
direction, and to turn a capture they like into a permanent rule — through the surfaces they
already use for every other mock, with no new tool to learn and no screen to open.

**Why this priority**: This is parity with what the other planes already give, and it is what makes
a failing scenario diagnosable.

**Independent Test**: Run an exchange, list what was recorded and confirm both directions appear
with timing and the answering rule, promote one exchange into a rule, and confirm a repeat is
answered by the promoted rule.

**Acceptance Scenarios**:

1. **Given** frames crossed an endpoint in both directions, **When** the traffic is listed, **Then**
   every frame appears with its direction, time, endpoint and answering rule if any.
2. **Given** a recorded exchange, **When** it is promoted into a rule, **Then** repeating the same
   frame is answered by that rule without the original stand-in.
3. **Given** rules and captures exist, **When** the space is reset, **Then** session-created ones
   are gone and configuration-supplied ones remain.
4. **Given** a stand-in is connected and a cadence is running, **When** the space is reset, **Then**
   the cadence stops and the connection closes.
5. **Given** two spaces with their own rules for the same endpoint name, **When** stand-ins connect
   to each, **Then** neither observes the other's rules or captures.
6. **Given** an endpoint's rules were built up during a session, **When** they are exported, **Then**
   the export can be supplied as configuration to reproduce that endpoint, and if it carries any
   mock derived from captured traffic it says so in the file itself.

---

### Edge Cases

- Bytes arrive that never complete a frame — the endpoint must not stall forever, and must not
  treat an unterminated remainder as a frame.
- A frame larger than the recorded-body cap — it must still be served in full, and the record must
  say plainly that what was stored is truncated.
- A declared field addresses bytes the frame does not have — the value is absent, not an error.
- A stand-in names an endpoint nothing has been declared for.
- Two stand-ins claim the same endpoint in the same space.
- A connection drops mid-frame, in either direction.
- A space is reset, or a rule expires, while a connection is live and a cadence is running.
- A frame matches a rule whose action this plane cannot perform — forwarding to a real device.
- A test injects at the same instant a cadence is due to emit.
- The plane is not enabled at all — everything Lyrebird already does must be untouched.

## Requirements *(mandatory)*

### Functional Requirements

**Standing an endpoint up**

- **FR-001**: Lyrebird MUST be able to carry framed byte streams in addition to HTTP and gRPC, and
  this MUST be opt-in: unless an operator enables it, no byte-stream surface exists and Lyrebird
  behaves exactly as it does today.
- **FR-002**: A stand-in MUST identify, when it connects, which named endpoint it stands in for, and
  MAY identify which space; absent a space, the default one applies.
- **FR-003**: A stand-in naming an endpoint nothing has been declared for MUST have its connection
  refused, naming the unrecognised endpoint. It MUST NOT be accepted and left silent, and MUST NOT
  bring that endpoint into existence implicitly.
- **FR-004**: Several endpoints MUST be servable at once, and a frame on one MUST never be answered
  by a rule belonging to another.
- **FR-030**: An endpoint MUST carry at most one stand-in at a time within a space. A second MUST be
  refused naming the conflict, rather than displacing the first or being served alongside it.

**Framing, projection and matching**

- **FR-005**: Where one frame ends MUST be declared as configuration, once per endpoint, so every
  rule on that endpoint reads the stream the same way. No rule may frame the stream differently.
- **FR-006**: A frame's bytes MUST be addressable as named or positioned fields through a declared
  projection covering at minimum: splitting on a separator, taking a run of bytes at a position, and
  reading those bytes as text, as a number, or as their raw value. The projection MUST be declarable
  on the endpoint as a default and overridable by an individual rule.
- **FR-007**: Rules MUST match on those fields using the same vocabulary of conditions Lyrebird
  already offers on its other planes.
- **FR-008**: No knowledge of any specific device or protocol may exist in Lyrebird. Supporting a
  peripheral Lyrebird has never seen MUST require only new configuration — no code change and no new
  release.
- **FR-009**: An answer MUST be constructible from a declaration of its parts, including copying
  bytes out of the frame that triggered it.
- **FR-010**: Rules on a byte-stream endpoint MUST support the same scripted matching and scripted
  answering Lyrebird already offers, under the same sandbox and execution time limit.
- **FR-032**: When no rule matches, the endpoint MUST write nothing, MUST record the frame with an
  outcome stating that nothing matched, and MUST leave the connection usable — never inventing an
  answer and never closing, so "no rule was authored" and "the peripheral was unplugged" stay
  distinguishable.

**Emitting without being asked**

- **FR-011**: An endpoint MUST be able to emit a declared sequence on a declared cadence with no
  frame provoking it. Exhaustion behaviour MUST be declared — repeat the last frame, loop, or stop
  — defaulting to repeating the last frame.
- **FR-012**: A frame MUST be injectable into a live connection at a moment of the caller's
  choosing, through the same control surfaces every other operation uses.
- **FR-033**: An emitted frame MUST be delivered whole: an injection and a cadence tick MUST NOT
  interleave with each other or with an answer, and emission order MUST be delivery order.
- **FR-013**: Everything emitted without being asked MUST be recorded exactly as an answer would be,
  and MUST be distinguishable from one.
- **FR-014**: An endpoint MUST be able to absorb inbound frames needing no answer, recording them
  and producing neither a reply nor an error.
- **FR-015**: Injecting into a connection that no longer exists MUST report that plainly.

**Failure modes**

- **FR-016**: The plane MUST be able to inject, on a declared match: a delayed answer, a dropped
  connection, no answer at all, and bytes altered from a well-formed answer.
- **FR-017**: Which failure is injected MUST be selected by the same matching rules that select an
  ordinary answer.

**Observing and reusing**

- **FR-018**: Every frame crossing an endpoint in either direction MUST be recorded with its
  direction, time, endpoint, answering rule if any, and outcome.
- **FR-019**: A recorded exchange MUST be promotable into a permanent rule that reproduces it, using
  the same operation that already promotes a captured call.
- **FR-020**: Byte-stream activity MUST appear in Lyrebird's existing counters and inspection views.

**Parity**

- **FR-021**: A byte-stream rule MUST be an ordinary mock: created, read, updated and deleted
  through the same control surfaces as every other rule, with no second control surface and no UI.
- **FR-022**: Byte-stream endpoints and rules MUST be loadable from seed configuration, and seeded
  ones MUST survive a reset and never expire, while session-created ones MUST be cleared by a reset
  and expire under the same retention rule as everything else.
- **FR-023**: An endpoint's declaration and rules MUST be exportable as configuration that
  reproduces it.
- **FR-024**: Spaces MUST isolate endpoints from each other: same name, different space, no shared
  rules and no shared captures.
- **FR-031**: Resetting a space MUST stop every cadence on its endpoints and close every connection
  to them.

**Bounds and resilience**

- **FR-025**: Forwarding on to a real device MUST NOT be served. A rule asking for it MUST fail
  cleanly with a message saying so, and this limitation MUST be documented alongside the plane.
- **FR-026**: Enabling the plane MUST NOT change the behaviour of anything Lyrebird already serves,
  nor of its control surfaces, spaces, seed loading or reset.
- **FR-027**: Malformed input — bytes that never complete a frame, a frame the declared projection
  cannot decompose, a declaration addressing bytes that are not there — MUST produce a clean,
  explanatory outcome and MUST NOT hang, crash, or take the endpoint down for other connections.
- **FR-034**: Bytes that never complete a frame MUST NOT accumulate without limit. A declared
  ceiling MUST apply, after which the unterminated remainder is abandoned with an explanatory record
  rather than held indefinitely.
- **FR-028**: A frame larger than the recorded-body cap MUST still be served in full, and its record
  MUST state that what was stored is truncated.

**Data protection**

- **FR-029**: Nothing shipped with this feature may require or contain real data: every sequence,
  frame and configuration shipped MUST use invented values, and captured traffic MUST remain
  disposable local state.
- **FR-035**: An export carrying any mock derived from captured traffic MUST say so in the exported
  file itself.

### Key Entities

- **Endpoint**: a named substituted wire. A stand-in connects to one; rules, captures and cadence
  belong to it; it lives inside a space, and it declares its framing and its default projection.
- **Frame**: one complete unit of bytes crossing an endpoint, in one direction, at one moment. What
  makes it complete is declared, not assumed.
- **Projection**: the declaration that turns a run of bytes into addressable fields. Endpoint-level
  by default, rule-level when a rule needs its own.
- **Frame spec**: the declaration that turns an outbound answer back into bytes.
- **Rule**: an ordinary `domain.Mock` — answer with a declared frame, or produce a declared failure.
- **Cadence**: a declared sequence and interval an endpoint emits without being asked.
- **Capture**: the record of one frame having crossed, promotable into a rule.

## Success Criteria *(mandatory)*

- **SC-001**: A byte-stream peripheral can be fully substituted — frames in, answers out, failures
  on demand — on a machine with no hardware attached and no network access.
- **SC-002**: Supporting a peripheral whose frames are shaped unlike anything already served requires
  only new configuration: zero lines changed in Lyrebird, and no new release.
- **SC-003**: Each of the four failure modes is reachable from configuration alone.
- **SC-004**: A capture can be promoted into a rule and reproduce the same answer on the next run.
- **SC-005**: With the plane not enabled, every existing behaviour is unchanged and the existing
  verification passes untouched.
- **SC-006**: A cadence delivers its sequence in order, and repeating the same scenario ten times —
  including scenarios injecting while the cadence runs — produces ten identical sequences.
- **SC-007**: An answer is produced within 10 ms of the frame arriving, for 99 of 100 frames, on a
  developer machine.
- **SC-008**: Malformed and never-terminating input never takes an endpoint down: after a hundred
  such inputs, other connections on the same endpoint are still served normally.
- **SC-009**: Two spaces exercising the same endpoint name concurrently never observe each other's
  rules or captures.
- **SC-010**: An audit of everything shipped finds no real-world data, and every export produced
  from captured traffic carries its warning.

## Assumptions

- Stand-ins run on the same machine as Lyrebird, as part of a local development stack; carrying
  these endpoints across a network is not a requirement.
- Only boundaries whose traffic can be divided into frames are in scope.
- The consuming stack decides how a peripheral gets substituted inside the software under test;
  this feature is responsible only for what happens on the wire.
- The existing conditions, scripting sandbox, spaces, retention and control surfaces are adopted as
  they are; where a byte stream cannot carry something the other planes carry, the gap is documented
  rather than worked around.
