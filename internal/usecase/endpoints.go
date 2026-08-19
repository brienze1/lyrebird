package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/brienze1/lyrebird/internal/domain"
)

// Endpoints implements declare/list/delete for byte-stream endpoints (003's
// FR-002, FR-005, FR-022). It is the byte-stream peer of MockCRUD, and it
// keeps the same discipline: seeded endpoints are protected config that never
// reaches the disposable store, session-created ones are ephemeral and cleared
// by a reset.
//
// It deliberately owns no transport state. Whether a stand-in is currently
// connected is runtime state belonging to the ConnectionRegistry, consulted
// read-only when listing so an operator can see occupancy without this use
// case knowing what a socket is.
type Endpoints struct {
	repo     EndpointRepo
	seeds    SeededEndpointSource
	registry ConnectionRegistry
	clock    Clock
}

// NewEndpoints builds an Endpoints use case. registry may be nil — it is
// whenever LYREBIRD_STREAM_PORT is unset — in which case every endpoint
// simply reports as unoccupied, because nothing can be connected to a plane
// that is not listening (FR-026).
func NewEndpoints(repo EndpointRepo, seeds SeededEndpointSource, registry ConnectionRegistry, clock Clock) *Endpoints {
	return &Endpoints{repo: repo, seeds: seeds, registry: registry, clock: clock}
}

// EndpointInput carries the caller-settable fields of an endpoint. Lifetime
// is never caller-settable: Create always produces LifetimeEphemeral, the
// same rule MockInput follows.
type EndpointInput struct {
	Partition  string
	Name       string
	Framing    domain.Framing
	Projection *domain.Projection
	Cadence    *domain.Cadence
}

// EndpointView is one endpoint plus the runtime fact an operator actually
// wants when listing: whether a stand-in currently holds it.
type EndpointView struct {
	Endpoint domain.Endpoint
	Occupied bool
}

// Create validates and persists a new ephemeral endpoint. A name already
// taken in this space — by an ephemeral OR a seeded endpoint — is a
// duplicate, since a handshake resolves one name to exactly one endpoint and
// two candidates would make which one a stand-in got depend on lookup order.
func (uc *Endpoints) Create(ctx context.Context, in EndpointInput) (domain.Endpoint, error) {
	if err := validateEndpoint(in); err != nil {
		return domain.Endpoint{}, err
	}
	if _, err := uc.Get(ctx, in.Partition, in.Name); err == nil {
		return domain.Endpoint{}, fmt.Errorf("%w: endpoint %q already exists in space %q",
			domain.ErrDuplicateID, in.Name, in.Partition)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Endpoint{}, err
	}

	e := domain.Endpoint{
		Name:       in.Name,
		Partition:  in.Partition,
		Framing:    framingWithDefaults(in.Framing),
		Projection: in.Projection,
		Cadence:    cadenceWithDefaults(in.Cadence),
		Lifetime:   domain.LifetimeEphemeral,
		CreatedAt:  uc.clock.Now(),
	}
	if err := uc.repo.CreateEndpoint(ctx, e); err != nil {
		return domain.Endpoint{}, fmt.Errorf("usecase: create endpoint: %w", err)
	}
	return e, nil
}

// Get resolves one endpoint by name — ephemeral first, then seeded, matching
// how MockCRUD.Get resolves a mock id across both sources.
func (uc *Endpoints) Get(ctx context.Context, partition, name string) (domain.Endpoint, error) {
	e, err := uc.repo.GetEndpoint(ctx, partition, name)
	if err == nil {
		return e, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.Endpoint{}, fmt.Errorf("usecase: get endpoint: %w", err)
	}
	for _, s := range uc.seeds.SeededEndpoints(partition) {
		if s.Name == name {
			return s, nil
		}
	}
	return domain.Endpoint{}, domain.ErrNotFound
}

// List returns every endpoint in the space — ephemeral and seeded together —
// each with its current occupancy.
func (uc *Endpoints) List(ctx context.Context, partition string) ([]EndpointView, error) {
	ephemeral, err := uc.repo.ListEndpoints(ctx, partition)
	if err != nil {
		return nil, fmt.Errorf("usecase: list endpoints: %w", err)
	}
	all := append(append([]domain.Endpoint{}, ephemeral...), uc.seeds.SeededEndpoints(partition)...)

	out := make([]EndpointView, 0, len(all))
	for _, e := range all {
		out = append(out, EndpointView{Endpoint: e, Occupied: uc.occupied(partition, e.Name)})
	}
	return out, nil
}

// Delete removes an ephemeral endpoint. A seeded one is protected config, so
// it is rejected explicitly rather than silently ignored — the same contract
// MockCRUD.Delete has for a seeded mock.
func (uc *Endpoints) Delete(ctx context.Context, partition, name string) error {
	if err := uc.repo.DeleteEndpoint(ctx, partition, name); err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("usecase: delete endpoint: %w", err)
		}
		for _, s := range uc.seeds.SeededEndpoints(partition) {
			if s.Name == name {
				return domain.ErrSeededMockImmutable
			}
		}
		return domain.ErrNotFound
	}
	// A stand-in holding an endpoint that no longer exists would keep being
	// served by rules for a boundary that has been removed, so its
	// connection goes with it.
	if uc.registry != nil {
		uc.registry.CloseSpace(partition)
	}
	return nil
}

func (uc *Endpoints) occupied(partition, name string) bool {
	return uc.registry != nil && uc.registry.Occupied(partition, name)
}

// validateEndpoint enforces data-model.md §10. Every rule here exists because
// its violation would otherwise surface as silence on a wire rather than as
// an error an author can act on.
func validateEndpoint(in EndpointInput) error {
	if in.Name == "" {
		return fmt.Errorf("%w: endpoint name is required", domain.ErrInvalidMock)
	}
	// The name is the last path segment of DELETE /__lyrebird/stream/
	// endpoints/{name}, and Go's ServeMux wildcard matches exactly one
	// segment — the same constraint seeds.Load already enforces on mock
	// names, for the same reason.
	if strings.Contains(in.Name, "/") {
		return fmt.Errorf("%w: endpoint name %q must not contain \"/\"", domain.ErrInvalidMock, in.Name)
	}
	if strings.ContainsAny(in.Name, " \t\r\n") {
		return fmt.Errorf("%w: endpoint name %q must not contain whitespace — a handshake line is space-separated",
			domain.ErrInvalidMock, in.Name)
	}
	if err := validateFraming(in.Framing); err != nil {
		return err
	}
	if err := validateProjection(in.Projection); err != nil {
		return err
	}
	return validateCadence(in.Cadence)
}

func validateFraming(f domain.Framing) error {
	switch f.Kind {
	case domain.FramingDelimiter, "":
		if len(f.Delimiter) == 0 {
			return fmt.Errorf("%w: framing.delimiter must not be empty", domain.ErrInvalidMock)
		}
	case domain.FramingLength:
		if f.Length <= 0 {
			return fmt.Errorf("%w: framing.length must be positive, got %d", domain.ErrInvalidMock, f.Length)
		}
	case domain.FramingPrefix:
		if f.PrefixWidth < 1 || f.PrefixWidth > 8 {
			return fmt.Errorf("%w: framing.prefix.width must be 1..8 bytes, got %d",
				domain.ErrInvalidMock, f.PrefixWidth)
		}
		switch f.PrefixEndian {
		case domain.EndianBig, domain.EndianLittle, "":
		default:
			return fmt.Errorf("%w: framing.prefix.endian %q is not big or little",
				domain.ErrInvalidMock, f.PrefixEndian)
		}
	default:
		return fmt.Errorf("%w: unknown framing kind %q (want delimiter, length or prefix)",
			domain.ErrInvalidMock, f.Kind)
	}
	return nil
}

func validateCadence(c *domain.Cadence) error {
	if c == nil {
		return nil
	}
	if c.Interval <= 0 {
		return fmt.Errorf("%w: cadence.interval must be positive", domain.ErrInvalidMock)
	}
	// An empty frame list is an error rather than a silent no-op: a cadence
	// that emits nothing is always a mistake, and one that quietly does
	// nothing is a mistake nobody notices until a scenario times out.
	if len(c.Frames) == 0 {
		return fmt.Errorf("%w: cadence.frames must not be empty", domain.ErrInvalidMock)
	}
	switch c.OnExhaust {
	case domain.OnExhaustionRepeatLast, domain.OnExhaustionLoop, domain.OnExhaustionStop, "":
		return nil
	default:
		return fmt.Errorf("%w: unknown cadence.on_exhaustion %q (want repeat_last, loop or stop)",
			domain.ErrInvalidMock, c.OnExhaust)
	}
}

// framingWithDefaults fills in the implicit variant, so a declaration that
// only sets a delimiter means what it obviously means.
func framingWithDefaults(f domain.Framing) domain.Framing {
	if f.Kind == "" {
		f.Kind = domain.FramingDelimiter
	}
	if f.Kind == domain.FramingPrefix && f.PrefixEndian == "" {
		f.PrefixEndian = domain.EndianBig
	}
	return f
}

// cadenceWithDefaults defaults OnExhaust to repeat_last — what a stationary
// source does (FR-011) — mirroring scenarioWithDefault's precedent. nil-safe.
func cadenceWithDefaults(c *domain.Cadence) *domain.Cadence {
	if c == nil {
		return nil
	}
	out := *c
	if out.OnExhaust == "" {
		out.OnExhaust = domain.OnExhaustionRepeatLast
	}
	return &out
}
