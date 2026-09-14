package usecase

import (
	"context"
	"errors"
	"net/textproto"
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
func (s scriptedEval) EvalRewriteRequest(_ string, _ MatchInput) (RewrittenRequest, error) {
	return RewrittenRequest{}, nil
}
func (s scriptedEval) EvalTransformResponse(_ string, _ TransformInput) (TransformedResponse, error) {
	return TransformedResponse{}, nil
}

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

	uc := NewMatchRequest(repo, &fakeSeededSource{}, alwaysMatchEval{}, scriptedEval{matchResult: true}, &fakeScenarioStateRepo{})
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

	uc := NewMatchRequest(repo, &fakeSeededSource{}, alwaysMatchEval{}, scriptedEval{matchResult: false}, &fakeScenarioStateRepo{})
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
	uc := NewMatchRequest(repo, &fakeSeededSource{}, alwaysMatchEval{}, scriptedEval{matchErr: scriptErr}, &fakeScenarioStateRepo{})
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

func TestMatchRequestSkipsExhaustedFallthroughScenarioCandidate(t *testing.T) {
	repo := newFakeMockRepo()
	high := domain.Mock{
		ID: "exhausted", Partition: "default", Priority: 10, CreatedAt: time.Unix(10, 0),
		Action:   domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200}},
		Scenario: &domain.Scenario{Responses: []domain.RespondAction{{Body: []byte("one")}}, OnExhaust: domain.OnExhaustFallthrough},
	}
	low := domain.Mock{
		ID: "fallback", Partition: "default", Priority: 1, CreatedAt: time.Unix(1, 0),
		Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200}},
	}
	if err := repo.CreateMock(context.Background(), high); err != nil {
		t.Fatalf("CreateMock(high): %v", err)
	}
	if err := repo.CreateMock(context.Background(), low); err != nil {
		t.Fatalf("CreateMock(low): %v", err)
	}

	scenario := &fakeScenarioStateRepo{indexes: map[string]int{"default/exhausted": 1}} // already consumed its only response
	uc := NewMatchRequest(repo, &fakeSeededSource{}, alwaysMatchEval{}, scriptedEval{matchResult: true}, scenario)

	got, matched, err := uc.Execute(context.Background(), "default", MatchInput{})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !matched || got.ID != "fallback" {
		t.Fatalf("Execute() = (%+v, %v), want the exhausted fallthrough candidate skipped in favor of fallback", got, matched)
	}
}

func TestMatchRequestDoesNotSkipRepeatLastScenarioEvenWhenExhausted(t *testing.T) {
	repo := newFakeMockRepo()
	m := domain.Mock{
		ID: "repeater", Partition: "default", Priority: 10, CreatedAt: time.Unix(10, 0),
		Action:   domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200}},
		Scenario: &domain.Scenario{Responses: []domain.RespondAction{{Body: []byte("one")}}, OnExhaust: domain.OnExhaustRepeatLast},
	}
	if err := repo.CreateMock(context.Background(), m); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}

	scenario := &fakeScenarioStateRepo{indexes: map[string]int{"default/repeater": 5}}
	uc := NewMatchRequest(repo, &fakeSeededSource{}, alwaysMatchEval{}, scriptedEval{matchResult: true}, scenario)

	got, matched, err := uc.Execute(context.Background(), "default", MatchInput{})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !matched || got.ID != "repeater" {
		t.Fatalf("Execute() = (%+v, %v), want repeat_last to keep matching even once exhausted", got, matched)
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

// bodyEqualsEval matches only when the candidate is evaluated against a body
// equal to want — the minimum needed to prove ExecuteProjected really gives
// each candidate the body its own projector produced, rather than one shared
// body for all of them.
type bodyEqualsEval struct{ want string }

func (e bodyEqualsEval) Matches(_ domain.Match, in MatchInput) (bool, []ConditionResult) {
	return string(in.Body) == e.want, nil
}
func (bodyEqualsEval) ValidateMatch(_ domain.Match) error { return nil }

// perMockProjector hands each mock its own body, keyed by mock id, and
// returns an error for any id in failFor.
type perMockProjector struct {
	bodies  map[string]string
	failFor map[string]bool
}

func (p perMockProjector) ProjectFor(m domain.Mock) ([]byte, error) {
	if p.failFor[m.ID] {
		return nil, errors.New("this rule's projection cannot read these bytes")
	}
	return []byte(p.bodies[m.ID]), nil
}

func TestExecuteProjectedGivesEachCandidateItsOwnBody(t *testing.T) {
	repo := newFakeMockRepo()
	// Higher priority wins the ordering, so "loser" is tried first and must
	// genuinely fail on ITS body before "winner" gets a turn.
	for _, m := range []domain.Mock{
		{ID: "loser", Partition: "default", Priority: 2, CreatedAt: time.Unix(2, 0)},
		{ID: "winner", Partition: "default", Priority: 1, CreatedAt: time.Unix(1, 0)},
	} {
		if err := repo.CreateMock(context.Background(), m); err != nil {
			t.Fatalf("CreateMock(): %v", err)
		}
	}

	uc := NewMatchRequest(repo, &fakeSeededSource{}, bodyEqualsEval{want: "WINNER-VIEW"},
		scriptedEval{matchResult: true}, &fakeScenarioStateRepo{})
	projector := perMockProjector{bodies: map[string]string{
		"loser":  "LOSER-VIEW",
		"winner": "WINNER-VIEW",
	}}

	got, in, matched, err := uc.ExecuteProjected(context.Background(), "default", MatchInput{}, projector)
	if err != nil {
		t.Fatalf("ExecuteProjected(): %v", err)
	}
	if !matched || got.ID != "winner" {
		t.Fatalf("matched %q (%v), want the candidate whose own projection matched", got.ID, matched)
	}
	// The returned MatchInput must be the one the winner was evaluated
	// against, so the caller builds its answer (and resolves any copyFrom)
	// from the same projection the match used.
	if string(in.Body) != "WINNER-VIEW" {
		t.Errorf("returned MatchInput.Body = %q, want the winning candidate's own body", in.Body)
	}
}

// A projector error means THIS rule cannot read THESE bytes — that candidate
// has simply not matched. Failing the whole frame would let one bad rule
// silence every other rule on the endpoint.
func TestExecuteProjectedSkipsACandidateWhoseProjectionFails(t *testing.T) {
	repo := newFakeMockRepo()
	for _, m := range []domain.Mock{
		{ID: "broken", Partition: "default", Priority: 2, CreatedAt: time.Unix(2, 0)},
		{ID: "good", Partition: "default", Priority: 1, CreatedAt: time.Unix(1, 0)},
	} {
		if err := repo.CreateMock(context.Background(), m); err != nil {
			t.Fatalf("CreateMock(): %v", err)
		}
	}

	uc := NewMatchRequest(repo, &fakeSeededSource{}, bodyEqualsEval{want: "OK"},
		scriptedEval{matchResult: true}, &fakeScenarioStateRepo{})
	projector := perMockProjector{
		bodies:  map[string]string{"good": "OK"},
		failFor: map[string]bool{"broken": true},
	}

	got, _, matched, err := uc.ExecuteProjected(context.Background(), "default", MatchInput{}, projector)
	if err != nil {
		t.Fatalf("ExecuteProjected(): %v", err)
	}
	if !matched || got.ID != "good" {
		t.Errorf("matched %q (%v), want the lower-priority rule that could read the bytes", got.ID, matched)
	}
}

// A nil projector means "every candidate sees the base body" — the same
// semantics Execute has, so the two can never disagree for a plane that has
// nothing per-rule to project.
func TestExecuteProjectedWithNoProjectorUsesTheBaseBody(t *testing.T) {
	repo := newFakeMockRepo()
	if err := repo.CreateMock(context.Background(), domain.Mock{ID: "only", Partition: "default"}); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}

	uc := NewMatchRequest(repo, &fakeSeededSource{}, bodyEqualsEval{want: "BASE"},
		scriptedEval{matchResult: true}, &fakeScenarioStateRepo{})

	got, in, matched, err := uc.ExecuteProjected(context.Background(), "default", MatchInput{Body: []byte("BASE")}, nil)
	if err != nil {
		t.Fatalf("ExecuteProjected(): %v", err)
	}
	if !matched || got.ID != "only" || string(in.Body) != "BASE" {
		t.Errorf("ExecuteProjected() = (%q, %q, %v), want the base body used unchanged", got.ID, in.Body, matched)
	}
}

// A script failure must still come back with the offending mock attached, so
// a caller can record which rule blew up — the same contract Execute has.
func TestExecuteProjectedPropagatesAScriptErrorWithItsMock(t *testing.T) {
	repo := newFakeMockRepo()
	if err := repo.CreateMock(context.Background(), mockWithScript("scripted", 1, "boom()")); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}

	uc := NewMatchRequest(repo, &fakeSeededSource{}, alwaysMatchEval{},
		scriptedEval{matchErr: errors.New("boom")}, &fakeScenarioStateRepo{})

	got, _, matched, err := uc.ExecuteProjected(context.Background(), "default", MatchInput{}, nil)
	var se *ScriptError
	if !errors.As(err, &se) {
		t.Fatalf("ExecuteProjected() err = %v, want a *ScriptError", err)
	}
	if matched || got.ID != "scripted" || se.MockID != "scripted" {
		t.Errorf("ExecuteProjected() = (%q, %v), want the offending mock returned unmatched", got.ID, matched)
	}
}

// TestCadenceActionMocksAreInvisibleToOrdinaryFrameMatching is CB5-11
// correction round 1's regression: a cadence-action mock has no respond/
// fault body to serve an ordinary inbound frame with, so it must be SKIPPED
// as a candidate for Execute/ExecuteProjected — not merely matched-and-then-
// mishandled by the caller — so a lower-priority respond/fault mock on the
// same path still gets its turn. Confirmed root cause of CB5-11's real-stack
// symptom 2 ("any SUBSEQUENT connection's own GPS boot fails outright" while
// a cadence override exists): a priority-100 override with no body condition
// matched (and thereby swallowed) every inbound frame on cb5/gps, including
// the firmware's own handshake, because candidateMatches never excluded
// ActionCadence from ordinary resolution.
func TestCadenceActionMocksAreInvisibleToOrdinaryFrameMatching(t *testing.T) {
	repo := newFakeMockRepo()
	cadenceOverride := domain.Mock{
		ID: "override", Partition: "default", Priority: 100, CreatedAt: time.Unix(2, 0),
		Match: domain.Match{Method: domain.StreamMethod, Path: "/gps"},
		Action: domain.Action{Kind: domain.ActionCadence, Cadence: &domain.CadenceAction{
			Frames: [][]domain.FramePart{{{Text: ptrString("OVERRIDE")}}},
		}},
	}
	respond := domain.Mock{
		ID: "handshake", Partition: "default", Priority: 0, CreatedAt: time.Unix(1, 0),
		Match:  domain.Match{Method: domain.StreamMethod, Path: "/gps"},
		Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Body: []byte("PONG")}},
	}
	if err := repo.CreateMock(context.Background(), cadenceOverride); err != nil {
		t.Fatalf("CreateMock(override): %v", err)
	}
	if err := repo.CreateMock(context.Background(), respond); err != nil {
		t.Fatalf("CreateMock(respond): %v", err)
	}

	uc := NewMatchRequest(repo, &fakeSeededSource{}, alwaysMatchEval{}, scriptedEval{}, &fakeScenarioStateRepo{})
	got, matched, err := uc.Execute(context.Background(), "default", MatchInput{Method: domain.StreamMethod, Path: "/gps"})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !matched || got.ID != "handshake" {
		t.Fatalf("Execute() = (%+v, %v), want the lower-priority respond mock \"handshake\" to win "+
			"(the higher-priority cadence-action mock must be invisible here)", got, matched)
	}

	// ExecuteProjected must agree — it is the path the byte-stream plane
	// actually calls for every inbound frame.
	got, _, matched, err = uc.ExecuteProjected(context.Background(), "default",
		MatchInput{Method: domain.StreamMethod, Path: "/gps"}, nil)
	if err != nil {
		t.Fatalf("ExecuteProjected(): %v", err)
	}
	if !matched || got.ID != "handshake" {
		t.Fatalf("ExecuteProjected() = (%+v, %v), want \"handshake\" to win", got, matched)
	}
}

// headerEqualsEval evaluates only a Match's header conditions, using the
// same canonicalised lookup matcher.Engine performs in production
// (textproto.CanonicalMIMEHeaderKey) — the minimum real semantics needed to
// prove an emission-opt-in rule's header condition genuinely excludes it,
// rather than a fake that always agrees the way alwaysMatchEval does.
type headerEqualsEval struct{}

func (headerEqualsEval) Matches(m domain.Match, in MatchInput) (bool, []ConditionResult) {
	for name, matcher := range m.Headers {
		vs, ok := in.Header[textproto.CanonicalMIMEHeaderKey(name)]
		if !ok || len(vs) == 0 {
			return false, nil
		}
		if matcher.Equals != nil && vs[0] != *matcher.Equals {
			return false, nil
		}
	}
	return true, nil
}
func (headerEqualsEval) ValidateMatch(_ domain.Match) error { return nil }

// TestEmissionOptInHeaderDoesNotWinOrdinaryFrameResolution is CB5-15 WI-04's
// sibling of TestCadenceActionMocksAreInvisibleToOrdinaryFrameMatching: an
// emission-opt-in rule (an ordinary respond rule distinguished only by the
// synthesized X-Lyrebird-Stream-Direction header condition) must not win
// resolution for an INBOUND frame, whose MatchInput.Header is the
// connection's own unmodified handshake header and therefore never carries
// the marker. The CB5-11 lesson this guards against: a rule that exists must
// never change how an ordinary frame resolves for a consumer who never opted
// into it.
func TestEmissionOptInHeaderDoesNotWinOrdinaryFrameResolution(t *testing.T) {
	repo := newFakeMockRepo()
	emissionRule := domain.Mock{
		ID: "emission-rule", Partition: "default", Priority: 100, CreatedAt: time.Unix(2, 0),
		Match: domain.Match{
			Method:  domain.StreamMethod,
			Path:    "/cb5/spp",
			Headers: map[string]domain.Matcher{"x-lyrebird-stream-direction": {Equals: ptrString("EMIT")}},
		},
		Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Body: []byte("EMISSION-ANSWER")}},
	}
	handshakeReply := domain.Mock{
		ID: "handshake", Partition: "default", Priority: 0, CreatedAt: time.Unix(1, 0),
		Match:  domain.Match{Method: domain.StreamMethod, Path: "/cb5/spp"},
		Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Body: []byte("PONG")}},
	}
	if err := repo.CreateMock(context.Background(), emissionRule); err != nil {
		t.Fatalf("CreateMock(emissionRule): %v", err)
	}
	if err := repo.CreateMock(context.Background(), handshakeReply); err != nil {
		t.Fatalf("CreateMock(handshakeReply): %v", err)
	}

	uc := NewMatchRequest(repo, &fakeSeededSource{}, headerEqualsEval{}, scriptedEval{}, &fakeScenarioStateRepo{})

	// An ordinary inbound frame: no synthesized emission marker, exactly
	// what handleFrame builds from c.header.
	in := MatchInput{Method: domain.StreamMethod, Path: "/cb5/spp", Header: map[string][]string{"Space": {"default"}}}

	got, matched, err := uc.Execute(context.Background(), "default", in)
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !matched || got.ID != "handshake" {
		t.Fatalf("Execute() = (%+v, %v), want the ordinary respond mock \"handshake\" to win "+
			"(the higher-priority emission-opt-in rule must be invisible to an inbound frame lacking the marker)", got, matched)
	}

	got, _, matched, err = uc.ExecuteProjected(context.Background(), "default", in, nil)
	if err != nil {
		t.Fatalf("ExecuteProjected(): %v", err)
	}
	if !matched || got.ID != "handshake" {
		t.Fatalf("ExecuteProjected() = (%+v, %v), want \"handshake\" to win", got, matched)
	}
}
