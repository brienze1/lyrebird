package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type stubMockCRUD struct {
	mock domain.Mock
	list []domain.Mock
	err  error

	gotCreate usecase.MockInput

	gotGetPartition, gotGetID string

	gotListPartition, gotListGroup string

	gotUpdatePartition, gotUpdateID string
	gotUpdateIn                     usecase.MockInput

	gotDeletePartition, gotDeleteID string
}

func (s *stubMockCRUD) Create(_ context.Context, in usecase.MockInput) (domain.Mock, error) {
	s.gotCreate = in
	return s.mock, s.err
}

func (s *stubMockCRUD) Get(_ context.Context, partition, id string) (domain.Mock, error) {
	s.gotGetPartition, s.gotGetID = partition, id
	return s.mock, s.err
}

func (s *stubMockCRUD) List(_ context.Context, partition, group string) ([]domain.Mock, error) {
	s.gotListPartition, s.gotListGroup = partition, group
	return s.list, s.err
}

func (s *stubMockCRUD) Update(_ context.Context, partition, id string, in usecase.MockInput) (domain.Mock, error) {
	s.gotUpdatePartition, s.gotUpdateID, s.gotUpdateIn = partition, id, in
	return s.mock, s.err
}

func (s *stubMockCRUD) Delete(_ context.Context, partition, id string) error {
	s.gotDeletePartition, s.gotDeleteID = partition, id
	return s.err
}

type stubReset struct {
	out usecase.ResetOutput
	err error
	got usecase.ResetInput
}

func (s *stubReset) Execute(_ context.Context, in usecase.ResetInput) (usecase.ResetOutput, error) {
	s.got = in
	return s.out, s.err
}

type stubMatchTest struct {
	out          usecase.MatchTestOutput
	err          error
	gotPartition string
	gotIn        usecase.MatchInput
}

func (s *stubMatchTest) Execute(_ context.Context, partition string, in usecase.MatchInput) (usecase.MatchTestOutput, error) {
	s.gotPartition, s.gotIn = partition, in
	return s.out, s.err
}

func mocksTestDeps(crud mockCRUDPort, reset *stubReset, matchTest *stubMatchTest) Deps {
	return Deps{DefaultSpace: "default", MockCRUD: crud, Reset: reset, MatchTest: matchTest}
}

func decodeMockArgs(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"name":   "ping",
		"match":  map[string]any{"method": "GET", "path": "/ping"},
		"action": map[string]any{"respond": map[string]any{"status": 200, "body": "pong"}},
	}
}

func TestCreateMockPersistsAndReturnsTheCreatedMock(t *testing.T) {
	crud := &stubMockCRUD{mock: domain.Mock{ID: "m1", Name: "ping"}}
	srv := New(mocksTestDeps(crud, &stubReset{}, &stubMatchTest{}))

	result := callTool(t, srv, "create_mock", decodeMockArgs(t))
	if result.IsError {
		t.Fatalf("create_mock returned an error: %s", errTextIfError(result))
	}
	if crud.gotCreate.Name != "ping" || crud.gotCreate.Match.Method != "GET" || crud.gotCreate.Match.Path != "/ping" {
		t.Errorf("use case received %+v, want the decoded mock input", crud.gotCreate)
	}
	if crud.gotCreate.Partition != "default" {
		t.Errorf("Partition = %q, want the default space", crud.gotCreate.Partition)
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok || out["id"] != "m1" {
		t.Errorf("structured content = %+v, want id=m1", result.StructuredContent)
	}
}

func TestCreateMockRejectsInvalidLifetimeInDTO(t *testing.T) {
	crud := &stubMockCRUD{}
	srv := New(mocksTestDeps(crud, &stubReset{}, &stubMatchTest{}))

	args := decodeMockArgs(t)
	args["lifetime"] = "seeded"
	result := callTool(t, srv, "create_mock", args)
	if !result.IsError {
		t.Fatal("create_mock with lifetime=seeded should be rejected, got success")
	}
	if !strings.Contains(errText(t, result), "validation:") {
		t.Errorf("error = %q, want a validation-tagged message", errText(t, result))
	}
}

func TestCreateMockRejectsInvalidActionInDTO(t *testing.T) {
	crud := &stubMockCRUD{}
	srv := New(mocksTestDeps(crud, &stubReset{}, &stubMatchTest{}))

	args := map[string]any{"name": "ping", "match": map[string]any{"method": "GET", "path": "/ping"}}
	result := callTool(t, srv, "create_mock", args)
	if !result.IsError {
		t.Fatal("create_mock with no action variant should be rejected, got success")
	}
}

func TestCreateMockMapsUseCaseErrorViaExplainWithKindTag(t *testing.T) {
	crud := &stubMockCRUD{err: domain.ErrInvalidMock}
	srv := New(mocksTestDeps(crud, &stubReset{}, &stubMatchTest{}))

	result := callTool(t, srv, "create_mock", decodeMockArgs(t))
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "validation: ") {
		t.Errorf("error = %q, want it prefixed with the validation kind tag", msg)
	}
}

func TestGetMockReturnsDecodedMock(t *testing.T) {
	crud := &stubMockCRUD{mock: domain.Mock{ID: "m1", Name: "ping"}}
	srv := New(mocksTestDeps(crud, &stubReset{}, &stubMatchTest{}))

	result := callTool(t, srv, "get_mock", map[string]any{"id": "m1"})
	if result.IsError {
		t.Fatalf("get_mock returned an error: %s", errTextIfError(result))
	}
	if crud.gotGetID != "m1" {
		t.Errorf("gotGetID = %q, want m1", crud.gotGetID)
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok || out["id"] != "m1" {
		t.Errorf("structured content = %+v, want id=m1", result.StructuredContent)
	}
}

// partitionedMockCRUD is a fake mockCRUDPort that actually stores mocks
// keyed by partition, unlike stubMockCRUD's single fixed mock/err — this is
// what lets TestGetMockRespectsExplicitNonDefaultSpace prove get_mock
// threads its space argument through: a stub that ignores partition on Get
// couldn't fail when the wrong partition is looked up.
type partitionedMockCRUD struct {
	byPartition               map[string]map[string]domain.Mock
	nextID                    int
	gotGetPartition, gotGetID string
}

func newPartitionedMockCRUD() *partitionedMockCRUD {
	return &partitionedMockCRUD{byPartition: map[string]map[string]domain.Mock{}}
}

func (s *partitionedMockCRUD) Create(_ context.Context, in usecase.MockInput) (domain.Mock, error) {
	s.nextID++
	m := domain.Mock{ID: fmt.Sprintf("m%d", s.nextID), Name: in.Name}
	if s.byPartition[in.Partition] == nil {
		s.byPartition[in.Partition] = map[string]domain.Mock{}
	}
	s.byPartition[in.Partition][m.ID] = m
	return m, nil
}

func (s *partitionedMockCRUD) Get(_ context.Context, partition, id string) (domain.Mock, error) {
	s.gotGetPartition, s.gotGetID = partition, id
	byID, ok := s.byPartition[partition]
	if !ok {
		return domain.Mock{}, domain.ErrNotFound
	}
	m, ok := byID[id]
	if !ok {
		return domain.Mock{}, domain.ErrNotFound
	}
	return m, nil
}

func (s *partitionedMockCRUD) List(context.Context, string, string) ([]domain.Mock, error) {
	return nil, nil
}

func (s *partitionedMockCRUD) Update(_ context.Context, _, _ string, _ usecase.MockInput) (domain.Mock, error) {
	return domain.Mock{}, nil
}

func (s *partitionedMockCRUD) Delete(context.Context, string, string) error { return nil }

func TestGetMockRespectsExplicitNonDefaultSpace(t *testing.T) {
	crud := newPartitionedMockCRUD()
	srv := New(mocksTestDeps(crud, &stubReset{}, &stubMatchTest{}))

	createArgs := decodeMockArgs(t)
	createArgs["space"] = "team-a"
	createResult := callTool(t, srv, "create_mock", createArgs)
	if createResult.IsError {
		t.Fatalf("create_mock returned an error: %s", errTextIfError(createResult))
	}
	createdOut, ok := createResult.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("create_mock structured content = %+v, want a map", createResult.StructuredContent)
	}
	id, _ := createdOut["id"].(string)
	if id == "" {
		t.Fatalf("create_mock did not return an id: %+v", createdOut)
	}

	getResult := callTool(t, srv, "get_mock", map[string]any{"id": id, "space": "team-a"})
	if getResult.IsError {
		t.Fatalf("get_mock(id=%s, space=team-a) returned an error: %s — get_mock is not threading space through to the use case", id, errTextIfError(getResult))
	}
	getOut, ok := getResult.StructuredContent.(map[string]any)
	if !ok || getOut["id"] != id {
		t.Errorf("get_mock structured content = %+v, want id=%s", getResult.StructuredContent, id)
	}
	if crud.gotGetPartition != "team-a" {
		t.Errorf("gotGetPartition = %q, want team-a (the space passed to get_mock)", crud.gotGetPartition)
	}
}

func TestGetMockDefaultsToConfiguredDefaultSpaceWhenSpaceOmitted(t *testing.T) {
	crud := newPartitionedMockCRUD()
	srv := New(mocksTestDeps(crud, &stubReset{}, &stubMatchTest{}))

	createResult := callTool(t, srv, "create_mock", decodeMockArgs(t))
	if createResult.IsError {
		t.Fatalf("create_mock returned an error: %s", errTextIfError(createResult))
	}
	createdOut, ok := createResult.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("create_mock structured content = %+v, want a map", createResult.StructuredContent)
	}
	id, _ := createdOut["id"].(string)
	if id == "" {
		t.Fatalf("create_mock did not return an id: %+v", createdOut)
	}

	getResult := callTool(t, srv, "get_mock", map[string]any{"id": id})
	if getResult.IsError {
		t.Fatalf("get_mock(id=%s) returned an error: %s", id, errTextIfError(getResult))
	}
	if crud.gotGetPartition != "default" {
		t.Errorf("gotGetPartition = %q, want default", crud.gotGetPartition)
	}
}

func TestGetMockMapsNotFoundErrorViaExplainWithKindTag(t *testing.T) {
	crud := &stubMockCRUD{err: domain.ErrNotFound}
	srv := New(mocksTestDeps(crud, &stubReset{}, &stubMatchTest{}))

	result := callTool(t, srv, "get_mock", map[string]any{"id": "missing"})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "not_found: ") {
		t.Errorf("error = %q, want it prefixed with the not_found kind tag", msg)
	}
}

func TestListMocksPassesSpaceAndGroupToUseCase(t *testing.T) {
	crud := &stubMockCRUD{list: []domain.Mock{{ID: "m1", Name: "ping"}}}
	srv := New(mocksTestDeps(crud, &stubReset{}, &stubMatchTest{}))

	result := callTool(t, srv, "list_mocks", map[string]any{"space": "team-a", "group": "canary"})
	if result.IsError {
		t.Fatalf("list_mocks returned an error: %s", errTextIfError(result))
	}
	if crud.gotListPartition != "team-a" || crud.gotListGroup != "canary" {
		t.Errorf("got partition=%q group=%q, want team-a/canary", crud.gotListPartition, crud.gotListGroup)
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content = %+v, want a map", result.StructuredContent)
	}
	mocks, ok := out["mocks"].([]any)
	if !ok || len(mocks) != 1 {
		t.Errorf("mocks = %+v, want one mock", out["mocks"])
	}
}

func TestListMocksDefaultsToConfiguredDefaultSpace(t *testing.T) {
	crud := &stubMockCRUD{}
	srv := New(mocksTestDeps(crud, &stubReset{}, &stubMatchTest{}))

	result := callTool(t, srv, "list_mocks", map[string]any{})
	if result.IsError {
		t.Fatalf("list_mocks returned an error: %s", errTextIfError(result))
	}
	if crud.gotListPartition != "default" {
		t.Errorf("gotListPartition = %q, want default", crud.gotListPartition)
	}
}

func TestUpdateMockPersistsAndReturnsTheUpdatedMock(t *testing.T) {
	crud := &stubMockCRUD{mock: domain.Mock{ID: "m1", Name: "updated"}}
	srv := New(mocksTestDeps(crud, &stubReset{}, &stubMatchTest{}))

	args := decodeMockArgs(t)
	args["id"] = "m1"
	args["name"] = "updated"
	result := callTool(t, srv, "update_mock", args)
	if result.IsError {
		t.Fatalf("update_mock returned an error: %s", errTextIfError(result))
	}
	if crud.gotUpdateID != "m1" || crud.gotUpdateIn.Name != "updated" {
		t.Errorf("got id=%q in=%+v, want id=m1 name=updated", crud.gotUpdateID, crud.gotUpdateIn)
	}
}

func TestUpdateMockMapsSeededMockImmutableErrorViaExplainWithKindTag(t *testing.T) {
	crud := &stubMockCRUD{err: domain.ErrSeededMockImmutable}
	srv := New(mocksTestDeps(crud, &stubReset{}, &stubMatchTest{}))

	args := decodeMockArgs(t)
	args["id"] = "seeded-1"
	result := callTool(t, srv, "update_mock", args)
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "conflict: ") {
		t.Errorf("error = %q, want it prefixed with the conflict kind tag", msg)
	}
}

func TestDeleteMockRemovesAndReturnsDeletedTrue(t *testing.T) {
	crud := &stubMockCRUD{}
	srv := New(mocksTestDeps(crud, &stubReset{}, &stubMatchTest{}))

	result := callTool(t, srv, "delete_mock", map[string]any{"id": "m1"})
	if result.IsError {
		t.Fatalf("delete_mock returned an error: %s", errTextIfError(result))
	}
	if crud.gotDeleteID != "m1" {
		t.Errorf("gotDeleteID = %q, want m1", crud.gotDeleteID)
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok || out["deleted"] != true {
		t.Errorf("structured content = %+v, want deleted=true", result.StructuredContent)
	}
}

func TestDeleteMockMapsNotFoundErrorViaExplainWithKindTag(t *testing.T) {
	crud := &stubMockCRUD{err: domain.ErrNotFound}
	srv := New(mocksTestDeps(crud, &stubReset{}, &stubMatchTest{}))

	result := callTool(t, srv, "delete_mock", map[string]any{"id": "missing"})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "not_found: ") {
		t.Errorf("error = %q, want it prefixed with the not_found kind tag", msg)
	}
}

func TestResetPassesSpaceAndClearTrafficToUseCase(t *testing.T) {
	reset := &stubReset{out: usecase.ResetOutput{MocksRemoved: 3, TrafficCleared: true}}
	srv := New(mocksTestDeps(&stubMockCRUD{}, reset, &stubMatchTest{}))

	result := callTool(t, srv, "reset", map[string]any{"space": "team-a", "clear_traffic": true})
	if result.IsError {
		t.Fatalf("reset returned an error: %s", errTextIfError(result))
	}
	if reset.got.Partition != "team-a" || !reset.got.ClearTraffic {
		t.Errorf("got %+v, want partition=team-a clear_traffic=true", reset.got)
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok || out["mocks_removed"] != 3.0 || out["traffic_cleared"] != true {
		t.Errorf("structured content = %+v, want mocks_removed=3 traffic_cleared=true", result.StructuredContent)
	}
}

func TestResetMapsUseCaseErrorViaExplainWithKindTag(t *testing.T) {
	reset := &stubReset{err: domain.ErrNotFound}
	srv := New(mocksTestDeps(&stubMockCRUD{}, reset, &stubMatchTest{}))

	result := callTool(t, srv, "reset", map[string]any{})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "not_found: ") {
		t.Errorf("error = %q, want it prefixed with the not_found kind tag", msg)
	}
}

func TestMatchTestPassesSpaceAndSampleRequestToUseCase(t *testing.T) {
	winner := domain.Mock{ID: "m1", Name: "ping"}
	matchTest := &stubMatchTest{out: usecase.MatchTestOutput{Winner: &winner, Status: 200, Body: []byte("pong")}}
	srv := New(mocksTestDeps(&stubMockCRUD{}, &stubReset{}, matchTest))

	result := callTool(t, srv, "match_test", map[string]any{
		"space":          "team-a",
		"sample_request": map[string]any{"method": "GET", "path": "/ping"},
	})
	if result.IsError {
		t.Fatalf("match_test returned an error: %s", errTextIfError(result))
	}
	if matchTest.gotPartition != "team-a" || matchTest.gotIn.Method != "GET" || matchTest.gotIn.Path != "/ping" {
		t.Errorf("got partition=%q in=%+v, want team-a GET /ping", matchTest.gotPartition, matchTest.gotIn)
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok || out["status"] != 200.0 || out["body"] != "pong" {
		t.Errorf("structured content = %+v, want status=200 body=pong", result.StructuredContent)
	}
}

func TestMatchTestMapsUseCaseErrorViaExplainWithKindTag(t *testing.T) {
	matchTest := &stubMatchTest{err: domain.ErrInvalidMock}
	srv := New(mocksTestDeps(&stubMockCRUD{}, &stubReset{}, matchTest))

	result := callTool(t, srv, "match_test", map[string]any{"sample_request": map[string]any{"method": "GET", "path": "/ping"}})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "validation: ") {
		t.Errorf("error = %q, want it prefixed with the validation kind tag", msg)
	}
}

// errTextIfError returns the error text for a failing test-diagnostic
// message when a result unexpectedly has IsError set, without itself
// failing the test (unlike errText).
func errTextIfError(result *sdkmcp.CallToolResult) string {
	if !result.IsError || len(result.Content) == 0 {
		return ""
	}
	if tc, ok := result.Content[0].(*sdkmcp.TextContent); ok {
		return tc.Text
	}
	return ""
}
