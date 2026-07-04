package usecase

import (
	"context"
	"fmt"
)

// Reset implements FR-028: remove every ephemeral mock in a partition
// (optionally clearing recorded traffic too) and restart every scenario
// mock's sequence, while preserving seeded mocks — which are never stored
// here at all (constitution Principle III), so there is nothing to
// distinguish or protect at this layer.
//
// List-then-delete is not atomic, but SQLite's single-connection
// serialization (store.Open already sets db.SetMaxOpenConns(1)) and
// Principle III's disposability discipline make that an acceptable,
// documented trade-off rather than a bug to fix here.
type Reset struct {
	mocks    MockRepo
	traffic  TrafficRepo
	scenario ScenarioStateRepo
}

// NewReset builds a Reset use case.
func NewReset(mocks MockRepo, traffic TrafficRepo, scenario ScenarioStateRepo) *Reset {
	return &Reset{mocks: mocks, traffic: traffic, scenario: scenario}
}

// ResetInput carries Reset.Execute's parameters.
type ResetInput struct {
	Partition    string
	ClearTraffic bool
}

// ResetOutput reports what Reset.Execute did.
type ResetOutput struct {
	MocksRemoved   int
	TrafficCleared bool
}

// Execute removes every ephemeral mock in in.Partition, optionally clearing
// its recorded traffic too.
func (uc *Reset) Execute(ctx context.Context, in ResetInput) (ResetOutput, error) {
	ephemeral, err := uc.mocks.ListMocks(ctx, in.Partition)
	if err != nil {
		return ResetOutput{}, fmt.Errorf("usecase: reset: list mocks: %w", err)
	}
	if err := uc.mocks.DeleteMocksByPartition(ctx, in.Partition); err != nil {
		return ResetOutput{}, fmt.Errorf("usecase: reset: delete mocks: %w", err)
	}
	if err := uc.scenario.ResetAllScenarios(ctx, in.Partition); err != nil {
		return ResetOutput{}, fmt.Errorf("usecase: reset: reset scenarios: %w", err)
	}

	out := ResetOutput{MocksRemoved: len(ephemeral)}
	if in.ClearTraffic {
		if err := uc.traffic.ClearTraffic(ctx, in.Partition); err != nil {
			return ResetOutput{}, fmt.Errorf("usecase: reset: clear traffic: %w", err)
		}
		out.TrafficCleared = true
	}
	return out, nil
}
