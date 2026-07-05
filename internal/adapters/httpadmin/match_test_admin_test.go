package httpadmin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type fakeMatchTester struct {
	out          usecase.MatchTestOutput
	err          error
	gotPartition string
	gotInput     usecase.MatchInput
}

func (f *fakeMatchTester) Execute(_ context.Context, partition string, in usecase.MatchInput) (usecase.MatchTestOutput, error) {
	f.gotPartition = partition
	f.gotInput = in
	return f.out, f.err
}

func TestMatchTestReturnsDecodedCandidatesAndWinner(t *testing.T) {
	winner := domain.Mock{ID: "m1", Name: "winner-mock"}
	uc := &fakeMatchTester{out: usecase.MatchTestOutput{
		Candidates: []usecase.CandidateResult{
			{Mock: winner, Matched: true, Conditions: []usecase.ConditionResult{{Field: "method", Expected: "GET", Actual: "GET", Passed: true}}},
		},
		Winner:  &winner,
		Status:  200,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    []byte(`{"ok":true}`),
	}}
	rr := httptest.NewRecorder()
	body := `{"method":"GET","path":"/x"}`
	MatchTest(uc)(rr, newPostRequest(t, "/__lyrebird/match-test", body))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	candidates, _ := out["candidates"].([]any)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want one candidate", out["candidates"])
	}
	winnerOut, _ := out["winner"].(map[string]any)
	if winnerOut == nil || winnerOut["id"] != "m1" {
		t.Errorf("winner = %+v, want id=m1", out["winner"])
	}
	if v, _ := out["status"].(float64); int(v) != 200 {
		t.Errorf("status = %v, want 200", out["status"])
	}
	if out["body"] != `{"ok":true}` {
		t.Errorf("body = %v, want the resolved response body", out["body"])
	}
	headersOut, _ := out["headers"].(map[string]any)
	if headersOut["Content-Type"] != "application/json" {
		t.Errorf("headers = %+v, want Content-Type=application/json", out["headers"])
	}
}

func TestMatchTestCanonicalizesRequestHeaderKeys(t *testing.T) {
	uc := &fakeMatchTester{}
	rr := httptest.NewRecorder()
	body := `{"method":"GET","path":"/x","headers":{"x-vip":["true"]}}`
	MatchTest(uc)(rr, newPostRequest(t, "/__lyrebird/match-test", body))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	vals, ok := uc.gotInput.Header["X-Vip"]
	if !ok || len(vals) != 1 || vals[0] != "true" {
		t.Errorf("gotInput.Header = %+v, want canonicalized key X-Vip=[true] (dto.CanonicalizeHeaders not applied)", uc.gotInput.Header)
	}
	if _, rawKeyPresent := uc.gotInput.Header["x-vip"]; rawKeyPresent {
		t.Errorf("gotInput.Header = %+v, want the raw lowercase key removed after canonicalization", uc.gotInput.Header)
	}
}

func TestMatchTestRejectsMalformedJSONBody(t *testing.T) {
	uc := &fakeMatchTester{}
	rr := httptest.NewRecorder()
	MatchTest(uc)(rr, newPostRequest(t, "/__lyrebird/match-test", `not json`))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a malformed body", rr.Code)
	}
}

func TestMatchTestRejectsUnknownFieldInJSONBody(t *testing.T) {
	// MatchTest is a REST/MCP parity endpoint: match_test's MCP twin already
	// rejects unknown fields via the SDK's jsonschema AdditionalProperties:
	// false default, so this REST handler must reject them too rather than
	// silently ignoring a typo'd field (e.g. "metod" instead of "method").
	uc := &fakeMatchTester{}
	rr := httptest.NewRecorder()
	body := `{"method":"GET","path":"/x","totally_bogus_field":"oops"}`
	MatchTest(uc)(rr, newPostRequest(t, "/__lyrebird/match-test", body))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a body with an unknown field", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "unknown field") {
		t.Errorf("body = %q, want it to mention the unknown field", rr.Body.String())
	}
}

func TestMatchTestRejectsEmptyPOSTBody(t *testing.T) {
	// Unlike Reset/PromoteTraffic, MatchTest has no `ContentLength != 0`
	// guard: a bodiless POST hits json.Decode's EOF path directly, which is
	// a distinct code path worth covering on its own.
	uc := &fakeMatchTester{}
	rr := httptest.NewRecorder()
	MatchTest(uc)(rr, newBodilessPostRequest(t, "/__lyrebird/match-test"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an empty body (EOF from json.Decode)", rr.Code)
	}
}

func TestMatchTestMapsUseCaseErrorViaExplain(t *testing.T) {
	uc := &fakeMatchTester{err: domain.ErrNotFound}
	rr := httptest.NewRecorder()
	body := `{"method":"GET","path":"/x"}`
	MatchTest(uc)(rr, newPostRequest(t, "/__lyrebird/match-test", body))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a not-found use-case error", rr.Code)
	}
}
