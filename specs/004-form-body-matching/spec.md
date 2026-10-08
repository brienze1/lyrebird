# Feature Specification: Form-Encoded Body Matching & Traffic Filter by Mock

**Feature Branch**: `form-body-matching`

**Created**: 2026-10-08

**Status**: Draft

**Input**: User description: "A consumer (a chat-bot client) posts `application/x-www-form-urlencoded`
bodies. Body conditions on mocks, the traffic listing's request-body filter and the decoded traffic
view only understand JSON, so a test can neither match nor inspect a form post. Make form bodies
addressable through the same path syntax, expose the decoded form in recorded traffic, and let a
test list only the traffic a given mock answered."

## Clarifications

- Q: Which bodies count as form-encoded? → A: Only those whose declared content type says so.
  Lyrebird never guesses from the bytes: arbitrary text that happens to look like `a=b` is not a
  form unless the sender said it was.
- Q: A form key may repeat (`a=1&a=2`). Which value is addressable? → A: The first, matching how
  query-string conditions already behave.
- Q: A body whose content type says form but whose bytes are valid JSON? → A: JSON wins — JSON
  bodies behave exactly as before, whatever their content type.
- Q: A body that says form but cannot be decoded? → A: Fails closed — every body condition reads
  the field as absent; nothing is half-parsed.

## User Scenarios & Testing

### User Story 1 — Match a mock on a form field (Priority: P1)

An agent declares a body condition (`text` contains `hello`) on a mock; the service under test posts
`channel=C1&text=hello+world` as a form. The mock fires. A form post without that field does not.

**Acceptance**:
1. Given a mock with body condition `text equals "hello world"`, When a form post carries
   `text=hello+world`, Then the mock answers.
2. Given the same mock, When the post carries `text=bye`, Then the request falls through to spy.
3. Given the same mock, When a `text/plain` post carries `text=hello+world`, Then it does not match.
4. A dry-run (`match_test`) with the same headers and body predicts the same outcome.

### User Story 2 — Inspect what a service posted as a form (Priority: P1)

A test fetches a recorded interaction and reads `request.form.<key>` directly, the way it reads
`request.json.<path>` for JSON — no base64 or query-string decoding of its own.

### User Story 3 — List only the traffic a mock answered (Priority: P2)

A test asks for recorded traffic whose matched mock is the one it pushed, so its assertions can never
pick up a neighbour's request.

## Requirements

- **FR-001**: Body conditions and the traffic request-body filter MUST evaluate their path against
  a form-encoded body as a flat object of key → first value (string), with unchanged path syntax and
  unchanged equals/contains/regex/exists semantics.
- **FR-002**: A body is form-encoded only when its declared media type is
  `application/x-www-form-urlencoded` and the bytes are not valid JSON. JSON bodies and every other
  body MUST behave exactly as before.
- **FR-003**: A declared form body that cannot be decoded MUST fail closed (fields read as absent).
- **FR-004**: The decoded traffic view MUST carry `form` (key → first value) for form-encoded request
  and response messages, omitted otherwise, including when the recorded body was truncated.
- **FR-005**: The traffic listing MUST accept a matched-mock filter returning only records answered
  by that mock, on both control surfaces (Admin REST and MCP) identically.

## Success Criteria

- **SC-001**: A form-posting client can be mocked and asserted on with no consumer-side decoding.
- **SC-002**: No existing JSON-body test changes behaviour.
