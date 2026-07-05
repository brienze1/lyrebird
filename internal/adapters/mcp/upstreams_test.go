package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

type stubSetUpstream struct {
	err error
	got domain.Upstream
}

func (s *stubSetUpstream) Execute(_ context.Context, u domain.Upstream) error {
	s.got = u
	return s.err
}

type stubListUpstreams struct {
	list []domain.Upstream
	err  error
}

func (s *stubListUpstreams) Execute(_ context.Context, _ string) ([]domain.Upstream, error) {
	return s.list, s.err
}

func upstreamsTestDeps(set *stubSetUpstream, list *stubListUpstreams) Deps {
	return Deps{DefaultSpace: "default", SetUpstream: set, ListUpstreams: list}
}

func TestSetUpstreamPersistsAndEchoesTheDecodedDTO(t *testing.T) {
	set := &stubSetUpstream{}
	srv := New(upstreamsTestDeps(set, &stubListUpstreams{}))

	result := callTool(t, srv, "set_upstream", map[string]any{
		"space": "team-a", "match_host": "api.example.com", "target_url": "https://api.example.com", "tls_skip_verify": true,
	})
	if result.IsError {
		t.Fatalf("set_upstream returned an error: %s", errTextIfError(result))
	}
	if set.got.Partition != "team-a" || set.got.MatchHost != "api.example.com" || set.got.TargetURL != "https://api.example.com" || !set.got.TLSSkipVerify {
		t.Errorf("use case received %+v, want the decoded upstream", set.got)
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok || out["match_host"] != "api.example.com" {
		t.Errorf("structured content = %+v, want the echoed match_host", result.StructuredContent)
	}
}

func TestSetUpstreamDefaultsToConfiguredDefaultSpace(t *testing.T) {
	set := &stubSetUpstream{}
	srv := New(upstreamsTestDeps(set, &stubListUpstreams{}))

	result := callTool(t, srv, "set_upstream", map[string]any{"match_host": "api.example.com", "target_url": "https://api.example.com"})
	if result.IsError {
		t.Fatalf("set_upstream returned an error: %s", errTextIfError(result))
	}
	if set.got.Partition != "default" {
		t.Errorf("Partition = %q, want default", set.got.Partition)
	}
}

func TestSetUpstreamMapsUseCaseErrorViaExplainWithKindTag(t *testing.T) {
	set := &stubSetUpstream{err: domain.ErrInvalidUpstream}
	srv := New(upstreamsTestDeps(set, &stubListUpstreams{}))

	result := callTool(t, srv, "set_upstream", map[string]any{"match_host": "", "target_url": ""})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "validation: ") {
		t.Errorf("error = %q, want it prefixed with the validation kind tag", msg)
	}
}

func TestListUpstreamsReturnsDecodedList(t *testing.T) {
	list := &stubListUpstreams{list: []domain.Upstream{{MatchHost: "api.example.com", TargetURL: "https://api.example.com"}}}
	srv := New(upstreamsTestDeps(&stubSetUpstream{}, list))

	result := callTool(t, srv, "list_upstreams", map[string]any{})
	if result.IsError {
		t.Fatalf("list_upstreams returned an error: %s", errTextIfError(result))
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content = %+v, want a map", result.StructuredContent)
	}
	upstreams, ok := out["upstreams"].([]any)
	if !ok || len(upstreams) != 1 {
		t.Errorf("upstreams = %+v, want one upstream", out["upstreams"])
	}
}

func TestListUpstreamsMapsUseCaseErrorViaExplainWithKindTag(t *testing.T) {
	list := &stubListUpstreams{err: domain.ErrNotFound}
	srv := New(upstreamsTestDeps(&stubSetUpstream{}, list))

	result := callTool(t, srv, "list_upstreams", map[string]any{})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "not_found: ") {
		t.Errorf("error = %q, want it prefixed with the not_found kind tag", msg)
	}
}
