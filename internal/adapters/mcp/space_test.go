package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

type stubCreateSpace struct {
	partition domain.Partition
	err       error
	got       domain.Partition
}

func (s *stubCreateSpace) Execute(_ context.Context, p domain.Partition) (domain.Partition, error) {
	s.got = p
	return s.partition, s.err
}

type stubListSpaces struct {
	list []domain.Partition
	err  error
}

func (s *stubListSpaces) Execute(_ context.Context) ([]domain.Partition, error) {
	return s.list, s.err
}

type stubDeleteSpace struct {
	err   error
	gotID string
}

func (s *stubDeleteSpace) Execute(_ context.Context, id string) error {
	s.gotID = id
	return s.err
}

func spaceTestDeps(create *stubCreateSpace, list *stubListSpaces, del *stubDeleteSpace) Deps {
	return Deps{DefaultSpace: "default", CreateSpace: create, ListSpaces: list, DeleteSpace: del}
}

func TestCreateSpacePersistsAndReturnsTheCreatedSpace(t *testing.T) {
	create := &stubCreateSpace{partition: domain.Partition{ID: "team-a", Description: "Team A"}}
	srv := New(spaceTestDeps(create, &stubListSpaces{}, &stubDeleteSpace{}))

	result := callTool(t, srv, "create_space", map[string]any{"id": "team-a", "description": "Team A"})
	if result.IsError {
		t.Fatalf("create_space returned an error: %s", errTextIfError(result))
	}
	if create.got.ID != "team-a" || create.got.Description != "Team A" {
		t.Errorf("use case received %+v, want the decoded partition", create.got)
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok || out["id"] != "team-a" {
		t.Errorf("structured content = %+v, want id=team-a", result.StructuredContent)
	}
}

func TestCreateSpaceMapsUseCaseErrorViaExplainWithKindTag(t *testing.T) {
	create := &stubCreateSpace{err: domain.ErrInvalidPartition}
	srv := New(spaceTestDeps(create, &stubListSpaces{}, &stubDeleteSpace{}))

	result := callTool(t, srv, "create_space", map[string]any{"id": ""})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "validation: ") {
		t.Errorf("error = %q, want it prefixed with the validation kind tag", msg)
	}
}

func TestListSpacesReturnsDecodedList(t *testing.T) {
	list := &stubListSpaces{list: []domain.Partition{{ID: "team-a", Description: "Team A"}}}
	srv := New(spaceTestDeps(&stubCreateSpace{}, list, &stubDeleteSpace{}))

	result := callTool(t, srv, "list_spaces", map[string]any{})
	if result.IsError {
		t.Fatalf("list_spaces returned an error: %s", errTextIfError(result))
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content = %+v, want a map", result.StructuredContent)
	}
	spaces, ok := out["spaces"].([]any)
	if !ok || len(spaces) != 1 {
		t.Errorf("spaces = %+v, want one space", out["spaces"])
	}
}

func TestListSpacesMapsUseCaseErrorViaExplainWithKindTag(t *testing.T) {
	list := &stubListSpaces{err: domain.ErrDuplicateID}
	srv := New(spaceTestDeps(&stubCreateSpace{}, list, &stubDeleteSpace{}))

	result := callTool(t, srv, "list_spaces", map[string]any{})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "internal: ") {
		t.Errorf("error = %q, want it prefixed with the internal kind tag (ErrDuplicateID has no Explain case)", msg)
	}
}

func TestDeleteSpaceRemovesAndReturnsEmptyResult(t *testing.T) {
	del := &stubDeleteSpace{}
	srv := New(spaceTestDeps(&stubCreateSpace{}, &stubListSpaces{}, del))

	result := callTool(t, srv, "delete_space", map[string]any{"id": "team-a"})
	if result.IsError {
		t.Fatalf("delete_space returned an error: %s", errTextIfError(result))
	}
	if del.gotID != "team-a" {
		t.Errorf("gotID = %q, want team-a", del.gotID)
	}
}

func TestDeleteSpaceMapsDefaultPartitionProtectedErrorWithKindTag(t *testing.T) {
	del := &stubDeleteSpace{err: domain.ErrDefaultPartitionProtected}
	srv := New(spaceTestDeps(&stubCreateSpace{}, &stubListSpaces{}, del))

	result := callTool(t, srv, "delete_space", map[string]any{"id": "default"})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "validation: ") {
		t.Errorf("error = %q, want it prefixed with the validation kind tag (Explain maps this to KindValidation, not KindConflict)", msg)
	}
}

func TestDeleteSpaceMapsNotFoundErrorWithKindTag(t *testing.T) {
	del := &stubDeleteSpace{err: domain.ErrNotFound}
	srv := New(spaceTestDeps(&stubCreateSpace{}, &stubListSpaces{}, del))

	result := callTool(t, srv, "delete_space", map[string]any{"id": "missing"})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "not_found: ") {
		t.Errorf("error = %q, want it prefixed with the not_found kind tag", msg)
	}
}

func TestResolveSpaceFallsBackToConfiguredDefaultThenDomainDefault(t *testing.T) {
	if got := resolveSpace("team-a", "configured-default"); got != "team-a" {
		t.Errorf("resolveSpace(explicit) = %q, want team-a", got)
	}
	if got := resolveSpace("", "configured-default"); got != "configured-default" {
		t.Errorf("resolveSpace(empty, configured) = %q, want configured-default", got)
	}
	if got := resolveSpace("", ""); got != domain.DefaultPartitionID {
		t.Errorf("resolveSpace(empty, empty) = %q, want %q", got, domain.DefaultPartitionID)
	}
}
