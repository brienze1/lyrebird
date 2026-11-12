package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

func cadenceEndpointInput(name string, withCadence bool) EndpointInput {
	in := delimiterInput(name)
	if withCadence {
		in.Cadence = &domain.Cadence{
			Interval: time.Millisecond,
			Frames:   [][]domain.FramePart{{{Text: strPtr("TICK")}}},
		}
	}
	return in
}

func strPtr(s string) *string { return &s }

func TestCadencePause(t *testing.T) {
	type op func(*CadencePause, context.Context, CadencePauseInput) error
	pause := func(uc *CadencePause, ctx context.Context, in CadencePauseInput) error { return uc.Pause(ctx, in) }
	resume := func(uc *CadencePause, ctx context.Context, in CadencePauseInput) error { return uc.Resume(ctx, in) }

	for name, run := range map[string]op{"pause": pause, "resume": resume} {
		t.Run(name+"/unknown endpoint", func(t *testing.T) {
			reg := &fakeRegistry{}
			eps, _ := newEndpoints(reg)
			err := run(NewCadencePause(eps, reg), context.Background(), CadencePauseInput{Partition: "default", Endpoint: "nosuch"})
			if !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
		})
		t.Run(name+"/unoccupied", func(t *testing.T) {
			reg := &fakeRegistry{cadenceErr: domain.ErrNotFound}
			eps, _ := newEndpoints(reg)
			if _, err := eps.Create(context.Background(), cadenceEndpointInput("widget", true)); err != nil {
				t.Fatal(err)
			}
			err := run(NewCadencePause(eps, reg), context.Background(), CadencePauseInput{Partition: "default", Endpoint: "widget"})
			if !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
		})
		t.Run(name+"/no cadence", func(t *testing.T) {
			reg := &fakeRegistry{}
			eps, _ := newEndpoints(reg)
			if _, err := eps.Create(context.Background(), cadenceEndpointInput("plain", false)); err != nil {
				t.Fatal(err)
			}
			err := run(NewCadencePause(eps, reg), context.Background(), CadencePauseInput{Partition: "default", Endpoint: "plain"})
			if !errors.Is(err, domain.ErrInvalidMock) {
				t.Fatalf("err = %v, want ErrInvalidMock", err)
			}
			if len(reg.cadenceCalls) != 0 {
				t.Errorf("registry called: %v", reg.cadenceCalls)
			}
		})
		t.Run(name+"/empty endpoint", func(t *testing.T) {
			reg := &fakeRegistry{}
			eps, _ := newEndpoints(reg)
			err := run(NewCadencePause(eps, reg), context.Background(), CadencePauseInput{Partition: "default"})
			if !errors.Is(err, domain.ErrInvalidMock) {
				t.Fatalf("err = %v, want ErrInvalidMock", err)
			}
		})
		t.Run(name+"/nil registry", func(t *testing.T) {
			eps, _ := newEndpoints(nil)
			err := run(NewCadencePause(eps, nil), context.Background(), CadencePauseInput{Partition: "default", Endpoint: "widget"})
			if !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
		})
		t.Run(name+"/happy path", func(t *testing.T) {
			reg := &fakeRegistry{}
			eps, _ := newEndpoints(reg)
			if _, err := eps.Create(context.Background(), cadenceEndpointInput("widget", true)); err != nil {
				t.Fatal(err)
			}
			if err := run(NewCadencePause(eps, reg), context.Background(), CadencePauseInput{Partition: "default", Endpoint: "widget"}); err != nil {
				t.Fatalf("err = %v", err)
			}
			if len(reg.cadenceCalls) != 1 || reg.cadenceCalls[0] != name+":default/widget" {
				t.Errorf("cadenceCalls = %v, want [%s:default/widget]", reg.cadenceCalls, name)
			}
		})
	}
}
