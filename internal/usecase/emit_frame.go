package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/brienze1/lyrebird/internal/domain"
)

// EmitFrame pushes bytes into a live byte-stream connection with no inbound
// frame having provoked them (003's FR-012).
//
// This is the injection half of the plane's originate capability; the other
// half is an endpoint's declared cadence, which runs without any control-plane
// call at all. Both reach the wire through the same single writer, so an
// injection can never be spliced into another frame and the order things were
// emitted in is the order they arrive in (FR-033).
type EmitFrame struct {
	endpoints *Endpoints
	registry  ConnectionRegistry
}

// NewEmitFrame builds an EmitFrame use case. registry is nil whenever
// LYREBIRD_STREAM_PORT is unset, in which case every call reports plainly
// that the plane is not enabled rather than panicking or silently succeeding.
func NewEmitFrame(endpoints *Endpoints, registry ConnectionRegistry) *EmitFrame {
	return &EmitFrame{endpoints: endpoints, registry: registry}
}

// EmitFrameInput carries EmitFrame.Execute's parameters. FrameSpec is the
// same declarative frame grammar a mock's respond body uses, so there is one
// grammar to learn wherever a frame is declared.
type EmitFrameInput struct {
	Partition string
	Endpoint  string
	FrameSpec []byte
}

// Execute resolves the endpoint, then queues the frame on whatever stand-in
// currently holds it.
//
// A missing stand-in is an error, never a silent success (FR-015): a test
// injecting into a connection that has already closed must be told so, since
// the alternative is a scenario that fails much later for a reason nobody can
// trace back to this call.
func (uc *EmitFrame) Execute(ctx context.Context, in EmitFrameInput) error {
	if uc.registry == nil {
		return fmt.Errorf(
			"%w: the byte-stream data plane is not enabled — set LYREBIRD_STREAM_PORT to turn it on",
			domain.ErrNotFound)
	}
	if in.Endpoint == "" {
		return fmt.Errorf("%w: endpoint is required", domain.ErrInvalidMock)
	}

	// Resolved first so "you named an endpoint that does not exist" and
	// "nothing is connected to it" stay separate answers — they call for
	// completely different fixes.
	if _, err := uc.endpoints.Get(ctx, in.Partition, in.Endpoint); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: no endpoint %q is declared in space %q",
				domain.ErrNotFound, in.Endpoint, in.Partition)
		}
		return fmt.Errorf("usecase: emit frame: %w", err)
	}

	if err := uc.registry.Emit(ctx, in.Partition, in.Endpoint, in.FrameSpec); err != nil {
		return fmt.Errorf("usecase: emit frame: %w", err)
	}
	return nil
}
