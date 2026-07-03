package domain

// OnExhaust controls what happens once a Scenario's responses are used up.
type OnExhaust string

// The OnExhaust values controlling what happens after Scenario.Responses runs out.
const (
	OnExhaustRepeatLast  OnExhaust = "repeat_last"
	OnExhaustWrap        OnExhaust = "wrap"
	OnExhaustFallthrough OnExhaust = "fallthrough"
)

// Scenario is opt-in "lite" state: successive matching calls return
// successive responses in order.
type Scenario struct {
	Responses []RespondAction
	OnExhaust OnExhaust
}
