package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

func TestEmitFrameQueuesOnTheLiveConnection(t *testing.T) {
	registry := &fakeRegistry{}
	endpoints, _ := newEndpoints(registry)
	if _, err := endpoints.Create(context.Background(), delimiterInput("widget")); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	uc := NewEmitFrame(endpoints, registry)

	err := uc.Execute(context.Background(), EmitFrameInput{
		Partition: "default", Endpoint: "widget", FrameSpec: []byte(`[{"text":"PUSHED"}]`),
	})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if len(registry.emitted) != 1 || registry.emitted[0] != `default/widget:[{"text":"PUSHED"}]` {
		t.Errorf("emitted = %v, want the frame queued on default/widget", registry.emitted)
	}
}

// FR-015: injecting into a connection that no longer exists must be reported
// plainly. Silently succeeding turns a dead stand-in into a scenario that
// fails much later for a reason nobody can trace back to this call.
func TestEmitFrameReportsAMissingStandIn(t *testing.T) {
	registry := &fakeRegistry{emitErr: domain.ErrNotFound}
	endpoints, _ := newEndpoints(registry)
	if _, err := endpoints.Create(context.Background(), delimiterInput("widget")); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	uc := NewEmitFrame(endpoints, registry)

	err := uc.Execute(context.Background(), EmitFrameInput{Partition: "default", Endpoint: "widget"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Execute() with nothing connected = %v, want ErrNotFound", err)
	}
}

// "You named an endpoint that does not exist" and "nothing is connected to
// it" call for completely different fixes, so they stay separate answers.
func TestEmitFrameDistinguishesAnUndeclaredEndpoint(t *testing.T) {
	registry := &fakeRegistry{}
	endpoints, _ := newEndpoints(registry)
	uc := NewEmitFrame(endpoints, registry)

	err := uc.Execute(context.Background(), EmitFrameInput{Partition: "default", Endpoint: "nosuch"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Execute() for an undeclared endpoint = %v, want ErrNotFound", err)
	}
	if len(registry.emitted) != 0 {
		t.Errorf("emitted = %v, want nothing queued for an endpoint that does not exist", registry.emitted)
	}
}

// With LYREBIRD_STREAM_PORT unset the registry is nil. The tool must say so
// rather than panicking or pretending to have emitted something.
func TestEmitFrameWithThePlaneDisabled(t *testing.T) {
	endpoints, _ := newEndpoints(nil)
	uc := NewEmitFrame(endpoints, nil)

	err := uc.Execute(context.Background(), EmitFrameInput{Partition: "default", Endpoint: "widget"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Execute() with the plane disabled = %v, want ErrNotFound", err)
	}
	if got := err.Error(); !strings.Contains(got, "LYREBIRD_STREAM_PORT") {
		t.Errorf("error = %q, want it to name the variable that turns the plane on", got)
	}
}

func TestEmitFrameRequiresAnEndpoint(t *testing.T) {
	endpoints, _ := newEndpoints(&fakeRegistry{})
	uc := NewEmitFrame(endpoints, &fakeRegistry{})

	if err := uc.Execute(context.Background(), EmitFrameInput{Partition: "default"}); err == nil {
		t.Error("Execute() with no endpoint succeeded, want it rejected")
	}
}
