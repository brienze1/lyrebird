package mcp

import (
	"context"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type fakeMockCRUD struct{}

func (fakeMockCRUD) Create(context.Context, usecase.MockInput) (domain.Mock, error) {
	return domain.Mock{}, nil
}
func (fakeMockCRUD) Get(context.Context, string, string) (domain.Mock, error) {
	return domain.Mock{}, nil
}
func (fakeMockCRUD) List(context.Context, string, string) ([]domain.Mock, error) { return nil, nil }
func (fakeMockCRUD) Update(context.Context, string, string, usecase.MockInput) (domain.Mock, error) {
	return domain.Mock{}, nil
}
func (fakeMockCRUD) Delete(context.Context, string, string) error { return nil }

type fakeReset struct{}

func (fakeReset) Execute(context.Context, usecase.ResetInput) (usecase.ResetOutput, error) {
	return usecase.ResetOutput{}, nil
}

type fakeMatchTest struct{}

func (fakeMatchTest) Execute(context.Context, string, usecase.MatchInput) (usecase.MatchTestOutput, error) {
	return usecase.MatchTestOutput{}, nil
}

type fakeSetUpstream struct{}

func (fakeSetUpstream) Execute(context.Context, domain.Upstream) error { return nil }

type fakeListUpstreams struct{}

func (fakeListUpstreams) Execute(context.Context, string) ([]domain.Upstream, error) { return nil, nil }

type fakeListTraffic struct{}

func (fakeListTraffic) Execute(context.Context, string, usecase.TrafficFilter) ([]domain.TrafficRecord, error) {
	return nil, nil
}

type fakeGetTraffic struct{}

func (fakeGetTraffic) Execute(context.Context, string, string) (domain.TrafficRecord, error) {
	return domain.TrafficRecord{}, nil
}

type fakeClearTraffic struct{}

func (fakeClearTraffic) Execute(context.Context, string) error { return nil }

type fakeMetrics struct{}

func (fakeMetrics) Execute(context.Context, usecase.MetricsInput) (usecase.MetricsOutput, error) {
	return usecase.MetricsOutput{}, nil
}

type fakePromoteTraffic struct{}

func (fakePromoteTraffic) Execute(context.Context, usecase.PromoteTrafficInput) (domain.Mock, error) {
	return domain.Mock{}, nil
}

func fakeDeps() Deps {
	return Deps{
		DefaultSpace:   "default",
		MockCRUD:       fakeMockCRUD{},
		Reset:          fakeReset{},
		MatchTest:      fakeMatchTest{},
		SetUpstream:    fakeSetUpstream{},
		ListUpstreams:  fakeListUpstreams{},
		ListTraffic:    fakeListTraffic{},
		GetTraffic:     fakeGetTraffic{},
		ClearTraffic:   fakeClearTraffic{},
		Metrics:        fakeMetrics{},
		PromoteTraffic: fakePromoteTraffic{},
	}
}

// TestNewRegistersEveryToolWithoutPanicking guards against mcp.AddTool's
// registration-time panic on a bad jsonschema tag or unsupported In/Out
// field type — a mistake here must fail `go test`, not surface only when a
// live server boots.
func TestNewRegistersEveryToolWithoutPanicking(t *testing.T) {
	srv := New(fakeDeps())
	if srv == nil {
		t.Fatal("New() returned nil")
	}
}
