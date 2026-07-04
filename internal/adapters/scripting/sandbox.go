package scripting

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/dop251/goja"
	"github.com/google/uuid"

	"github.com/brienze1/lyrebird/internal/usecase"
)

// newRuntime builds a fresh sandboxed goja.Runtime: no filesystem, network,
// or environment access is ever wired in (FR-015 holds by construction — a
// bare goja.New() runtime has no such globals built in, unlike Node or a
// browser, so there is no opt-out path to accidentally leave enabled). A
// fresh Runtime is built for every single evaluation (see engine.go's doc
// comment for why), so these globals never need explicit resetting between
// uses — there is no "between uses" for one Runtime.
func newRuntime() *goja.Runtime {
	vm := goja.New()
	vm.SetMaxCallStackSize(maxCallStackSize)
	// Set's error return is only non-nil for a value goja can't convert at
	// all; none of these four literal/closure values can ever hit that.
	_ = vm.Set("uuid", uuid.NewString)
	_ = vm.Set("now", func() string { return time.Now().UTC().Format(time.RFC3339) })
	_ = vm.Set("faker", fakerAPI())
	_ = vm.Set("jsonpath", newJSONPath(vm))
	return vm
}

// reqToJS builds the per-invocation "req" global exposed to scripts:
// method/path/headers/query/body.
func reqToJS(in usecase.MatchInput) map[string]any {
	return map[string]any{
		"method":  in.Method,
		"path":    in.Path,
		"headers": firstOrSlice(in.Header),
		"query":   firstOrSlice(in.Query),
		"body":    parseBody(in.Body),
	}
}

// firstOrSlice collapses a single-valued header/query entry to a plain
// string (the common case, ergonomic in scripts as req.headers["X"]) while
// preserving multi-valued entries as an array.
func firstOrSlice(m map[string][]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if len(v) == 1 {
			out[k] = v[0]
		} else {
			out[k] = v
		}
	}
	return out
}

// parseBody implements sandbox_api.md's "parsed JSON if the content type
// allows, else raw text" as try-parse-then-fallback rather than a strict
// Content-Type check — behaviorally equivalent for well-formed inputs and
// simpler/more robust (works even when Content-Type is absent but the body
// happens to be JSON). A request with no body at all yields nil (JS null).
func parseBody(body []byte) any {
	if len(body) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(body, &v) == nil {
		return v
	}
	return string(body)
}

// valueToBody converts a script's returned value into response bytes: a
// returned JS string is used verbatim; anything else JSON-encodable
// (object/array/number/bool/null) is JSON-marshaled, which is what lets a
// script literally "return a response object" without every script needing
// to call JSON.stringify itself.
func valueToBody(v goja.Value) ([]byte, error) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, nil
	}
	exported := v.Export()
	if s, ok := exported.(string); ok {
		return []byte(s), nil
	}
	b, err := json.Marshal(exported)
	if err != nil {
		return nil, fmt.Errorf("scripting: respond_src returned a non-JSON-encodable value: %w", err)
	}
	return b, nil
}
