package scripting

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

func TestEvalRespondReturnsStringVerbatim(t *testing.T) {
	e := New(100 * time.Millisecond)
	body, err := e.EvalRespond(`"hello"`, usecase.MatchInput{})
	if err != nil {
		t.Fatalf("EvalRespond(): %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("body = %q, want %q", body, "hello")
	}
}

func TestEvalRespondJSONEncodesNonStringValues(t *testing.T) {
	e := New(100 * time.Millisecond)
	body, err := e.EvalRespond(`({a: 1, b: "x"})`, usecase.MatchInput{})
	if err != nil {
		t.Fatalf("EvalRespond(): %v", err)
	}
	if string(body) != `{"a":1,"b":"x"}` {
		t.Errorf("body = %s, want {\"a\":1,\"b\":\"x\"}", body)
	}
}

func TestEvalRespondEchoesBodyField(t *testing.T) {
	e := New(100 * time.Millisecond)
	in := usecase.MatchInput{Body: []byte(`{"field":"alpha"}`)}
	body, err := e.EvalRespond(`({echoed: req.body.field})`, in)
	if err != nil {
		t.Fatalf("EvalRespond(): %v", err)
	}
	if string(body) != `{"echoed":"alpha"}` {
		t.Errorf("body = %s, want {\"echoed\":\"alpha\"}", body)
	}
}

func TestEvalMatchTruthiness(t *testing.T) {
	e := New(100 * time.Millisecond)
	cases := map[string]bool{
		`true`:                 true,
		`false`:                false,
		`req.method == "GET"`:  true,
		`req.method == "POST"`: false,
		`1`:                    true,
		`0`:                    false,
	}
	for src, want := range cases {
		ok, err := e.EvalMatch(src, usecase.MatchInput{Method: "GET"})
		if err != nil {
			t.Fatalf("EvalMatch(%q): %v", src, err)
		}
		if ok != want {
			t.Errorf("EvalMatch(%q) = %v, want %v", src, ok, want)
		}
	}
}

func TestValidateScriptAcceptsEmptyAndWellFormed(t *testing.T) {
	e := New(100 * time.Millisecond)
	for _, src := range []string{"", `"ok"`, `({a:1})`} {
		if err := e.ValidateScript(src); err != nil {
			t.Errorf("ValidateScript(%q) = %v, want nil", src, err)
		}
	}
}

func TestValidateScriptRejectsMalformedJS(t *testing.T) {
	e := New(100 * time.Millisecond)
	err := e.ValidateScript(`this is not valid javascript {{{`)
	if !errors.Is(err, domain.ErrInvalidMock) {
		t.Fatalf("ValidateScript(malformed) = %v, want ErrInvalidMock", err)
	}
}

func TestEvalRespondFailsSafeOnInfiniteLoop(t *testing.T) {
	e := New(30 * time.Millisecond)
	start := time.Now()
	_, err := e.EvalRespond(`while(true){}`, usecase.MatchInput{})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("EvalRespond(infinite loop) = nil error, want a timeout error")
	}
	if elapsed > 2*time.Second {
		t.Errorf("EvalRespond(infinite loop) took %v, want it bounded near the configured timeout", elapsed)
	}
}

// TestEngineReusableAfterTimeout guards that a timed-out evaluation never
// wedges the Engine for later, unrelated calls (each gets its own fresh
// Runtime, so there is nothing to "reuse" in the pooling sense — this
// pins the observable behavior: the Engine keeps working normally after a
// timeout, round after round).
func TestEngineReusableAfterTimeout(t *testing.T) {
	e := New(30 * time.Millisecond)
	for i := 0; i < 5; i++ {
		if _, err := e.EvalRespond(`while(true){}`, usecase.MatchInput{}); err == nil {
			t.Fatal("expected a timeout error")
		}
		body, err := e.EvalRespond(`"ok"`, usecase.MatchInput{})
		if err != nil {
			t.Fatalf("round %d: EvalRespond(ok) after a timeout: %v", i, err)
		}
		if string(body) != "ok" {
			t.Fatalf("round %d: body = %q, want %q", i, body, "ok")
		}
	}
}

func TestSandboxExposesNoFilesystemNetworkOrEnv(t *testing.T) {
	e := New(100 * time.Millisecond)
	body, err := e.EvalRespond(
		`({fetch: typeof fetch, proc: typeof process, req: typeof require, env: typeof process})`,
		usecase.MatchInput{},
	)
	if err != nil {
		t.Fatalf("EvalRespond(): %v", err)
	}
	for _, want := range []string{`"fetch":"undefined"`, `"proc":"undefined"`, `"req":"undefined"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("body = %s, want it to contain %q", body, want)
		}
	}
}

func TestSandboxJSONPathAndFaker(t *testing.T) {
	e := New(100 * time.Millisecond)
	in := usecase.MatchInput{Body: []byte(`{"user":{"name":"x"}}`)}

	body, err := e.EvalRespond(`({found: jsonpath(req.body, "user.name")})`, in)
	if err != nil {
		t.Fatalf("EvalRespond(jsonpath): %v", err)
	}
	if string(body) != `{"found":"x"}` {
		t.Errorf("body = %s, want {\"found\":\"x\"}", body)
	}

	body, err = e.EvalRespond(`({name: faker.name(), email: faker.email(), id: uuid(), now: typeof now()})`, usecase.MatchInput{})
	if err != nil {
		t.Fatalf("EvalRespond(faker/uuid/now): %v", err)
	}
	if strings.Contains(string(body), `""`) {
		t.Errorf("body = %s, want no empty-string fields from faker/uuid", body)
	}
}

func TestReqBodyIsNullWhenRequestHasNoBody(t *testing.T) {
	e := New(100 * time.Millisecond)
	body, err := e.EvalRespond(`({isNull: req.body === null})`, usecase.MatchInput{})
	if err != nil {
		t.Fatalf("EvalRespond(): %v", err)
	}
	if string(body) != `{"isNull":true}` {
		t.Errorf("body = %s, want {\"isNull\":true}", body)
	}
}

func TestAccessingFieldOnNullBodyIsAScriptError(t *testing.T) {
	e := New(100 * time.Millisecond)
	_, err := e.EvalRespond(`req.body.field`, usecase.MatchInput{}) // no body set
	if err == nil {
		t.Fatal("EvalRespond(req.body.field) with no body, want a script error (TypeError), got nil")
	}
}

// TestEngineConcurrentUse exercises many concurrent evaluations at once
// (run with -race) — a mix of well-behaved and timing-out scripts across
// many goroutines. Since each call gets its own fresh Runtime (see the
// Engine doc comment), there is no shared-state hazard to guard against
// here beyond ordinary goroutine safety, but this still pins that the
// Engine handles real concurrent load without error.
func TestEngineConcurrentUse(t *testing.T) {
	e := New(20 * time.Millisecond)
	const n = 50
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			if i%5 == 0 {
				_, _ = e.EvalRespond(`while(true){}`, usecase.MatchInput{})
				return
			}
			body, err := e.EvalRespond(`"ok"`, usecase.MatchInput{})
			if err != nil || string(body) != "ok" {
				t.Errorf("goroutine %d: EvalRespond() = (%q, %v), want (\"ok\", nil)", i, body, err)
			}
		}(i)
	}
	for i := 0; i < n; i++ {
		<-done
	}
}

// TestNoGlobalStateLeaksBetweenUnrelatedEvaluations is a regression test
// for a real, reproduced bug: a pooled/reused goja.Runtime lets one
// script's top-level var/let/const declarations and native-global
// mutations persist and become visible to a later, completely unrelated
// evaluation. Every eval must get a Runtime with no memory of any prior
// one — this pins that guarantee for both an implicit global assignment
// (which a "use strict" alone would not have caught, since strict mode
// only blocks *undeclared* identifier assignment, not an explicit `var`)
// and mutation of a native global (faker.name).
func TestNoGlobalStateLeaksBetweenUnrelatedEvaluations(t *testing.T) {
	e := New(100 * time.Millisecond)

	t.Run("explicit var declaration does not leak a later request's data", func(t *testing.T) {
		secretIn := usecase.MatchInput{Body: []byte(`{"secret":"top-secret-value"}`)}
		if _, err := e.EvalRespond(`var leaked = req.body; "ok"`, secretIn); err != nil {
			t.Fatalf("first eval: %v", err)
		}
		body, err := e.EvalRespond(`({leaked: typeof leaked})`, usecase.MatchInput{Body: []byte(`{}`)})
		if err != nil {
			t.Fatalf("second eval: %v", err)
		}
		if string(body) != `{"leaked":"undefined"}` {
			t.Fatalf("second eval saw %s, want no trace of the first eval's \"leaked\" variable", body)
		}
	})

	t.Run("mutating a native global does not affect a later unrelated evaluation", func(t *testing.T) {
		if _, err := e.EvalRespond(`faker.name = function(){ return "PWNED" }; "ok"`, usecase.MatchInput{}); err != nil {
			t.Fatalf("first eval: %v", err)
		}
		body, err := e.EvalRespond(`({name: faker.name()})`, usecase.MatchInput{})
		if err != nil {
			t.Fatalf("second eval: %v", err)
		}
		if strings.Contains(string(body), "PWNED") {
			t.Fatalf("second eval saw %s, want faker.name() unaffected by the first eval's mutation", body)
		}
	})
}
