package usecase

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Metrics aggregates recorded traffic into counts and latency percentiles
// per (mock, path, status) bucket (FR-021). Computed from TrafficRepo.ListTraffic
// on every call rather than a dedicated aggregation repo method — matching
// the "recompute per call" precedent already established by
// mock_candidates.go's loadSortedCandidates, reasonable at this project's
// expected traffic volumes.
type Metrics struct {
	repo  TrafficRepo
	clock Clock
}

// NewMetrics builds a Metrics use case.
func NewMetrics(repo TrafficRepo, clock Clock) *Metrics {
	return &Metrics{repo: repo, clock: clock}
}

// MetricsInput carries Metrics.Execute's parameters. Window == 0 means all
// retained traffic (no time filter).
type MetricsInput struct {
	Partition string
	Window    time.Duration
}

// MetricBucket reports one (mock, path, status) group's aggregate stats.
// MockID is "" for proxied/unmatched traffic (there is no matched mock).
type MetricBucket struct {
	MockID       string
	Path         string
	Status       int
	Count        int
	AvgLatencyMS float64
	P95LatencyMS float64
}

// MetricsOutput is Metrics.Execute's result.
type MetricsOutput struct {
	Window  time.Duration
	Total   int
	Buckets []MetricBucket
}

type bucketKey struct {
	mockID string
	path   string
	status int
}

// Execute aggregates recorded traffic in in.Partition (optionally windowed
// to the last in.Window) into per-(mock,path,status) counts and latency
// stats, sorted by Count descending then Path ascending.
func (uc *Metrics) Execute(ctx context.Context, in MetricsInput) (MetricsOutput, error) {
	filter := TrafficFilter{}
	if in.Window > 0 {
		since := uc.clock.Now().Add(-in.Window)
		filter.Since = &since
	}

	records, err := uc.repo.ListTraffic(ctx, in.Partition, filter)
	if err != nil {
		return MetricsOutput{}, fmt.Errorf("usecase: metrics: %w", err)
	}

	latencies := make(map[bucketKey][]int)
	for _, r := range records {
		mockID := ""
		if r.MatchedMockID != nil {
			mockID = *r.MatchedMockID
		}
		key := bucketKey{mockID: mockID, path: r.Path, status: r.Status}
		latencies[key] = append(latencies[key], r.LatencyMS)
	}

	buckets := make([]MetricBucket, 0, len(latencies))
	for key, ls := range latencies {
		sort.Ints(ls)
		buckets = append(buckets, MetricBucket{
			MockID: key.mockID, Path: key.path, Status: key.status,
			Count: len(ls), AvgLatencyMS: average(ls), P95LatencyMS: percentile(ls, 0.95),
		})
	}
	// A total order: Count desc, then Path/Status/MockID asc as tie-breakers
	// in turn. Buckets are built by ranging over a map (line above), whose
	// iteration order Go deliberately randomizes — without every field as a
	// tie-breaker, two buckets sharing Count and Path (but differing in
	// Status or MockID, e.g. a matched-mock 200 and an unmatched 404 for the
	// same path) could swap order between calls over identical data,
	// contradicting this function's own "sorted by Count desc then Path asc"
	// doc comment.
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].Count != buckets[j].Count {
			return buckets[i].Count > buckets[j].Count
		}
		if buckets[i].Path != buckets[j].Path {
			return buckets[i].Path < buckets[j].Path
		}
		if buckets[i].Status != buckets[j].Status {
			return buckets[i].Status < buckets[j].Status
		}
		return buckets[i].MockID < buckets[j].MockID
	})

	return MetricsOutput{Window: in.Window, Total: len(records), Buckets: buckets}, nil
}

func average(sorted []int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	sum := 0
	for _, v := range sorted {
		sum += v
	}
	return float64(sum) / float64(len(sorted))
}

// percentile returns the p-th percentile (0..1) of an already-sorted slice,
// using the nearest-rank method.
func percentile(sorted []int, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p*float64(len(sorted)-1) + 0.5)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return float64(sorted[idx])
}
