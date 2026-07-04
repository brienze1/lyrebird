package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// scriptGateEval is a fakeMatchEval-like helper that always passes
// declarative matching (matcher.Engine's real semantics are covered
// elsewhere) so these tests can isolate MatchRequest's script-gate
// composition logic in match_request.go.
type alwaysMatchEval struct{}

func (alwaysMatchEval) Matches(_ domain.Match, _ MatchInput) (bool, []ConditionResult) {
	return true, nil
}
func (alwaysMatchEval) ValidateMatch(_ domain.Match) error { return nil }

type scriptedEval struct {
	matchResult bool
	matchErr    error
}

func (s scriptedEval) ValidateScript(_ string) error { return nil }
func (s scriptedEval) EvalMatch(_ string, _ MatchInput) (bool, error) {
	return s.matchResult, s.matchErr
}
func (s scriptedEval) EvalRespond(_ string, _ MatchInput) ([]byte, error) { return nil, nil }

func mockWithScript(id string, priority int, matchSrc string) domain.Mock {
	m := domain.Mock{ID: id, Partition: "default", Priority: priority, CreatedAt: time.Unix(int64(priority), 0)}
	if matchSrc != "" {
		m.Script = &domain.Script{MatchSrc: matchSrc}
	}
	return m
}

func TestMatchRequestScriptGateANDsWithDeclarativeMatch(t *testing.T) {
	repo := newFakeMockRepo()
	m := mockWithScript("scripted", 1, "req.method == 'GET'")
	if err := repo.CreateMock(context.Background(), m); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}

	uc := NewMatchRequest(repo, &fakeSeededSource{}, alwaysMatchEval{}, scriptedEval{matchResult: true})
	got, matched, err := uc.Execute(context.Background(), "default", MatchInput{Method: "GET"})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !matched || got.ID != "scripted" {
		t.Fatalf("Execute() = (%+v, %v), want the scripted mock matched", got, matched)
	}
}

func TestMatchRequestScriptGateRejectsWhenScriptReturnsFalse(t *testing.T) {
	repo := newFakeMockRepo()
	if err := repo.CreateMock(context.Background(), mockWithScript("scripted", 1, "false")); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}

	uc := NewMatchRequest(repo, &fakeSeededSource{}, alwaysMatchEval{}, scriptedEval{matchResult: false})
	_, matched, err := uc.Execute(context.Background(), "default", MatchInput{})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if matched {
		t.Fatal("Execute() matched, want false since the script gate returned false")
	}
}

func TestMatchRequestScriptErrorFailsSafeRatherThanFallingThrough(t *testing.T) {
	repo := newFakeMockRepo()
	// Two candidates: a higher-priority one with a failing script, and a
	// lower-priority one that would otherwise match fine. A script error on
	// the higher-priority candidate must stop the search (fail safe), not
	// silently fall through to the lower-priority one.
	if err := repo.CreateMock(context.Background(), mockWithScript("broken", 10, "throw new Error('boom')")); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}
	if err := repo.CreateMock(context.Background(), mockWithScript("fallback", 1, "")); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}

	scriptErr := errors.New("boom")
	uc := NewMatchRequest(repo, &fakeSeededSource{}, alwaysMatchEval{}, scriptedEval{matchErr: scriptErr})
	got, matched, err := uc.Execute(context.Background(), "default", MatchInput{})
	if matched {
		t.Fatal("Execute() matched, want false — a script error must fail safe, not match")
	}
	var serr *ScriptError
	if !errors.As(err, &serr) {
		t.Fatalf("Execute() err = %v, want a *ScriptError", err)
	}
	if serr.MockID != "broken" || serr.Phase != "match" {
		t.Errorf("ScriptError = %+v, want MockID=broken Phase=match", serr)
	}
	if got.ID != "broken" {
		t.Errorf("Execute() returned mock %+v, want the mock whose script failed (for traffic recording)", got)
	}
}

func TestBuildRespondOutputWithScriptUsesActionStatusAndHeaders(t *testing.T) {
	action := domain.RespondAction{Status: 201, Headers: map[string]string{"X-Test": "1"}}
	script := &domain.Script{RespondSrc: "ignored by the fake"}
	se := scriptRespondEval{body: []byte(`{"ok":true}`)}

	status, headers, body, err := BuildRespondOutputWithScript(action, script, MatchInput{}, nil, se)
	if err != nil {
		t.Fatalf("BuildRespondOutputWithScript(): %v", err)
	}
	if status != 201 || headers["X-Test"] != "1" || string(body) != `{"ok":true}` {
		t.Errorf("got status=%d headers=%v body=%s, want status=201 header X-Test=1 body from script", status, headers, body)
	}
}

func TestBuildRespondOutputWithScriptPropagatesError(t *testing.T) {
	action := domain.RespondAction{Status: 200}
	script := &domain.Script{RespondSrc: "boom"}
	wantErr := errors.New("script exploded")
	se := scriptRespondEval{err: wantErr}

	_, _, _, err := BuildRespondOutputWithScript(action, script, MatchInput{}, nil, se)
	if !errors.Is(err, wantErr) {
		t.Fatalf("BuildRespondOutputWithScript() err = %v, want %v", err, wantErr)
	}
}

func TestBuildRespondOutputWithScriptFallsBackWhenNoRespondSrc(t *testing.T) {
	action := domain.RespondAction{Status: 200, Body: []byte("static")}
	status, _, body, err := BuildRespondOutputWithScript(action, nil, MatchInput{}, noopTemplater{}, scriptRespondEval{})
	if err != nil {
		t.Fatalf("BuildRespondOutputWithScript(): %v", err)
	}
	if status != 200 || string(body) != "static" {
		t.Errorf("got status=%d body=%s, want the unmodified static action", status, body)
	}
}

type scriptRespondEval struct {
	body []byte
	err  error
}

func (s scriptRespondEval) EvalRespond(_ string, _ MatchInput) ([]byte, error) { return s.body, s.err }

type noopTemplater struct{}

func (noopTemplater) Render(body []byte, _ MatchInput) []byte                           { return body }
func (noopTemplater) RenderHeaders(h map[string]string, _ MatchInput) map[string]string { return h }
