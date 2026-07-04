package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

func mockID(id string) *string { return &id }

func TestMetricsAggregatesByMockPathStatus(t *testing.T) {
	repo := &fakeTrafficRepo{listResult: []domain.TrafficRecord{
		{Path: "/ping", Status: 200, LatencyMS: 10, MatchedMockID: mockID("m1"), Decision: domain.DecisionMocked},
		{Path: "/ping", Status: 200, LatencyMS: 20, MatchedMockID: mockID("m1"), Decision: domain.DecisionMocked},
		{Path: "/other", Status: 404, LatencyMS: 5, Decision: domain.DecisionNotConfigured},
	}}
	uc := NewMetrics(repo, fixedClock{time.Now()})

	out, err := uc.Execute(context.Background(), MetricsInput{Partition: "default"})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if out.Total != 3 {
		t.Errorf("Total = %d, want 3", out.Total)
	}
	if len(out.Buckets) != 2 {
		t.Fatalf("Buckets = %+v, want 2 buckets", out.Buckets)
	}
	// Sorted by Count desc: the /ping,200 bucket (count 2) comes first.
	if out.Buckets[0].Path != "/ping" || out.Buckets[0].Count != 2 || out.Buckets[0].MockID != "m1" {
		t.Errorf("Buckets[0] = %+v, unexpected", out.Buckets[0])
	}
	if out.Buckets[0].AvgLatencyMS != 15 {
		t.Errorf("Buckets[0].AvgLatencyMS = %v, want 15", out.Buckets[0].AvgLatencyMS)
	}
	if out.Buckets[1].Path != "/other" || out.Buckets[1].Count != 1 || out.Buckets[1].MockID != "" {
		t.Errorf("Buckets[1] = %+v, unexpected", out.Buckets[1])
	}
}

// TestMetricsBucketOrderIsDeterministicForTiedCountAndPath guards against
// buckets sharing Count and Path (but differing in Status/MockID) swapping
// order across calls — buckets are built by ranging over a map, whose
// iteration order Go deliberately randomizes, so the sort comparator must
// be a genuine total order, not just Count-then-Path.
func TestMetricsBucketOrderIsDeterministicForTiedCountAndPath(t *testing.T) {
	repo := &fakeTrafficRepo{listResult: []domain.TrafficRecord{
		{Path: "/shared", Status: 200, MatchedMockID: mockID("m1"), Decision: domain.DecisionMocked},
		{Path: "/shared", Status: 404, Decision: domain.DecisionNotConfigured},
	}}
	uc := NewMetrics(repo, fixedClock{time.Now()})

	for i := 0; i < 10; i++ {
		out, err := uc.Execute(context.Background(), MetricsInput{Partition: "default"})
		if err != nil {
			t.Fatalf("Execute(): %v", err)
		}
		if len(out.Buckets) != 2 {
			t.Fatalf("Buckets = %+v, want 2", out.Buckets)
		}
		if out.Buckets[0].Status != 200 || out.Buckets[1].Status != 404 {
			t.Fatalf("run %d: Buckets = %+v, want status 200 before 404 (deterministic tie-break)", i, out.Buckets)
		}
	}
}

func TestMetricsWindowSetsSinceFilter(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	repo := &fakeTrafficRepo{}
	uc := NewMetrics(repo, fixedClock{now})

	if _, err := uc.Execute(context.Background(), MetricsInput{Partition: "default", Window: time.Hour}); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if repo.listFilter.Since == nil || !repo.listFilter.Since.Equal(now.Add(-time.Hour)) {
		t.Errorf("ListTraffic filter.Since = %v, want %v", repo.listFilter.Since, now.Add(-time.Hour))
	}
}

func TestMetricsNoWindowMeansNoSinceFilter(t *testing.T) {
	repo := &fakeTrafficRepo{}
	uc := NewMetrics(repo, fixedClock{time.Now()})

	if _, err := uc.Execute(context.Background(), MetricsInput{Partition: "default"}); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if repo.listFilter.Since != nil {
		t.Errorf("ListTraffic filter.Since = %v, want nil", repo.listFilter.Since)
	}
}
