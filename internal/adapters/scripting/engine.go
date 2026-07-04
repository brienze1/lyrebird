// Package scripting implements usecase.ScriptEval: sandboxed JavaScript
// execution (goja) for a mock's match_src/respond_src hooks (FR-014),
// bounded so a misbehaving script can never hang or crash the server
// (FR-016/SC-010).
//
// Known accepted scope boundary: goja exposes no memory/heap limit API —
// only SetMaxCallStackSize (recursion depth) and an execution timeout via
// Interrupt. FR-016 is satisfied for time-bounded and stack-depth
// misbehavior; an unbounded single huge allocation within one VM step
// (e.g. a giant array literal) is not guarded against here and would need
// a separate watchdog if ever required.
package scripting

import (
	"errors"
	"fmt"
	"time"

	"github.com/dop251/goja"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// maxCallStackSize bounds JS call-stack depth (goja's one concrete
// "memory guard" primitive) — recursion beyond this throws a catchable
// *goja.StackOverflowError rather than exhausting the host process's stack.
const maxCallStackSize = 512

// Engine implements usecase.ScriptEval. A fresh goja.Runtime is constructed
// for every single evaluation and discarded afterward — deliberately NOT
// pooled/reused.
//
// This was a real, reproduced bug: a reused Runtime's global JS object
// persists across invocations, and JS has no reliable way to fully reset
// it between "unrelated" uses. A script's own top-level `var`/`let`/`const`
// declarations always become properties of that persistent global object
// regardless of strict mode (strict mode only blocks *implicit*,
// undeclared-identifier globals — it does nothing for an explicit
// `var leaked = req.body`), so mock A's script could leak its request body,
// or overwrite a native global like faker.name, into mock B's completely
// unrelated evaluation on a later request that happened to draw the same
// pooled Runtime. That is a real cross-tenant data leak, not a theoretical
// one — sandboxing correctness matters more here than the (real but
// small) cost of constructing a Runtime per call, which for goja — a pure
// bytecode interpreter with no JIT warmup — is on the order of
// microseconds, not milliseconds.
type Engine struct {
	timeout time.Duration
}

// New builds a scripting Engine bounding every script to timeout (a
// non-positive value defaults to 100ms).
func New(timeout time.Duration) *Engine {
	if timeout <= 0 {
		timeout = 100 * time.Millisecond
	}
	return &Engine{timeout: timeout}
}

// ValidateScript reports whether src compiles as well-formed JS, without
// executing it. src == "" is always valid (no script attached).
func (e *Engine) ValidateScript(src string) error {
	if src == "" {
		return nil
	}
	if _, err := goja.Compile("script", src, false); err != nil {
		return fmt.Errorf("%w: script does not compile: %w", domain.ErrInvalidMock, err)
	}
	return nil
}

// EvalMatch runs src (match_src) and reports its last-evaluated
// expression's JS truthiness.
func (e *Engine) EvalMatch(src string, in usecase.MatchInput) (bool, error) {
	v, err := e.run(src, in)
	if err != nil {
		return false, err
	}
	return v.ToBoolean(), nil
}

// EvalRespond runs src (respond_src) and returns the response body it
// produced.
func (e *Engine) EvalRespond(src string, in usecase.MatchInput) ([]byte, error) {
	v, err := e.run(src, in)
	if err != nil {
		return nil, err
	}
	return valueToBody(v)
}

var errScriptTimeout = errors.New("script exceeded execution timeout")

// run builds a fresh Runtime (see the Engine doc comment for why it isn't
// pooled), sets req, and executes src bounded by e.timeout. Because the
// Runtime is discarded immediately after this single use, there is no
// "next caller" a delayed Interrupt() could ever poison — the interrupt
// timer is simply stopped best-effort on the way out.
func (e *Engine) run(src string, in usecase.MatchInput) (goja.Value, error) {
	vm := newRuntime()
	_ = vm.Set("req", reqToJS(in))

	timer := time.AfterFunc(e.timeout, func() { vm.Interrupt(errScriptTimeout) })
	v, err := vm.RunString(src)
	timer.Stop()

	if err != nil {
		var ie *goja.InterruptedError
		if errors.As(err, &ie) {
			return nil, fmt.Errorf("scripting: script exceeded %s timeout", e.timeout)
		}
		return nil, fmt.Errorf("scripting: %w", err)
	}
	return v, nil
}
