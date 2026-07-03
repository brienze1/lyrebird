package domain

// Matcher describes a single condition; only the non-nil fields are checked,
// and all set fields on a Matcher must hold (AND) for it to be satisfied.
type Matcher struct {
	Equals   *string
	Contains *string
	Regex    *string
	Exists   *bool
}

// BodyMatcher applies a Matcher to the value found at a JSONPath in the
// request body.
type BodyMatcher struct {
	Path    string
	Matcher Matcher
}

// Match is a declarative AND of every present condition. Method/Path are
// plain strings interpreted by the matcher adapter (exact/glob/regex); the
// domain only carries the raw values.
type Match struct {
	Method  string
	Path    string
	Headers map[string]Matcher
	Query   map[string]Matcher
	Body    []BodyMatcher
}
