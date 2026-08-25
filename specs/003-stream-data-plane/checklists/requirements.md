# Checklist: Requirements coverage — 003 byte-stream data plane

Every functional requirement in [spec.md](../spec.md), the artefact that satisfies it, and how it is
proven. `stream_data_plane.feature` is `test/features/stream_data_plane.feature`.

## Standing an endpoint up

- [X] **FR-001** opt-in plane — `config.StreamPlaneAddr` + `bootstrap.Run` · proven by
  `config_test.go` and `app_test.go`
- [X] **FR-002** handshake names endpoint and optional space — `streamplane/handshake.go` ·
  `stream_data_plane.feature`
- [X] **FR-003** unknown endpoint refused by name — `handshake.go` · feature scenario
- [X] **FR-004** several endpoints at once, no cross-answering — `registry.go` + `match.path` ·
  feature scenario
- [X] **FR-030** one stand-in per endpoint — `registry.go` · feature scenario

## Framing, projection and matching

- [X] **FR-005** framing declared once per endpoint — `domain.Endpoint.Framing`,
  `streamplane/framing.go` · `framing_test.go`
- [X] **FR-006** projection: split, offset+length, text/int/hex; endpoint default, rule override —
  `streamplane/project.go`, `usecase.MatchRequest.ExecuteProjected` · `project_test.go` + feature
- [X] **FR-007** existing condition vocabulary — reuses `domain.Match.Body` unchanged · feature
- [X] **FR-008** no protocol knowledge in Lyrebird — reviewed in `streamplane/doc.go`; the shipped
  recipe is invented configuration only
- [X] **FR-009** answer built from a declared part list, including `copyFrom` — `streamplane/build.go`
  · `build_test.go` + feature
- [X] **FR-010** scripted match and respond — `ExecuteProjected` + `BuildRespondOutputWithScript` ·
  `handler_test.go`
- [X] **FR-032** unmatched frame is silent, recorded, connection usable — `handler.go` · feature

## Emitting without being asked

- [X] **FR-011** cadence with `repeat_last`/`loop`/`stop` — `streamplane/cadence.go` ·
  `cadence_test.go` + feature
- [X] **FR-012** injection over the existing control surfaces — `usecase/emit_frame.go`,
  `mcp/stream.go`, `httpadmin/stream.go` · feature
- [X] **FR-033** whole frames, deterministic order — the single writer in `conn.go` · `conn_test.go`
- [X] **FR-013** emissions recorded and distinguishable (`EMIT`) — `handler.go` · feature
- [X] **FR-014** absorbed inbound frames — `handler.go` · feature
- [X] **FR-015** injecting into a closed connection reports plainly — `emit_frame.go` · feature

## Failure modes

- [X] **FR-016** delay / reset / timeout / malformed — `streamplane/fault.go` · `fault_test.go` +
  feature
- [X] **FR-017** faults selected by the same matching rules — reuses `domain.Action` · feature

## Observing and reusing

- [X] **FR-018** every frame recorded with direction, time, endpoint, rule, outcome — `handler.go` ·
  feature
- [X] **FR-019** promotion works unchanged — `usecase/promote_traffic.go` · feature
- [X] **FR-020** stream traffic in metrics — no adapter change · `metrics` test

## Parity

- [X] **FR-021** stream rules are ordinary mocks — no new mock surface · feature
- [X] **FR-022** seeded survives reset and never expires; session-created does neither —
  `seeds.go` + `reset.go` · feature
- [X] **FR-023** endpoints exportable as configuration — `import_export.go` · feature
- [X] **FR-024** spaces isolate endpoints — partition-keyed registry and repo · feature
- [X] **FR-031** reset stops cadences and closes connections — `reset.go` + `registry.CloseSpace` ·
  feature

## Bounds and resilience

- [X] **FR-025** proxy not served, rejected at creation, documented — `usecase/endpoints.go`,
  contract "Deferred" section · unit test
- [X] **FR-026** disabled plane changes nothing — `app_test.go` + the untouched existing suite
- [X] **FR-027** malformed input is clean and isolated — recover guard in `handler.go`, absent
  values in `project.go` · fuzz targets
- [X] **FR-034** unterminated bytes bounded by `LYREBIRD_BODY_CAP_BYTES` — `framing.go` ·
  `framing_test.go`
- [X] **FR-028** oversized frame served in full, record marked truncated — `handler.go` · unit test

## Data protection

- [X] **FR-029** everything shipped is invented — the recipe, the seed examples and the quickstart
  use no real-world values
- [X] **FR-035** export warns when it carries captured traffic — `promote_traffic.go` +
  `import_export.go` · feature
