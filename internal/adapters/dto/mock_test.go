package dto

import (
	"errors"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

func validMockDTO() MockDTO {
	return MockDTO{
		Name:   "ping",
		Match:  MatchDTO{Method: "GET", Path: "/ping"},
		Action: ActionDTO{Respond: &RespondDTO{Status: 200, Body: "pong"}},
	}
}

func TestMockInputFromDTORejectsNonEphemeralLifetime(t *testing.T) {
	d := validMockDTO()
	d.Lifetime = "seeded"

	_, err := MockInputFromDTO("default", d)
	if !errors.Is(err, domain.ErrInvalidMock) {
		t.Fatalf("MockInputFromDTO(lifetime=seeded) = %v, want ErrInvalidMock", err)
	}
}

func TestMockInputFromDTOAcceptsEphemeralOrEmptyLifetime(t *testing.T) {
	for _, lifetime := range []string{"", "ephemeral"} {
		d := validMockDTO()
		d.Lifetime = lifetime

		in, err := MockInputFromDTO("default", d)
		if err != nil {
			t.Fatalf("MockInputFromDTO(lifetime=%q) = %v, want nil", lifetime, err)
		}
		if in.Name != "ping" {
			t.Errorf("MockInputFromDTO(lifetime=%q).Name = %q, want %q", lifetime, in.Name, "ping")
		}
	}
}

func TestActionFromDTORejectsNoneSet(t *testing.T) {
	_, err := ActionFromDTO(ActionDTO{})
	if !errors.Is(err, domain.ErrInvalidMock) {
		t.Fatalf("ActionFromDTO(none set) = %v, want ErrInvalidMock", err)
	}
}

func TestActionFromDTORejectsMultipleActionsSet(t *testing.T) {
	d := ActionDTO{
		Respond: &RespondDTO{Status: 200, Body: "pong"},
		Proxy:   &ProxyDTO{},
	}

	_, err := ActionFromDTO(d)
	if !errors.Is(err, domain.ErrInvalidMock) {
		t.Fatalf("ActionFromDTO(respond+proxy set) = %v, want ErrInvalidMock", err)
	}
}

// TestActionFromDTORoundTripsCadence proves the wire<->domain conversion for
// action.cadence: interval parsed as a Go duration when present, left nil
// when omitted (content-only default, WI-02 AC-3), frames carried as
// frame-spec STRINGS (mirroring mcp.CadenceIn.Frames — CadenceActionDTO's
// own doc comment explains why this differs from CadenceDTO.Frames'
// structured shape) rather than structured parts.
func TestActionFromDTORoundTripsCadence(t *testing.T) {
	interval := "500ms"
	d := ActionDTO{Cadence: &CadenceActionDTO{
		Interval:     &interval,
		OnExhaustion: "loop",
		Frames:       []string{`[{"text":"GPRMC"}]`},
	}}

	action, err := ActionFromDTO(d)
	if err != nil {
		t.Fatalf("ActionFromDTO(cadence): %v", err)
	}
	if action.Kind != domain.ActionCadence {
		t.Fatalf("Kind = %q, want %q", action.Kind, domain.ActionCadence)
	}
	if action.Cadence == nil {
		t.Fatal("Cadence = nil, want the converted action")
	}
	if action.Cadence.Interval == nil || *action.Cadence.Interval != 500_000_000 {
		t.Errorf("Interval = %v, want 500ms", action.Cadence.Interval)
	}
	if action.Cadence.OnExhaust != domain.OnExhaustionLoop {
		t.Errorf("OnExhaust = %q, want %q", action.Cadence.OnExhaust, domain.OnExhaustionLoop)
	}
	if len(action.Cadence.Frames) != 1 || *action.Cadence.Frames[0][0].Text != "GPRMC" {
		t.Errorf("Frames = %+v, want one frame containing %q", action.Cadence.Frames, "GPRMC")
	}

	back := ActionToDTO(action)
	if back.Cadence == nil || back.Cadence.Interval == nil || *back.Cadence.Interval != "500ms" {
		t.Errorf("ActionToDTO round trip Interval = %+v, want \"500ms\"", back.Cadence)
	}
	if len(back.Cadence.Frames) != 1 || back.Cadence.Frames[0] != `[{"text":"GPRMC"}]` {
		t.Errorf("ActionToDTO round trip Frames = %+v, want the original spec string back", back.Cadence.Frames)
	}
}

// TestActionFromDTOCadenceOmittedIntervalStaysNil proves the content-only
// default at the DTO boundary: an override that never sets "interval"
// produces a nil domain Interval (inherit), not a zero duration.
func TestActionFromDTOCadenceOmittedIntervalStaysNil(t *testing.T) {
	d := ActionDTO{Cadence: &CadenceActionDTO{Frames: []string{`[{"text":"GPRMC"}]`}}}

	action, err := ActionFromDTO(d)
	if err != nil {
		t.Fatalf("ActionFromDTO(cadence, no interval): %v", err)
	}
	if action.Cadence.Interval != nil {
		t.Errorf("Interval = %v, want nil (inherit the endpoint's declared interval)", action.Cadence.Interval)
	}
}

// TestActionFromDTORejectsRespondAndCadenceSet extends the existing
// multiple-actions-set rejection to the new cadence variant.
func TestActionFromDTORejectsRespondAndCadenceSet(t *testing.T) {
	d := ActionDTO{
		Respond: &RespondDTO{Status: 200, Body: "pong"},
		Cadence: &CadenceActionDTO{Frames: []string{`[{"text":"x"}]`}},
	}

	_, err := ActionFromDTO(d)
	if !errors.Is(err, domain.ErrInvalidMock) {
		t.Fatalf("ActionFromDTO(respond+cadence set) = %v, want ErrInvalidMock", err)
	}
}
