package scripting

import (
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/usecase"
)

func TestEvalRewriteRequestReturnsZeroValueWhenScriptChangesNothing(t *testing.T) {
	e := New(100 * time.Millisecond)
	got, err := e.EvalRewriteRequest(`undefined`, usecase.MatchInput{})
	if err != nil {
		t.Fatalf("EvalRewriteRequest(): %v", err)
	}
	if got.Method != nil || got.Path != nil || got.Headers != nil || got.BodySet {
		t.Errorf("got = %+v, want the zero value", got)
	}
}

func TestEvalRewriteRequestChangesMethodPathHeadersBody(t *testing.T) {
	e := New(100 * time.Millisecond)
	got, err := e.EvalRewriteRequest(
		`({method: "POST", path: "/rewritten", headers: {"X-Injected": "yes"}, body: "new body"})`,
		usecase.MatchInput{Method: "GET", Path: "/original"},
	)
	if err != nil {
		t.Fatalf("EvalRewriteRequest(): %v", err)
	}
	if got.Method == nil || *got.Method != "POST" {
		t.Errorf("Method = %v, want POST", got.Method)
	}
	if got.Path == nil || *got.Path != "/rewritten" {
		t.Errorf("Path = %v, want /rewritten", got.Path)
	}
	if len(got.Headers["X-Injected"]) != 1 || got.Headers["X-Injected"][0] != "yes" {
		t.Errorf("Headers = %+v, want X-Injected=yes", got.Headers)
	}
	if !got.BodySet || string(got.Body) != "new body" {
		t.Errorf("Body/BodySet = %q/%v, want %q/true", got.Body, got.BodySet, "new body")
	}
}

func TestEvalRewriteRequestNullHeaderMeansDelete(t *testing.T) {
	e := New(100 * time.Millisecond)
	got, err := e.EvalRewriteRequest(`({headers: {"X-Remove": null}})`, usecase.MatchInput{})
	if err != nil {
		t.Fatalf("EvalRewriteRequest(): %v", err)
	}
	v, present := got.Headers["X-Remove"]
	if !present || v != nil {
		t.Errorf("Headers[X-Remove] = %+v (present=%v), want nil (present, signaling deletion)", v, present)
	}
}

func TestEvalRewriteRequestRejectsANonObjectReturnValue(t *testing.T) {
	e := New(100 * time.Millisecond)
	if _, err := e.EvalRewriteRequest(`"just a string"`, usecase.MatchInput{}); err == nil {
		t.Fatal("EvalRewriteRequest() with a non-object return = nil error, want an error")
	}
}

func TestEvalTransformResponseSeesRespGlobalAndChangesBody(t *testing.T) {
	e := New(100 * time.Millisecond)
	got, err := e.EvalTransformResponse(
		`({body: JSON.stringify({wrapped: resp.body})})`,
		usecase.TransformInput{Status: 200, Body: []byte(`"real"`)},
	)
	if err != nil {
		t.Fatalf("EvalTransformResponse(): %v", err)
	}
	if !got.BodySet || string(got.Body) != `{"wrapped":"real"}` {
		t.Errorf("Body/BodySet = %s/%v, want {\"wrapped\":\"real\"}/true", got.Body, got.BodySet)
	}
}

func TestEvalTransformResponseChangesStatusAndHeaders(t *testing.T) {
	e := New(100 * time.Millisecond)
	got, err := e.EvalTransformResponse(
		`({status: 201, headers: {"X-Transformed": "yes"}})`,
		usecase.TransformInput{Status: 200},
	)
	if err != nil {
		t.Fatalf("EvalTransformResponse(): %v", err)
	}
	if got.Status == nil || *got.Status != 201 {
		t.Errorf("Status = %v, want 201", got.Status)
	}
	if len(got.Headers["X-Transformed"]) != 1 || got.Headers["X-Transformed"][0] != "yes" {
		t.Errorf("Headers = %+v, want X-Transformed=yes", got.Headers)
	}
}

func TestEvalTransformResponseReturnsZeroValueWhenScriptChangesNothing(t *testing.T) {
	e := New(100 * time.Millisecond)
	got, err := e.EvalTransformResponse(`null`, usecase.TransformInput{Status: 200})
	if err != nil {
		t.Fatalf("EvalTransformResponse(): %v", err)
	}
	if got.Status != nil || got.Headers != nil || got.BodySet {
		t.Errorf("got = %+v, want the zero value", got)
	}
}

func TestEvalRewriteRequestFailsSafeOnThrow(t *testing.T) {
	e := New(100 * time.Millisecond)
	if _, err := e.EvalRewriteRequest(`throw new Error('boom')`, usecase.MatchInput{}); err == nil {
		t.Fatal("EvalRewriteRequest() with a throwing script = nil error, want an error")
	}
}

func TestEvalTransformResponseFailsSafeOnThrow(t *testing.T) {
	e := New(100 * time.Millisecond)
	if _, err := e.EvalTransformResponse(`throw new Error('boom')`, usecase.TransformInput{}); err == nil {
		t.Fatal("EvalTransformResponse() with a throwing script = nil error, want an error")
	}
}
