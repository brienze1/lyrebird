package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// fakeEndpointRepo is an in-memory EndpointRepo, mirroring fakeMockRepo.
type fakeEndpointRepo struct {
	byKey map[string]domain.Endpoint
}

func newFakeEndpointRepo() *fakeEndpointRepo {
	return &fakeEndpointRepo{byKey: map[string]domain.Endpoint{}}
}

func (f *fakeEndpointRepo) key(partition, name string) string { return partition + "\x00" + name }

func (f *fakeEndpointRepo) CreateEndpoint(_ context.Context, e domain.Endpoint) error {
	f.byKey[f.key(e.Partition, e.Name)] = e
	return nil
}

func (f *fakeEndpointRepo) GetEndpoint(_ context.Context, partition, name string) (domain.Endpoint, error) {
	e, ok := f.byKey[f.key(partition, name)]
	if !ok {
		return domain.Endpoint{}, domain.ErrNotFound
	}
	return e, nil
}

func (f *fakeEndpointRepo) ListEndpoints(_ context.Context, partition string) ([]domain.Endpoint, error) {
	var out []domain.Endpoint
	for _, e := range f.byKey {
		if e.Partition == partition {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeEndpointRepo) DeleteEndpoint(_ context.Context, partition, name string) error {
	k := f.key(partition, name)
	if _, ok := f.byKey[k]; !ok {
		return domain.ErrNotFound
	}
	delete(f.byKey, k)
	return nil
}

func (f *fakeEndpointRepo) DeleteEndpointsByPartition(_ context.Context, partition string) error {
	for k, e := range f.byKey {
		if e.Partition == partition {
			delete(f.byKey, k)
		}
	}
	return nil
}

// fakeSeededEndpoints is a SeededEndpointSource holding protected config.
type fakeSeededEndpoints struct{ endpoints []domain.Endpoint }

func (f *fakeSeededEndpoints) SeededEndpoints(partition string) []domain.Endpoint {
	var out []domain.Endpoint
	for _, e := range f.endpoints {
		if e.Partition == partition {
			out = append(out, e)
		}
	}
	return out
}

// fakeRegistry records what a use case asked of the live connection registry.
type fakeRegistry struct {
	closedSpaces []string
	occupied     map[string]bool
	emitted      []string
	emitErr      error
}

func (f *fakeRegistry) Emit(_ context.Context, partition, endpoint string, spec []byte) error {
	if f.emitErr != nil {
		return f.emitErr
	}
	f.emitted = append(f.emitted, partition+"/"+endpoint+":"+string(spec))
	return nil
}

func (f *fakeRegistry) CloseSpace(partition string) {
	f.closedSpaces = append(f.closedSpaces, partition)
}

func (f *fakeRegistry) Occupied(partition, endpoint string) bool {
	return f.occupied[partition+"/"+endpoint]
}

func newEndpoints(registry ConnectionRegistry) (*Endpoints, *fakeSeededEndpoints) {
	repo := newFakeEndpointRepo()
	seeds := &fakeSeededEndpoints{}
	return NewEndpoints(repo, seeds, registry, fixedClock{t: time.Unix(1, 0)}), seeds
}

func delimiterInput(name string) EndpointInput {
	return EndpointInput{
		Partition: "default",
		Name:      name,
		Framing:   domain.Framing{Kind: domain.FramingDelimiter, Delimiter: []byte("\r\n")},
	}
}

func TestEndpointsCreateDefaultsTheFramingKindAndLifetime(t *testing.T) {
	uc, _ := newEndpoints(nil)

	in := delimiterInput("widget")
	in.Framing.Kind = "" // an author who wrote only `{delimiter: "\r\n"}`

	e, err := uc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if e.Framing.Kind != domain.FramingDelimiter {
		t.Errorf("Framing.Kind = %q, want it defaulted to delimiter", e.Framing.Kind)
	}
	if e.Lifetime != domain.LifetimeEphemeral {
		t.Errorf("Lifetime = %q, want ephemeral — Create never produces seeded config", e.Lifetime)
	}
}

func TestEndpointsCreateDefaultsCadenceExhaustionToRepeatLast(t *testing.T) {
	uc, _ := newEndpoints(nil)

	in := delimiterInput("ticker")
	in.Cadence = &domain.Cadence{Interval: time.Second, Frames: [][]domain.FramePart{{{Text: textPart("TICK")}}}}

	e, err := uc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if e.Cadence.OnExhaust != domain.OnExhaustionRepeatLast {
		t.Errorf("OnExhaust = %q, want repeat_last — what a stationary source does", e.Cadence.OnExhaust)
	}
}

// A handshake resolves one name to exactly one endpoint, so a duplicate is
// refused whether the incumbent is ephemeral or seeded.
func TestEndpointsCreateRejectsDuplicates(t *testing.T) {
	uc, seeds := newEndpoints(nil)

	if _, err := uc.Create(context.Background(), delimiterInput("widget")); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if _, err := uc.Create(context.Background(), delimiterInput("widget")); !errors.Is(err, domain.ErrDuplicateID) {
		t.Errorf("Create() of a duplicate = %v, want ErrDuplicateID", err)
	}

	seeds.endpoints = append(seeds.endpoints, domain.Endpoint{
		Name: "seeded", Partition: "default", Lifetime: domain.LifetimeSeeded,
	})
	if _, err := uc.Create(context.Background(), delimiterInput("seeded")); !errors.Is(err, domain.ErrDuplicateID) {
		t.Errorf("Create() shadowing a seeded endpoint = %v, want ErrDuplicateID", err)
	}
}

func TestEndpointsCreateRejectsMalformedDeclarations(t *testing.T) {
	tests := []struct {
		name string
		in   EndpointInput
	}{
		{"no name", EndpointInput{Partition: "default", Framing: delimiterInput("x").Framing}},
		{"name with a slash", delimiterInputNamed("a/b")},
		{"name with whitespace", delimiterInputNamed("a b")},
		{"empty delimiter", EndpointInput{Partition: "default", Name: "x",
			Framing: domain.Framing{Kind: domain.FramingDelimiter}}},
		{"non-positive length", EndpointInput{Partition: "default", Name: "x",
			Framing: domain.Framing{Kind: domain.FramingLength, Length: 0}}},
		{"prefix width out of range", EndpointInput{Partition: "default", Name: "x",
			Framing: domain.Framing{Kind: domain.FramingPrefix, PrefixWidth: 99}}},
		{"unknown framing kind", EndpointInput{Partition: "default", Name: "x",
			Framing: domain.Framing{Kind: "smoke-signals"}}},
		{"cadence with no frames", cadenceInput(time.Second, nil)},
		{"cadence with no interval", cadenceInput(0, [][]domain.FramePart{{{Text: textPart("x")}}})},
		{"unknown on_exhaustion", unknownExhaustionInput()},
		{"projection field with no name", projectionInput(domain.ProjectionField{Offset: 0, Length: 1})},
		{"projection field with a zero length", projectionInput(domain.ProjectionField{Name: "a", Length: 0})},
		{"projection field with an unknown as", projectionInput(domain.ProjectionField{Name: "a", Length: 1, As: "morse"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc, _ := newEndpoints(nil)
			if _, err := uc.Create(context.Background(), tt.in); err == nil {
				t.Error("Create() succeeded, want it rejected at declaration time")
			}
		})
	}
}

func TestEndpointsListReportsOccupancyAndBothSources(t *testing.T) {
	registry := &fakeRegistry{occupied: map[string]bool{"default/widget": true}}
	uc, seeds := newEndpoints(registry)

	if _, err := uc.Create(context.Background(), delimiterInput("widget")); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	seeds.endpoints = append(seeds.endpoints, domain.Endpoint{
		Name: "seeded", Partition: "default", Lifetime: domain.LifetimeSeeded,
	})

	views, err := uc.List(context.Background(), "default")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("List() returned %d endpoints, want the ephemeral and the seeded one", len(views))
	}
	for _, v := range views {
		switch v.Endpoint.Name {
		case "widget":
			if !v.Occupied {
				t.Error("widget reported unoccupied, want occupied")
			}
		case "seeded":
			if v.Occupied {
				t.Error("seeded reported occupied, want unoccupied")
			}
		}
	}
}

// With the plane disabled the registry is nil, and every endpoint must simply
// report unoccupied rather than panicking — nothing can be connected to a
// plane that is not listening (FR-026).
func TestEndpointsListIsNilRegistrySafe(t *testing.T) {
	uc, _ := newEndpoints(nil)
	if _, err := uc.Create(context.Background(), delimiterInput("widget")); err != nil {
		t.Fatalf("Create(): %v", err)
	}

	views, err := uc.List(context.Background(), "default")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(views) != 1 || views[0].Occupied {
		t.Errorf("List() = %+v, want one unoccupied endpoint", views)
	}
}

func TestEndpointsDeleteProtectsSeededAndReportsMissing(t *testing.T) {
	registry := &fakeRegistry{}
	uc, seeds := newEndpoints(registry)
	seeds.endpoints = append(seeds.endpoints, domain.Endpoint{
		Name: "seeded", Partition: "default", Lifetime: domain.LifetimeSeeded,
	})

	if err := uc.Delete(context.Background(), "default", "seeded"); !errors.Is(err, domain.ErrSeededMockImmutable) {
		t.Errorf("Delete() of a seeded endpoint = %v, want ErrSeededMockImmutable", err)
	}
	if err := uc.Delete(context.Background(), "default", "nosuch"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Delete() of an unknown endpoint = %v, want ErrNotFound", err)
	}

	if _, err := uc.Create(context.Background(), delimiterInput("widget")); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if err := uc.Delete(context.Background(), "default", "widget"); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	// A stand-in holding an endpoint that no longer exists would keep being
	// served by rules for a boundary that has been removed.
	if len(registry.closedSpaces) != 1 || registry.closedSpaces[0] != "default" {
		t.Errorf("closedSpaces = %v, want the space closed once", registry.closedSpaces)
	}
}

// textPart builds a one-part frame spec for a cadence fixture.
func textPart(s string) *string { return &s }

func delimiterInputNamed(name string) EndpointInput {
	in := delimiterInput("placeholder")
	in.Name = name
	return in
}

func cadenceInput(interval time.Duration, frames [][]domain.FramePart) EndpointInput {
	in := delimiterInput("ticker")
	in.Cadence = &domain.Cadence{Interval: interval, Frames: frames}
	return in
}

func unknownExhaustionInput() EndpointInput {
	in := cadenceInput(time.Second, [][]domain.FramePart{{{Text: textPart("x")}}})
	in.Cadence.OnExhaust = "explode"
	return in
}

func projectionInput(f domain.ProjectionField) EndpointInput {
	in := delimiterInput("widget")
	in.Projection = &domain.Projection{At: []domain.ProjectionField{f}}
	return in
}
