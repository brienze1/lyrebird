package streamplane

import (
	"context"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

func cadenceOverrideMock(frames [][]domain.FramePart) *domain.Mock {
	return &domain.Mock{
		ID: "m1", Name: "m1",
		Action: domain.Action{Kind: domain.ActionCadence, Cadence: &domain.CadenceAction{Frames: frames}},
	}
}

// TestCadenceOverride_EffectiveResolution drives resolveEffectiveCadence
// (pure — no sockets, no goroutines) through WI-02's Test Plan item 1: no
// override resolves to the declared cadence; an active override resolves to
// its own content; and switching sources (declared -> override, override ->
// declared again on delete) changes identity, which is what tells
// runCadence's loop to restart the frame index.
func TestCadenceOverride_EffectiveResolution(t *testing.T) {
	declared := &domain.Cadence{
		Interval: 2 * time.Millisecond, OnExhaust: domain.OnExhaustionRepeatLast,
		Frames: [][]domain.FramePart{{{Text: ptr("SEED")}}},
	}
	override := cadenceOverrideMock([][]domain.FramePart{{{Text: ptr("OVERRIDE")}}})

	t.Run("no override resolves to declared", func(t *testing.T) {
		eff := resolveEffectiveCadence(declared, nil)
		if eff.cadence != declared {
			t.Errorf("cadence = %+v, want the declared cadence pointer", eff.cadence)
		}
		if eff.identity != declaredCadenceIdentity {
			t.Errorf("identity = %q, want %q", eff.identity, declaredCadenceIdentity)
		}
	})

	t.Run("an active override resolves to its own content", func(t *testing.T) {
		eff := resolveEffectiveCadence(declared, override)
		if len(eff.cadence.Frames) != 1 || *eff.cadence.Frames[0][0].Text != "OVERRIDE" {
			t.Errorf("cadence.Frames = %+v, want the override's OVERRIDE frame", eff.cadence.Frames)
		}
		if eff.identity != "mock:m1" {
			t.Errorf("identity = %q, want %q", eff.identity, "mock:m1")
		}
	})

	t.Run("switching sources changes identity", func(t *testing.T) {
		declaredEff := resolveEffectiveCadence(declared, nil)
		overrideEff := resolveEffectiveCadence(declared, override)
		revertedEff := resolveEffectiveCadence(declared, nil) // e.g. after the override mock was deleted

		if declaredEff.identity == overrideEff.identity {
			t.Fatal("declared and override resolved to the same identity, want distinct so a switch is detectable")
		}
		if revertedEff.identity != declaredEff.identity {
			t.Errorf("reverted identity = %q, want it to match the original declared identity %q",
				revertedEff.identity, declaredEff.identity)
		}
	})

	t.Run("a mock with no cadence action is treated as no override (defensive)", func(t *testing.T) {
		bare := &domain.Mock{ID: "bare", Action: domain.Action{Kind: domain.ActionCadence, Cadence: nil}}
		eff := resolveEffectiveCadence(declared, bare)
		if eff.identity != declaredCadenceIdentity {
			t.Errorf("identity = %q, want %q (fall back to declared)", eff.identity, declaredCadenceIdentity)
		}
	})
}

// TestCadenceOverride_InheritsDeclaredPacing proves WI-02 AC-3's
// content-only default at the merge boundary: an override that omits
// interval/on_exhaustion inherits the endpoint's declared values, and one
// that sets them overrides pacing too.
func TestCadenceOverride_InheritsDeclaredPacing(t *testing.T) {
	declared := &domain.Cadence{
		Interval: 2 * time.Millisecond, OnExhaust: domain.OnExhaustionRepeatLast,
		Frames: [][]domain.FramePart{{{Text: ptr("SEED")}}},
	}

	t.Run("omitted interval and on_exhaustion inherit the declared cadence", func(t *testing.T) {
		override := cadenceOverrideMock([][]domain.FramePart{{{Text: ptr("OVERRIDE")}}})
		eff := resolveEffectiveCadence(declared, override)
		if eff.cadence.Interval != declared.Interval {
			t.Errorf("Interval = %v, want inherited %v", eff.cadence.Interval, declared.Interval)
		}
		if eff.cadence.OnExhaust != declared.OnExhaust {
			t.Errorf("OnExhaust = %q, want inherited %q", eff.cadence.OnExhaust, declared.OnExhaust)
		}
	})

	t.Run("an explicit interval and on_exhaustion override pacing", func(t *testing.T) {
		override := cadenceOverrideMock([][]domain.FramePart{{{Text: ptr("OVERRIDE")}}})
		explicit := 500 * time.Millisecond
		override.Action.Cadence.Interval = &explicit
		override.Action.Cadence.OnExhaust = domain.OnExhaustionLoop

		eff := resolveEffectiveCadence(declared, override)
		if eff.cadence.Interval != explicit {
			t.Errorf("Interval = %v, want the override's own %v", eff.cadence.Interval, explicit)
		}
		if eff.cadence.OnExhaust != domain.OnExhaustionLoop {
			t.Errorf("OnExhaust = %q, want %q", eff.cadence.OnExhaust, domain.OnExhaustionLoop)
		}
	})
}

// fakeCadenceResolver is a minimal cadenceResolver stand-in for exercising
// conn.resolveCadence without a real usecase.CadenceOverride or a store.
type fakeCadenceResolver struct {
	mock *domain.Mock
	err  error
}

func (f *fakeCadenceResolver) Resolve(_ context.Context, _, _ string) (domain.Mock, bool, error) {
	if f.err != nil {
		return domain.Mock{}, false, f.err
	}
	if f.mock == nil {
		return domain.Mock{}, false, nil
	}
	return *f.mock, true, nil
}

// TestConnResolveCadence_NoResolverWiredBehavesAsToday proves a nil resolver
// (every existing cadence test in this package, and any byte-stream plane
// built without cadence-override support wired) is unaffected by this
// feature: it always falls back to the declared cadence.
func TestConnResolveCadence_NoResolverWiredBehavesAsToday(t *testing.T) {
	c, _ := newTestConn(t)
	declared := &domain.Cadence{Interval: time.Second, Frames: [][]domain.FramePart{{{Text: ptr("SEED")}}}}

	eff := c.resolveCadence(context.Background(), declared)
	if eff.identity != declaredCadenceIdentity {
		t.Errorf("identity = %q, want %q (no resolver wired)", eff.identity, declaredCadenceIdentity)
	}
}

// TestConnResolveCadence_ActiveOverride proves conn.resolveCadence merges
// the resolver's winning mock through resolveEffectiveCadence.
func TestConnResolveCadence_ActiveOverride(t *testing.T) {
	c, _ := newTestConn(t)
	c.handler.cadence = &fakeCadenceResolver{mock: cadenceOverrideMock([][]domain.FramePart{{{Text: ptr("OVERRIDE")}}})}
	declared := &domain.Cadence{Interval: time.Second, Frames: [][]domain.FramePart{{{Text: ptr("SEED")}}}}

	eff := c.resolveCadence(context.Background(), declared)
	if eff.identity != "mock:m1" {
		t.Errorf("identity = %q, want %q", eff.identity, "mock:m1")
	}
}
