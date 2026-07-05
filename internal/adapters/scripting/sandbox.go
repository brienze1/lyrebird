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

// respToJS builds the per-invocation "resp" global exposed to
// transform_response scripts, alongside the usual "req" (so a transform can
// still reference the original request that produced this response).
func respToJS(in usecase.TransformInput) map[string]any {
	return map[string]any{
		"status":  in.Status,
		"headers": firstOrSlice(in.Headers),
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
	return anyToBody(v.Export())
}

// anyToBody is valueToBody's exported-value half, reused by
// jsValueToRewrite/jsValueToTransform for a rewrite/transform script's body
// field — same "string verbatim, else JSON-marshal" contract.
func anyToBody(exported any) ([]byte, error) {
	if exported == nil {
		return nil, nil
	}
	if s, ok := exported.(string); ok {
		return []byte(s), nil
	}
	b, err := json.Marshal(exported)
	if err != nil {
		return nil, fmt.Errorf("scripting: script returned a non-JSON-encodable value: %w", err)
	}
	return b, nil
}

// toHeaderMap converts a JS-exported "headers" object (map[string]any, one
// entry per header) into RewrittenRequest/TransformedResponse's
// map[string][]string shape: a JS null value becomes an explicit nil slice
// (the "delete this header" sentinel — a real header always has at least
// one non-nil value, so nil is unambiguous); a string becomes a
// single-element slice; a JS array becomes a multi-element slice (each
// element stringified via fmt.Sprint, matching how a script would naturally
// build one from req.headers's own array shape). raw itself being nil (the
// "headers" field is present but its value round-tripped to JS null or
// undefined) is a distinct case from a wrong-shaped value: per
// RewrittenRequest/TransformedResponse's own doc comment, a nil Headers map
// already means "no header changes," and jsValueToRewrite's top-level
// undefined/null already means "no changes" the same way — so
// headers: null/undefined is treated as that same no-op, not an error,
// returning (nil, true). ok is false only when raw is present but isn't a
// plain object AND isn't nil (e.g. a script returned headers: [...] or
// headers: "x"), so the caller can tell a genuinely malformed value apart
// from both "headers wasn't set" and "headers was set to null/undefined."
func toHeaderMap(raw any) (out map[string][]string, ok bool) {
	if raw == nil {
		return nil, true
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	out = make(map[string][]string, len(obj))
	for k, v := range obj {
		switch val := v.(type) {
		case nil:
			out[k] = nil
		case string:
			out[k] = []string{val}
		case []any:
			vals := make([]string, len(val))
			for i, e := range val {
				vals[i] = fmt.Sprint(e)
			}
			out[k] = vals
		default:
			out[k] = []string{fmt.Sprint(val)}
		}
	}
	return out, true
}

// jsNumberToInt accepts either of goja's two possible number export types
// (int64 for integer-valued JS numbers, float64 otherwise — which one
// depends on internal representation, not on script syntax) and reports
// whether v was a number at all.
func jsNumberToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// jsValueToRewrite reads a rewrite_request script's returned value back
// into a usecase.RewrittenRequest. undefined/null means "no changes" (the
// zero value); anything else must export to a plain JS object or it's a
// script-authoring error, reported like any other scripting failure.
func jsValueToRewrite(v goja.Value) (usecase.RewrittenRequest, error) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return usecase.RewrittenRequest{}, nil
	}
	obj, ok := v.Export().(map[string]any)
	if !ok {
		return usecase.RewrittenRequest{}, fmt.Errorf("scripting: rewrite_request must return an object (or nothing), got %T", v.Export())
	}
	var out usecase.RewrittenRequest
	if m, ok := obj["method"].(string); ok {
		out.Method = &m
	}
	if p, ok := obj["path"].(string); ok {
		out.Path = &p
	}
	if h, present := obj["headers"]; present {
		headers, ok := toHeaderMap(h)
		if !ok {
			return usecase.RewrittenRequest{}, fmt.Errorf("scripting: rewrite_request's \"headers\" must be an object, got %T", h)
		}
		out.Headers = headers
	}
	if b, present := obj["body"]; present {
		body, err := anyToBody(b)
		if err != nil {
			return usecase.RewrittenRequest{}, err
		}
		out.Body, out.BodySet = body, true
	}
	return out, nil
}

// jsValueToTransform mirrors jsValueToRewrite for transform_response's
// status/headers/body.
func jsValueToTransform(v goja.Value) (usecase.TransformedResponse, error) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return usecase.TransformedResponse{}, nil
	}
	obj, ok := v.Export().(map[string]any)
	if !ok {
		return usecase.TransformedResponse{}, fmt.Errorf("scripting: transform_response must return an object (or nothing), got %T", v.Export())
	}
	var out usecase.TransformedResponse
	if status, ok := jsNumberToInt(obj["status"]); ok {
		out.Status = &status
	}
	if h, present := obj["headers"]; present {
		headers, ok := toHeaderMap(h)
		if !ok {
			return usecase.TransformedResponse{}, fmt.Errorf("scripting: transform_response's \"headers\" must be an object, got %T", h)
		}
		out.Headers = headers
	}
	if b, present := obj["body"]; present {
		body, err := anyToBody(b)
		if err != nil {
			return usecase.TransformedResponse{}, err
		}
		out.Body, out.BodySet = body, true
	}
	return out, nil
}
