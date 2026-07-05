package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/brienze1/lyrebird/internal/domain"
)

// MockCRUD implements create/read/update/delete for ephemeral mocks
// (FR-007). Seeded mocks are never created/updated/deleted through it —
// UpdateMock/DeleteMock explicitly reject any id that resolves to a seeded
// mock (constitution Principle III, FR-025).
type MockCRUD struct {
	repo     MockRepo
	seeds    SeededMockSource
	match    MatchEval
	script   ScriptEval
	ids      IDGen
	clock    Clock
	scenario ScenarioStateRepo
}

// NewMockCRUD builds a MockCRUD use case.
func NewMockCRUD(repo MockRepo, seeds SeededMockSource, match MatchEval, script ScriptEval, ids IDGen, clock Clock, scenario ScenarioStateRepo) *MockCRUD {
	return &MockCRUD{repo: repo, seeds: seeds, match: match, script: script, ids: ids, clock: clock, scenario: scenario}
}

// MockInput carries the caller-settable fields of a mock — shared by Create
// and Update. Lifetime is never caller-settable: Create always produces
// LifetimeEphemeral, enforced here rather than by convention.
type MockInput struct {
	Partition  string
	Name       string
	Priority   int
	Group      string
	Match      domain.Match
	Script     *domain.Script
	Action     domain.Action
	Scenario   *domain.Scenario
	TTLSeconds *int
}

func (uc *MockCRUD) validate(in MockInput) error {
	if in.Name == "" {
		return fmt.Errorf("%w: name is required", domain.ErrInvalidMock)
	}
	if in.TTLSeconds != nil && *in.TTLSeconds <= 0 {
		return fmt.Errorf("%w: ttl_seconds must be a positive number of seconds, or omitted", domain.ErrInvalidMock)
	}
	if err := validateAction(in.Action); err != nil {
		return err
	}
	if err := uc.match.ValidateMatch(in.Match); err != nil {
		return err
	}
	if in.Script != nil {
		if err := uc.script.ValidateScript(in.Script.MatchSrc); err != nil {
			return fmt.Errorf("script.match_src: %w", err)
		}
		if err := uc.script.ValidateScript(in.Script.RespondSrc); err != nil {
			return fmt.Errorf("script.respond_src: %w", err)
		}
	}
	if in.Scenario != nil {
		if in.Action.Kind != domain.ActionRespond {
			return fmt.Errorf("%w: scenario requires action kind respond", domain.ErrInvalidMock)
		}
		if len(in.Scenario.Responses) == 0 {
			return fmt.Errorf("%w: scenario.responses must not be empty", domain.ErrInvalidMock)
		}
		switch in.Scenario.OnExhaust {
		case domain.OnExhaustRepeatLast, domain.OnExhaustWrap, domain.OnExhaustFallthrough, "":
		default:
			return fmt.Errorf("%w: unknown scenario.on_exhaust %q", domain.ErrInvalidMock, in.Scenario.OnExhaust)
		}
	}
	return nil
}

// scenarioWithDefault returns sc with OnExhaust defaulted to repeat_last
// when unset (mirrors RespondAction.Status's existing 0-defaults-to-200
// precedent). nil-safe.
func scenarioWithDefault(sc *domain.Scenario) *domain.Scenario {
	if sc == nil {
		return nil
	}
	out := *sc
	if out.OnExhaust == "" {
		out.OnExhaust = domain.OnExhaustRepeatLast
	}
	return &out
}

func validateAction(a domain.Action) error {
	switch a.Kind {
	case domain.ActionRespond:
		if a.Respond == nil {
			return fmt.Errorf("%w: action kind respond requires a respond body", domain.ErrInvalidMock)
		}
	case domain.ActionProxy:
		if a.Proxy == nil {
			return fmt.Errorf("%w: action kind proxy requires a proxy body", domain.ErrInvalidMock)
		}
	case domain.ActionFault:
		if a.Fault == nil {
			return fmt.Errorf("%w: action kind fault requires a fault body", domain.ErrInvalidMock)
		}
		switch a.Fault.Kind {
		case domain.FaultDelay, domain.FaultReset, domain.FaultTimeout, domain.FaultMalformed:
		default:
			return fmt.Errorf("%w: unknown fault.kind %q", domain.ErrInvalidMock, a.Fault.Kind)
		}
	default:
		return fmt.Errorf("%w: unknown action kind %q", domain.ErrInvalidMock, a.Kind)
	}
	return nil
}

// Create validates and persists a new ephemeral mock. Empty Match matches
// every request — a deliberate catch-all, not rejected.
func (uc *MockCRUD) Create(ctx context.Context, in MockInput) (domain.Mock, error) {
	if err := uc.validate(in); err != nil {
		return domain.Mock{}, err
	}
	m := domain.Mock{
		ID:         uc.ids.NewID(),
		Partition:  in.Partition,
		Name:       in.Name,
		Lifetime:   domain.LifetimeEphemeral,
		TTLSeconds: in.TTLSeconds,
		Priority:   in.Priority,
		Group:      in.Group,
		Match:      in.Match,
		Script:     in.Script,
		Action:     in.Action,
		Scenario:   scenarioWithDefault(in.Scenario),
		CreatedAt:  uc.clock.Now(),
	}
	if err := uc.repo.CreateMock(ctx, m); err != nil {
		return domain.Mock{}, fmt.Errorf("usecase: create mock: %w", err)
	}
	return m, nil
}

// Get returns one mock by id — ephemeral (from repo) or seeded (from
// SeededMockSource), since both are addressable via the same endpoint.
func (uc *MockCRUD) Get(ctx context.Context, partition, id string) (domain.Mock, error) {
	m, err := uc.repo.GetMock(ctx, partition, id)
	if err == nil {
		return m, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.Mock{}, fmt.Errorf("usecase: get mock: %w", err)
	}
	if seeded, ok := findSeeded(uc.seeds, partition, id); ok {
		return seeded, nil
	}
	return domain.Mock{}, domain.ErrNotFound
}

// List returns every mock in partition — ephemeral and seeded together —
// optionally filtered to one group (group == "" means no filter).
func (uc *MockCRUD) List(ctx context.Context, partition, group string) ([]domain.Mock, error) {
	ephemeral, err := uc.repo.ListMocks(ctx, partition)
	if err != nil {
		return nil, fmt.Errorf("usecase: list mocks: %w", err)
	}
	all := append(append([]domain.Mock{}, ephemeral...), uc.seeds.SeededMocks(partition)...)
	if group == "" {
		return all, nil
	}
	filtered := make([]domain.Mock, 0, len(all))
	for _, m := range all {
		if m.Group == group {
			filtered = append(filtered, m)
		}
	}
	return filtered, nil
}

// Update validates and overwrites an existing ephemeral mock's mutable
// fields, preserving its id and creation time. Returns
// domain.ErrSeededMockImmutable if id resolves to a seeded mock.
func (uc *MockCRUD) Update(ctx context.Context, partition, id string, in MockInput) (domain.Mock, error) {
	existing, err := uc.repo.GetMock(ctx, partition, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			if _, ok := findSeeded(uc.seeds, partition, id); ok {
				return domain.Mock{}, domain.ErrSeededMockImmutable
			}
			return domain.Mock{}, domain.ErrNotFound
		}
		return domain.Mock{}, fmt.Errorf("usecase: get mock for update: %w", err)
	}
	if err := uc.validate(in); err != nil {
		return domain.Mock{}, err
	}
	updated := domain.Mock{
		ID: existing.ID, Partition: existing.Partition, Lifetime: domain.LifetimeEphemeral,
		Name: in.Name, TTLSeconds: in.TTLSeconds, Priority: in.Priority, Group: in.Group,
		Match: in.Match, Script: in.Script, Action: in.Action, Scenario: scenarioWithDefault(in.Scenario),
		CreatedAt: existing.CreatedAt,
	}
	if err := uc.repo.UpdateMock(ctx, updated); err != nil {
		return domain.Mock{}, fmt.Errorf("usecase: update mock: %w", err)
	}
	// Editing a scenario's response list would otherwise leave a stale,
	// possibly out-of-range stored index behind — resetting here is
	// idempotent (a no-op if no scenario_state row exists) and cheap
	// compared to reasoning about partial-sequence carryover across edits.
	// The update itself already succeeded above; a failure here is
	// deliberately not propagated as this call's error (matching the
	// "never fail an already-completed operation for a best-effort
	// cleanup step" convention used elsewhere, e.g. proxy.Handler.recordTraffic) —
	// worst case a stale scenario index lingers, which AdvanceScenario's
	// own clamping/wrap logic already tolerates.
	_ = uc.scenario.ResetScenario(ctx, partition, id)
	return updated, nil
}

// Delete removes an ephemeral mock. Returns domain.ErrSeededMockImmutable if
// id resolves to a seeded mock, domain.ErrNotFound if id resolves to
// neither.
func (uc *MockCRUD) Delete(ctx context.Context, partition, id string) error {
	_, err := uc.repo.GetMock(ctx, partition, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			if _, ok := findSeeded(uc.seeds, partition, id); ok {
				return domain.ErrSeededMockImmutable
			}
			return domain.ErrNotFound
		}
		return fmt.Errorf("usecase: get mock for delete: %w", err)
	}
	if err := uc.repo.DeleteMock(ctx, partition, id); err != nil {
		return fmt.Errorf("usecase: delete mock: %w", err)
	}
	// Otherwise a deleted scenario mock's state row is orphaned, keyed to
	// an id nothing will ever look up again — harmless but pointless to
	// leave behind when cleaning it up here is a cheap, idempotent no-op.
	// The delete itself already succeeded above; not propagated as an
	// error for the same reason as Update's own best-effort cleanup call.
	_ = uc.scenario.ResetScenario(ctx, partition, id)
	return nil
}

func findSeeded(seeds SeededMockSource, partition, id string) (domain.Mock, bool) {
	for _, m := range seeds.SeededMocks(partition) {
		if m.ID == id {
			return m, true
		}
	}
	return domain.Mock{}, false
}
