package scripting

import (
	"encoding/json"

	"github.com/dop251/goja"

	"github.com/brienze1/lyrebird/internal/adapters/jsonpath"
)

// newJSONPath builds the sandbox's jsonpath(value, path) global. It reuses
// the exact path dialect already used by internal/adapters/matcher's
// body-JSONPath conditions and internal/adapters/template's
// {{request.body.<path>}} placeholders — one consistent path language
// across the product, not a second one.
func newJSONPath(vm *goja.Runtime) func(goja.Value, string) goja.Value {
	return func(value goja.Value, path string) goja.Value {
		b, err := json.Marshal(value.Export())
		if err != nil {
			return goja.Undefined()
		}
		r := jsonpath.GetBytes(b, path)
		if !r.Exists() {
			return goja.Undefined()
		}
		// r.Value() is always one of nil/bool/float64/string/
		// map[string]any/[]any — exactly what vm.ToValue already knows how
		// to wrap, so no further conversion is needed.
		return vm.ToValue(r.Value())
	}
}
