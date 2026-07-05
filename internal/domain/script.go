package domain

// Script is encrypted at rest as a whole. MatchSrc is evaluated by the goja
// scripting engine at match-time, as an additional AND-composed gate applied
// after a mock's declarative Match already passes (usecase.MatchRequest.Execute).
// RespondSrc is evaluated at respond-time to build the response body
// (usecase.BuildRespondOutputWithScript) — it takes over Body only; Status,
// Headers, and LatencyMS still come from the mock's Action.
type Script struct {
	MatchSrc   string
	RespondSrc string
}
