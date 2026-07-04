package scripting

import (
	"math/rand"
	"strings"
)

// fakerAPI returns explicit lowercase-keyed functions (faker.name(),
// faker.email()) rather than relying on goja's Go-struct-method wrapping,
// which would use exported-method casing instead — the sandbox_api.md
// content resource commits to these exact names. No new third-party
// dependency: a small curated word list plus math/rand is enough for "a
// small set of realistic fake-data generators". Not security-sensitive
// (fixture data, not secrets), so math/rand is fine here.
func fakerAPI() map[string]any {
	return map[string]any{
		"name":  fakeName,
		"email": fakeEmail,
	}
}

var firstNames = []string{"Alice", "Bob", "Carol", "Dave", "Erin", "Frank"}
var lastNames = []string{"Smith", "Jones", "Lee", "Garcia", "Patel", "Kim"}

func fakeName() string {
	return firstNames[rand.Intn(len(firstNames))] + " " + lastNames[rand.Intn(len(lastNames))] //nolint:gosec // fixture data, not a security context
}

func fakeEmail() string {
	return strings.ToLower(firstNames[rand.Intn(len(firstNames))]) + "@example.test" //nolint:gosec // fixture data, not a security context
}
