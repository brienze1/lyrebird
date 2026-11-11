package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/brienze1/lyrebird/internal/domain"
)

// CadencePause stops and restarts an endpoint's cadence on its live
// connection (CB5-84 WI-04). Injection is unaffected: only the endpoint's own
// unprompted ticks are held back.
type CadencePause struct {
	endpoints *Endpoints
	registry  ConnectionRegistry
}

// NewCadencePause builds a CadencePause use case. registry is nil whenever
// LYREBIRD_STREAM_PORT is unset, in which case every call reports plainly that
// the plane is not enabled.
func NewCadencePause(endpoints *Endpoints, registry ConnectionRegistry) *CadencePause {
	return &CadencePause{endpoints: endpoints, registry: registry}
}

// CadencePauseInput carries Pause's and Resume's parameters.
type CadencePauseInput struct {
	Partition string
	Endpoint  string
}

// Pause stops the endpoint's cadence from queueing further frames and returns
// once every frame already queued is on the wire.
func (uc *CadencePause) Pause(ctx context.Context, in CadencePauseInput) error {
	if err := uc.validate(ctx, in); err != nil {
		return err
	}
	if err := uc.registry.PauseCadence(ctx, in.Partition, in.Endpoint); err != nil {
		return fmt.Errorf("usecase: pause cadence: %w", err)
	}
	return nil
}

// Resume lets a paused cadence emit again from its next iteration.
func (uc *CadencePause) Resume(ctx context.Context, in CadencePauseInput) error {
	if err := uc.validate(ctx, in); err != nil {
		return err
	}
	if err := uc.registry.ResumeCadence(ctx, in.Partition, in.Endpoint); err != nil {
		return fmt.Errorf("usecase: resume cadence: %w", err)
	}
	return nil
}

// validate keeps "no such endpoint", "endpoint without a cadence" and "nothing
// connected" as three separate answers, since each calls for a different fix.
func (uc *CadencePause) validate(ctx context.Context, in CadencePauseInput) error {
	if uc.registry == nil {
		return fmt.Errorf(
			"%w: the byte-stream data plane is not enabled — set LYREBIRD_STREAM_PORT to turn it on",
			domain.ErrNotFound)
	}
	if in.Endpoint == "" {
		return fmt.Errorf("%w: endpoint is required", domain.ErrInvalidMock)
	}
	e, err := uc.endpoints.Get(ctx, in.Partition, in.Endpoint)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: no endpoint %q is declared in space %q",
				domain.ErrNotFound, in.Endpoint, in.Partition)
		}
		return fmt.Errorf("usecase: cadence pause: %w", err)
	}
	if e.Cadence == nil {
		return fmt.Errorf("%w: endpoint %q declares no cadence to pause or resume",
			domain.ErrInvalidMock, in.Endpoint)
	}
	return nil
}
