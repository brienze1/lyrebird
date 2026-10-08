# Tasks: Form-Encoded Body Matching & Traffic Filter by Mock

- [x] T001 BDD scenario (red first): form-body condition fires / falls through, `test/features/mock_override.feature` + form-body send step in `test/support/steps_spy.go`
- [x] T002 `jsonpath.Document` / `jsonpath.FormFields` helper + unit tests, `internal/adapters/jsonpath/`
- [x] T003 Matcher body conditions route through `jsonpath.Document` + tests (form equals/contains/regex/exists, JSON unchanged, text/plain fails closed, undecodable form fails closed), `internal/adapters/matcher/`
- [x] T004 Traffic `request_body_path` filter uses the recorded request headers + tests, `internal/usecase/traffic_query.go`
- [x] T005 `form` on `RecordedMessageDTO` (request and response) + tests, `internal/adapters/dto/traffic.go`
- [x] T006 `matched_mock_id` filter: `usecase.TrafficFilter`, store SQL, REST query param, MCP `list_traffic` input + tests
- [x] T007 `match_test` dry-run covered by the shared matcher (dry-run test with a form body)
- [x] T008 Docs: MCP guide, `specs/001-lyrebird/contracts/{admin-rest,mcp-tools}.md`
- [x] T009 Gates: `go vet`, `gofmt`, `golangci-lint`, `go test ./... -race`
