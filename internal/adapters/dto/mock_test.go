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
