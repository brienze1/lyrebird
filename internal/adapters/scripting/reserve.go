// Package scripting will host the sandboxed JavaScript rule engine (goja)
// starting at M4 (specs/001-lyrebird/tasks.md). The blank import below keeps
// the dependency pinned in go.mod/go.sum so an intermediate `go mod tidy`
// between now and M4 cannot silently drop it.
package scripting

import (
	// Blank-imported to keep this dependency pinned in go.mod/go.sum until
	// the M4 rule engine actually uses it — see the package doc above.
	_ "github.com/dop251/goja"
)
