package domain

// Script is encrypted at rest as a whole. goja execution is out of scope
// until M4 — this is only the data shape.
type Script struct {
	MatchSrc   string
	RespondSrc string
}
