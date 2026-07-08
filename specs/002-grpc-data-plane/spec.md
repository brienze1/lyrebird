# Feature Specification: Generic gRPC Data Plane

**Feature Branch**: `002-grpc-data-plane`

**Created**: 2026-07-07

**Status**: Draft

**Input**: User description: "Generic plaintext-gRPC data plane for Lyrebird. Add a generic gRPC (h2c, no TLS, no auth) data plane that serves matched mocks over the same match→respond model as the HTTP data plane, so agents can mock any unary gRPC method WITHOUT per-service code. GCP KMS Decrypt echo and GCP Pub/Sub GetTopic/Publish must be expressible entirely as seed recipes/config, adding zero service-specific code. GCS (stateful object store) is out of scope, deferred pending a constitution governance decision."

## Clarifications

### Session 2026-07-07

Resolved from authoritative sources / recorded design decisions (see `research.md`); no
blocking user-facing ambiguities remained after these:

- Q: Exact protobuf field numbers for the acceptance messages, and how much response-building
  machinery is needed? → A: Verified from the googleapis protos. KMS `DecryptResponse.plaintext`
  (field 1) ← `DecryptRequest.ciphertext` (field 2); `plaintext_crc32c` (2) ← request
  `ciphertext_crc32c` (5) when present. Pub/Sub `Topic.name` (1) ← `GetTopicRequest.topic` (1);
  `PublishResponse.message_ids` (1) = synthetic. Every behavior reduces to **field-copy +
  constant fields** — no CRC computation, no compiled schema, no goja proto codec.
- Q: Is the gRPC listener always on, or opt-in? → A: Opt-in. It binds only when
  `LYREBIRD_GRPC_PORT` is set (mirrors MITM's explicit-enable shape), guaranteeing unchanged
  behavior when unset (FR-010) and zero new surface by default.
- Q: Must `Publish` return one message id per request message? → A: No. A fixed single
  synthetic id is sufficient — the publish path is not functionally exercised locally (only the
  boot-time topic-exists check must pass). Count-matching is explicitly deferred.
- Q: How is each acceptance validated in the test suite? → A: KMS (SC-002) is proven with the
  **real** `cloud.google.com/go/kms/apiv1` client (endpoint override, no auth, insecure) decrypting
  a base64 stub in-process. Pub/Sub (SC-003) is proven at the **wire level** with a raw gRPC client
  sending real `Publisher/GetTopic` + `Publish` messages (byte-identical to the real client), rather
  than adding the heavy `cloud.google.com/go/pubsub` dependency — the real gRPC transport is already
  proven by the KMS real-client test. Both are validated against the in-process Lyrebird server, not
  a running wallet-api/social-api. **Open item for the consumer follow-up:** confirm social-api's
  Pub/Sub boot path calls `GetTopic` (mocked) and not another `Publisher` method
  (`CreateTopic`/`UpdateTopic`) before dropping the emulator; if it does, add that method as a second
  seed mock (same echo shape).
- Q: Must the KMS CRC field be satisfied? → A: Handled robustly without a decision: the recipe
  echoes the request's `ciphertext_crc32c` into `plaintext_crc32c` when the client sends it
  (valid because under echo plaintext == ciphertext), and omits it otherwise. Covers both a
  verifying and a non-verifying consumer.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Mock any unary gRPC method generically (Priority: P1)

An agent or operator points a gRPC client (plaintext, no TLS, no auth) at Lyrebird and
receives a mocked response for a matched method — without Lyrebird containing any code
specific to that client's service or message types. The mock is authored the same way an
HTTP mock is: a match rule (now keyed on the gRPC method and on request message fields) and
a respond action (a response message described declaratively). Everything about "which
service / which fields" lives in the mock definition, never in Lyrebird's code.

**Why this priority**: This is the whole feature. Without a generic gRPC data plane there is
nothing to build recipes on, and the two acceptance targets (KMS, Pub/Sub) cannot exist.
It is the MVP: a single generic engine that turns a gRPC call into the existing match→respond
decision.

**Independent Test**: Seed a mock matching an arbitrary unary method path with a response
message defined as field values; dial that method with a plaintext gRPC client; confirm the
client receives the exact response message and an OK status. No service-specific code is
added to make this work.

**Acceptance Scenarios**:

1. **Given** a seeded mock for method `/some.pkg.Service/Method` that responds with a message
   built from declared fields, **When** a plaintext gRPC client calls that method, **Then**
   the client receives the declared response message with gRPC status OK.
2. **Given** a mock whose match rule inspects a request message field, **When** a client calls
   the method with a matching field value, **Then** the mock matches; **When** the field
   value does not match, **Then** the mock does not match.
3. **Given** no mock matches an incoming unary method call, **When** the client calls it,
   **Then** the call fails cleanly with an "unimplemented" gRPC status (never a hang, panic,
   or plaintext-HTTP error body).

---

### User Story 2 - GCP KMS Decrypt echo as a recipe (Priority: P1)

A service that decrypts secrets at boot using the official GCP KMS client is pointed at
Lyrebird (plaintext gRPC endpoint, authentication disabled). Lyrebird answers the KMS
`Decrypt` call by echoing the request ciphertext back as the plaintext, so the service —
which stores base64-of-plaintext stubs and round-trips them — boots and "decrypts" its stubs
back to their original bytes. This behavior is defined entirely as a **recipe/seed config**
(method path + field-echo response), adding no KMS-specific code to Lyrebird.

**Why this priority**: This is one of the two containers the creator-ads stack drops once
Lyrebird ships gRPC. It is the smallest, most clearly-defined acceptance target and proves the
"generic engine + recipe" model on a real consumer.

**Independent Test**: Seed the KMS Decrypt echo recipe; point a real KMS client at Lyrebird
with endpoint override, authentication disabled, and insecure transport; call Decrypt with a
base64 stub; confirm the returned plaintext equals the base64-decode of the input ciphertext.

**Acceptance Scenarios**:

1. **Given** the KMS Decrypt echo recipe is seeded, **When** a KMS client calls Decrypt with
   ciphertext `C`, **Then** the response plaintext equals `C` (echo) and the call returns OK.
2. **Given** the consuming service configured against Lyrebird, **When** it boots and decrypts
   its four base64 stubs, **Then** each decrypts to its original bytes and the service starts.

---

### User Story 3 - GCP Pub/Sub topic-exists + publish sink as a recipe (Priority: P2)

A service that constructs a Pub/Sub publisher at boot and verifies its topic exists is pointed
at Lyrebird via the standard Pub/Sub emulator endpoint variable. Lyrebird answers the topic
lookup so the existence check passes, and accepts publishes (returning synthetic message ids
and OK) without retaining anything. Defined entirely as recipe/seed config.

**Why this priority**: The second dropped container. Slightly lower priority than KMS because
the publish path itself is not exercised locally — only the boot-time topic-exists check must
pass. Independently valuable and testable.

**Independent Test**: Seed the Pub/Sub recipe (a topic-lookup response for the expected topic
and a publish-accept response); point a Pub/Sub client at Lyrebird via the emulator endpoint;
confirm the topic-exists check passes and a publish returns OK with message ids.

**Acceptance Scenarios**:

1. **Given** the Pub/Sub recipe is seeded for a topic `T`, **When** a client looks up topic
   `T`, **Then** the lookup succeeds (topic returned, not "not found").
2. **Given** the recipe is seeded, **When** a client publishes to `T`, **Then** the call
   returns OK with one or more message ids and Lyrebird retains no message state.
3. **Given** the consuming service configured against Lyrebird, **When** it boots, **Then** it
   passes its topic-exists check without erroring.

---

### User Story 4 - Recipes discoverable and reset-safe (Priority: P3)

An agent browsing the recipe library over the control plane (REST or MCP) finds ready-to-adapt
gRPC recipes for KMS and Pub/Sub alongside the existing HTTP recipes, and can post a recipe's
mock payload to create it. gRPC mocks behave like every other mock under reset/GC: ephemeral
gRPC mocks are cleared by reset and expire under retention; seeded gRPC recipes are protected.

**Why this priority**: Discoverability and disposability are how the product stays generic and
operable, but the core value (US1–US3) works without browsing the library.

**Acceptance Scenarios**:

1. **Given** the recipe library, **When** an agent lists recipes, **Then** the gRPC KMS and
   Pub/Sub recipes appear as summaries and each full recipe carries a ready-to-adapt mock.
2. **Given** an ephemeral gRPC mock and a seeded gRPC recipe in a space, **When** the space is
   reset, **Then** the ephemeral gRPC mock is removed and the seeded one remains.

### Edge Cases

- A request message whose bytes are truncated or not valid protobuf → the call fails cleanly
  with an explanatory gRPC error status; the server never panics or hangs.
- A method call with no matching mock → clean "unimplemented" status (US1 scenario 3).
- A gRPC client that sends no space header → the call is served from the default space (gRPC
  clients cannot set Lyrebird's space header; default-space behavior is expected and sufficient).
- A response recipe that references a request field which is absent → the referenced value is
  treated as empty/omitted rather than failing the whole response.
- Streaming (non-unary) methods → out of scope for this feature; only unary is served. A
  streaming call receives a clean "unimplemented" status.
- The gRPC listener is not configured (no port set) → Lyrebird runs exactly as today with no
  gRPC surface; enabling it never changes HTTP/control-plane behavior.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Lyrebird MUST serve a plaintext (no TLS) gRPC data plane on a dedicated,
  operator-configured listener, reachable by clients that dial with insecure transport and
  authentication disabled.
- **FR-002**: The gRPC data plane MUST route an incoming unary call by its gRPC method path
  and evaluate it through the SAME match→respond decision model used by the HTTP data plane
  (mocks resolved by priority/recency, seeded vs ephemeral, per space).
- **FR-003**: Lyrebird MUST support matching on request-message content generically — by
  addressing protobuf fields by their field number — WITHOUT any compiled service/message
  schema and WITHOUT any per-service code path.
- **FR-004**: Lyrebird MUST support producing a response message generically from a declarative
  description of field values (including copying/echoing a value from a named request field),
  re-encoded to the protobuf wire format, WITHOUT any compiled schema or per-service code.
- **FR-005**: A matched gRPC mock MUST return the mocked response message with gRPC status OK;
  an unmatched unary call MUST return a clean "unimplemented" status; a malformed request or an
  internal failure MUST return a clean, explanatory gRPC error status — never a hang or crash.
- **FR-006**: The GCP KMS `Decrypt` echo behavior (respond with plaintext equal to the request
  ciphertext) MUST be expressible entirely as seed recipe/config, with NO KMS-specific code in
  Lyrebird.
- **FR-007**: The GCP Pub/Sub topic-exists (topic lookup returns the topic) and publish-accept
  (returns synthetic message ids + OK, retains nothing) behaviors MUST be expressible entirely
  as seed recipe/config, with NO Pub/Sub-specific code in Lyrebird.
- **FR-008**: gRPC mocks MUST be creatable, readable, updatable, and deletable through the same
  admin REST and MCP control-plane surfaces as HTTP mocks, and loadable from mounted seed config
  — no new parallel control surface.
- **FR-009**: gRPC mocks MUST honor reset and retention/GC identically to HTTP mocks: ephemeral
  gRPC mocks are cleared by reset and expire under the retention window; seeded gRPC recipes are
  protected from both.
- **FR-010**: Enabling or disabling the gRPC listener MUST NOT change any existing behavior of
  the HTTP data plane, the control plane, spaces, seed loading, or reset. When the listener is
  not configured, Lyrebird MUST behave exactly as it does today.
- **FR-011**: The gRPC data plane MUST NEVER require authentication (consistent with the HTTP
  data plane).
- **FR-012**: The recipe library MUST include ready-to-adapt gRPC recipes for KMS Decrypt echo
  and Pub/Sub topic-exists/publish, discoverable over both REST and MCP like existing recipes.
- **FR-013**: gRPC traffic decisions (matched/unmatched/error) MUST be recorded in the traffic
  log like HTTP traffic, subject to the same disposability and retention rules.
- **FR-014**: The new listener's configuration and port MUST be documented in the existing
  environment-variable style, and the port exposed by the distributed container image.

### Out of Scope

- **Stateful GCS object store (fake-gcs replacement)**: DEFERRED. A store that persists uploaded
  bytes and serves them back is stateful business-data emulation, which conflicts with the
  project's generic-first and disposability principles. It requires an explicit governance
  decision (constitution amendment) before any implementation and is therefore excluded from
  this feature. See Assumptions.
- **Non-unary (streaming) gRPC methods**: not served in this feature.
- **gRPC-side space selection via request metadata**: not built; gRPC calls use the default space.

### Key Entities *(include if feature involves data)*

- **gRPC mock**: an ordinary mock whose match targets a gRPC method path and request-message
  fields (by field number) and whose respond action describes a response message by field
  values. Same lifecycle (seeded/ephemeral), same storage, same reset/GC rules as an HTTP mock.
- **gRPC recipe**: a ready-to-adapt gRPC mock definition surfaced in the recipe library
  (KMS Decrypt echo; Pub/Sub topic-exists + publish).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A plaintext gRPC client calling a seeded unary method mock receives the declared
  response message with an OK status, with zero service-specific code added to Lyrebird to make
  a new service work — a new gRPC service is supported by configuration alone.
- **SC-002**: A real GCP KMS client pointed at Lyrebird decrypts each of the consuming service's
  four base64 stubs back to its original bytes (plaintext equals base64-decode of input), and
  the consuming service boots successfully against Lyrebird.
- **SC-003**: A real GCP Pub/Sub client pointed at Lyrebird passes its boot-time topic-exists
  check and receives OK from a publish call, and the consuming service boots successfully.
- **SC-004**: All existing HTTP data-plane, control-plane, spaces, seed, and reset behavior
  remains unchanged (existing behavior suite continues to pass) whether or not the gRPC listener
  is enabled.
- **SC-005**: The creator-ads stack can drop its `fake-kms` and `pubsub-emulator` containers and
  point KMS/Pub/Sub endpoints at Lyrebird with no loss of the local behaviors they relied on.

## Assumptions

- The consuming clients are the official GCP KMS and Pub/Sub client libraries dialing plaintext
  gRPC with authentication disabled and endpoint override / emulator-host set to Lyrebird; the
  endpoint resolves to a loopback/private address (a container/service name satisfies this).
- KMS echo semantics are sufficient for the consumer: it stores base64-of-plaintext stubs and
  only needs Decrypt to return the ciphertext bytes unchanged. Any integrity/CRC field the
  consumer verifies is satisfiable by echoing back the corresponding request field, since under
  echo the plaintext equals the ciphertext (to be confirmed against the real consumer during
  clarification, from the actual message definitions — not assumed from memory).
- The exact protobuf field numbers for the KMS/Pub/Sub request and response messages, and which
  response fields the consumers actually read, will be confirmed during clarification from the
  authoritative message definitions; those field numbers live in recipes, not code.
- Only the boot-time topic-exists check must pass for Pub/Sub locally; the publish path is not
  functionally exercised, so accept-and-drop with synthetic ids is sufficient.
- GCS remains served by the existing external emulator until a separate governance decision is
  made; nothing in this feature depends on GCS.
- The existing seed config, spaces, admin REST + MCP, reset, and GC mechanisms are reused as-is;
  this feature adds a data-plane transport, not a new control model.
