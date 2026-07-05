package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type stubListTraffic struct {
	list         []domain.TrafficRecord
	err          error
	gotPartition string
	gotFilter    usecase.TrafficFilter
}

func (s *stubListTraffic) Execute(_ context.Context, partition string, filter usecase.TrafficFilter) ([]domain.TrafficRecord, error) {
	s.gotPartition, s.gotFilter = partition, filter
	return s.list, s.err
}

type stubGetTraffic struct {
	record domain.TrafficRecord
	err    error
}

func (s *stubGetTraffic) Execute(_ context.Context, _, _ string) (domain.TrafficRecord, error) {
	return s.record, s.err
}

type stubClearTraffic struct {
	err          error
	gotPartition string
}

func (s *stubClearTraffic) Execute(_ context.Context, partition string) error {
	s.gotPartition = partition
	return s.err
}

type stubMetrics struct {
	out usecase.MetricsOutput
	err error
	got usecase.MetricsInput
}

func (s *stubMetrics) Execute(_ context.Context, in usecase.MetricsInput) (usecase.MetricsOutput, error) {
	s.got = in
	return s.out, s.err
}

type stubPromoteTraffic struct {
	mock domain.Mock
	err  error
	got  usecase.PromoteTrafficInput
}

func (s *stubPromoteTraffic) Execute(_ context.Context, in usecase.PromoteTrafficInput) (domain.Mock, error) {
	s.got = in
	return s.mock, s.err
}

func trafficTestDeps(listTraffic *stubListTraffic, getTraffic getTrafficPort, clearTraffic *stubClearTraffic, metrics *stubMetrics, promote *stubPromoteTraffic) Deps {
	return Deps{
		DefaultSpace: "default", ListTraffic: listTraffic, GetTraffic: getTraffic,
		ClearTraffic: clearTraffic, Metrics: metrics, PromoteTraffic: promote,
	}
}

func TestListTrafficParsesEveryFilterField(t *testing.T) {
	listTraffic := &stubListTraffic{}
	srv := New(trafficTestDeps(listTraffic, &stubGetTraffic{}, &stubClearTraffic{}, &stubMetrics{}, &stubPromoteTraffic{}))

	result := callTool(t, srv, "list_traffic", map[string]any{
		"space": "team-a", "method": "POST", "host": "api.local", "path": "/v1",
		"status": 404, "since": "2024-01-01T00:00:00Z", "until": "2024-06-01T00:00:00Z", "limit": 10,
	})
	if result.IsError {
		t.Fatalf("list_traffic returned an error: %s", errTextIfError(result))
	}
	if listTraffic.gotPartition != "team-a" {
		t.Errorf("gotPartition = %q, want team-a", listTraffic.gotPartition)
	}
	f := listTraffic.gotFilter
	if f.Method != "POST" || f.Host != "api.local" || f.PathPrefix != "/v1" || f.Limit != 10 {
		t.Fatalf("parsed filter = %+v, want method=POST host=api.local path_prefix=/v1 limit=10", f)
	}
	if f.Status == nil || *f.Status != 404 {
		t.Errorf("Status = %v, want 404", f.Status)
	}
	if f.Since == nil || !f.Since.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Since = %v, want 2024-01-01T00:00:00Z", f.Since)
	}
	if f.Until == nil || !f.Until.Equal(time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Until = %v, want 2024-06-01T00:00:00Z", f.Until)
	}
}

func TestListTrafficRejectsMalformedSinceAndUntil(t *testing.T) {
	cases := map[string]map[string]any{
		"since": {"since": "not-a-date"},
		"until": {"until": "not-a-date"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			listTraffic := &stubListTraffic{}
			srv := New(trafficTestDeps(listTraffic, &stubGetTraffic{}, &stubClearTraffic{}, &stubMetrics{}, &stubPromoteTraffic{}))
			result := callTool(t, srv, "list_traffic", args)
			msg := errText(t, result)
			if !strings.HasPrefix(msg, "validation: ") {
				t.Errorf("error = %q, want it prefixed with the validation kind tag via explainErr (not a bare parse error)", msg)
			}
		})
	}
}

func TestListTrafficMapsUseCaseErrorViaExplainWithKindTag(t *testing.T) {
	listTraffic := &stubListTraffic{err: domain.ErrNotFound}
	srv := New(trafficTestDeps(listTraffic, &stubGetTraffic{}, &stubClearTraffic{}, &stubMetrics{}, &stubPromoteTraffic{}))

	result := callTool(t, srv, "list_traffic", map[string]any{})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "not_found: ") {
		t.Errorf("error = %q, want it prefixed with the not_found kind tag", msg)
	}
}

func TestGetTrafficReturnsDecodedDetail(t *testing.T) {
	// Body now carries real, non-nil bytes: get_traffic's registered output
	// schema explicitly describes dto.RecordedMessageDTO.Body ([]byte) as the
	// base64 string encoding/json actually produces, so this exercises the
	// real schema-validation path (callTool round-trips through the SDK's
	// client/server transport) with a non-trivial body instead of dodging it.
	reqMsg, err := usecase.EncodeRecordedMessage(usecase.RecordedMessage{Headers: map[string][]string{"Content-Type": {"text/plain"}}, Body: []byte("hello world")})
	if err != nil {
		t.Fatalf("EncodeRecordedMessage(req): %v", err)
	}
	respMsg, err := usecase.EncodeRecordedMessage(usecase.RecordedMessage{Headers: map[string][]string{"Content-Type": {"text/plain"}}, Body: []byte("hello world")})
	if err != nil {
		t.Fatalf("EncodeRecordedMessage(resp): %v", err)
	}
	getTraffic := &stubGetTraffic{record: domain.TrafficRecord{
		ID: "t1", Method: "GET", Host: "example.local", Path: "/x", Status: 200,
		Request: reqMsg, Response: respMsg,
	}}
	srv := New(trafficTestDeps(&stubListTraffic{}, getTraffic, &stubClearTraffic{}, &stubMetrics{}, &stubPromoteTraffic{}))

	result := callTool(t, srv, "get_traffic", map[string]any{"id": "t1"})
	if result.IsError {
		t.Fatalf("get_traffic returned an error: %s", errTextIfError(result))
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok || out["id"] != "t1" {
		t.Errorf("structured content = %+v, want id=t1", result.StructuredContent)
	}
	req, ok := out["request"].(map[string]any)
	if !ok {
		t.Fatalf("request = %+v, want a decoded request object", out["request"])
	}
	headers, ok := req["headers"].(map[string]any)
	if !ok || headers["Content-Type"] == nil {
		t.Errorf("request headers = %+v, want Content-Type present", req["headers"])
	}
	const wantBodyBase64 = "aGVsbG8gd29ybGQ="
	if req["body"] != wantBodyBase64 {
		t.Errorf("request body = %+v, want base64 %q", req["body"], wantBodyBase64)
	}
	resp, ok := out["response"].(map[string]any)
	if !ok {
		t.Fatalf("response = %+v, want a decoded response object", out["response"])
	}
	if resp["body"] != wantBodyBase64 {
		t.Errorf("response body = %+v, want base64 %q", resp["body"], wantBodyBase64)
	}
}

// partitionedGetTraffic is a fake getTrafficPort that actually stores
// records keyed by partition, unlike stubGetTraffic's single fixed
// record/err (it discards its partition/id args entirely) — this is what
// lets TestGetTrafficRespectsExplicitNonDefaultSpace prove get_traffic
// threads its space argument through: a stub that ignores partition on
// Execute couldn't fail when the wrong partition is looked up.
type partitionedGetTraffic struct {
	byPartition         map[string]map[string]domain.TrafficRecord
	gotPartition, gotID string
}

func (s *partitionedGetTraffic) Execute(_ context.Context, partition, id string) (domain.TrafficRecord, error) {
	s.gotPartition, s.gotID = partition, id
	byID, ok := s.byPartition[partition]
	if !ok {
		return domain.TrafficRecord{}, domain.ErrNotFound
	}
	r, ok := byID[id]
	if !ok {
		return domain.TrafficRecord{}, domain.ErrNotFound
	}
	return r, nil
}

func encodedTrafficRecord(t *testing.T, id string) domain.TrafficRecord {
	t.Helper()
	reqMsg, err := usecase.EncodeRecordedMessage(usecase.RecordedMessage{Headers: map[string][]string{"Content-Type": {"text/plain"}}, Body: []byte("hello")})
	if err != nil {
		t.Fatalf("EncodeRecordedMessage(req): %v", err)
	}
	respMsg, err := usecase.EncodeRecordedMessage(usecase.RecordedMessage{Headers: map[string][]string{"Content-Type": {"text/plain"}}, Body: []byte("world")})
	if err != nil {
		t.Fatalf("EncodeRecordedMessage(resp): %v", err)
	}
	return domain.TrafficRecord{ID: id, Method: "GET", Host: "example.local", Path: "/x", Status: 200, Request: reqMsg, Response: respMsg}
}

func TestGetTrafficRespectsExplicitNonDefaultSpace(t *testing.T) {
	getTraffic := &partitionedGetTraffic{byPartition: map[string]map[string]domain.TrafficRecord{
		"team-a": {"t1": encodedTrafficRecord(t, "t1")},
	}}
	srv := New(trafficTestDeps(&stubListTraffic{}, getTraffic, &stubClearTraffic{}, &stubMetrics{}, &stubPromoteTraffic{}))

	result := callTool(t, srv, "get_traffic", map[string]any{"id": "t1", "space": "team-a"})
	if result.IsError {
		t.Fatalf("get_traffic(id=t1, space=team-a) returned an error: %s — get_traffic is not threading space through to the use case", errTextIfError(result))
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok || out["id"] != "t1" {
		t.Errorf("structured content = %+v, want id=t1", result.StructuredContent)
	}
	if getTraffic.gotPartition != "team-a" {
		t.Errorf("gotPartition = %q, want team-a (the space passed to get_traffic)", getTraffic.gotPartition)
	}
}

func TestGetTrafficDefaultsToConfiguredDefaultSpaceWhenSpaceOmitted(t *testing.T) {
	getTraffic := &partitionedGetTraffic{byPartition: map[string]map[string]domain.TrafficRecord{
		"default": {"t1": encodedTrafficRecord(t, "t1")},
	}}
	srv := New(trafficTestDeps(&stubListTraffic{}, getTraffic, &stubClearTraffic{}, &stubMetrics{}, &stubPromoteTraffic{}))

	result := callTool(t, srv, "get_traffic", map[string]any{"id": "t1"})
	if result.IsError {
		t.Fatalf("get_traffic(id=t1) returned an error: %s", errTextIfError(result))
	}
	if getTraffic.gotPartition != "default" {
		t.Errorf("gotPartition = %q, want default", getTraffic.gotPartition)
	}
}

func TestGetTrafficMapsUseCaseErrorViaExplainWithKindTag(t *testing.T) {
	getTraffic := &stubGetTraffic{err: domain.ErrNotFound}
	srv := New(trafficTestDeps(&stubListTraffic{}, getTraffic, &stubClearTraffic{}, &stubMetrics{}, &stubPromoteTraffic{}))

	result := callTool(t, srv, "get_traffic", map[string]any{"id": "missing"})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "not_found: ") {
		t.Errorf("error = %q, want it prefixed with the not_found kind tag", msg)
	}
}

func TestGetTrafficMapsUndecodableStoredRequestToInternalKindTag(t *testing.T) {
	getTraffic := &stubGetTraffic{record: domain.TrafficRecord{ID: "t1", Request: []byte("not json")}}
	srv := New(trafficTestDeps(&stubListTraffic{}, getTraffic, &stubClearTraffic{}, &stubMetrics{}, &stubPromoteTraffic{}))

	result := callTool(t, srv, "get_traffic", map[string]any{"id": "t1"})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "internal: ") {
		t.Errorf("error = %q, want it prefixed with the internal kind tag", msg)
	}
}

func TestInspectRequestsDefaultsLimitTo20(t *testing.T) {
	listTraffic := &stubListTraffic{}
	srv := New(trafficTestDeps(listTraffic, &stubGetTraffic{}, &stubClearTraffic{}, &stubMetrics{}, &stubPromoteTraffic{}))

	result := callTool(t, srv, "inspect_requests", map[string]any{"space": "team-a"})
	if result.IsError {
		t.Fatalf("inspect_requests returned an error: %s", errTextIfError(result))
	}
	if listTraffic.gotFilter.Limit != 20 {
		t.Errorf("Limit = %d, want the default of 20", listTraffic.gotFilter.Limit)
	}
	if listTraffic.gotPartition != "team-a" {
		t.Errorf("gotPartition = %q, want team-a", listTraffic.gotPartition)
	}
}

// noopTrafficRepo satisfies usecase.TrafficRepo with no-op behavior on
// every method other than ListTraffic — enough to wire a *real*
// usecase.NewListTraffic use case (rather than stubListTraffic's canned
// Execute) into deps.ListTraffic, so
// TestListTrafficRejectsNegativeLimitEndToEnd proves the negative-limit
// rejection added to ListTraffic.Execute is actually reachable through the
// list_traffic tool, not just exercised at the usecase layer in isolation.
type noopTrafficRepo struct{}

func (noopTrafficRepo) AppendTraffic(context.Context, domain.TrafficRecord) error { return nil }
func (noopTrafficRepo) GetTraffic(context.Context, string, string) (domain.TrafficRecord, error) {
	return domain.TrafficRecord{}, domain.ErrNotFound
}
func (noopTrafficRepo) ListTraffic(context.Context, string, usecase.TrafficFilter) ([]domain.TrafficRecord, error) {
	return nil, nil
}
func (noopTrafficRepo) PruneTraffic(context.Context, time.Time) (int, error) { return 0, nil }
func (noopTrafficRepo) ClearTraffic(context.Context, string) error           { return nil }

func TestListTrafficRejectsNegativeLimitEndToEnd(t *testing.T) {
	srv := New(Deps{
		DefaultSpace:   "default",
		ListTraffic:    usecase.NewListTraffic(noopTrafficRepo{}),
		GetTraffic:     &stubGetTraffic{},
		ClearTraffic:   &stubClearTraffic{},
		Metrics:        &stubMetrics{},
		PromoteTraffic: &stubPromoteTraffic{},
	})

	result := callTool(t, srv, "list_traffic", map[string]any{"limit": -1})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "validation: ") {
		t.Errorf("error = %q, want it prefixed with the validation kind tag", msg)
	}
	if !strings.Contains(msg, "invalid traffic filter") {
		t.Errorf("error = %q, want it to mention %q", msg, "invalid traffic filter")
	}
}

func TestInspectRequestsHonorsExplicitLimit(t *testing.T) {
	listTraffic := &stubListTraffic{}
	srv := New(trafficTestDeps(listTraffic, &stubGetTraffic{}, &stubClearTraffic{}, &stubMetrics{}, &stubPromoteTraffic{}))

	result := callTool(t, srv, "inspect_requests", map[string]any{"limit": 5})
	if result.IsError {
		t.Fatalf("inspect_requests returned an error: %s", errTextIfError(result))
	}
	if listTraffic.gotFilter.Limit != 5 {
		t.Errorf("Limit = %d, want 5", listTraffic.gotFilter.Limit)
	}
}

func TestMetricsParsesWindowDuration(t *testing.T) {
	metrics := &stubMetrics{out: usecase.MetricsOutput{Total: 2, Buckets: []usecase.MetricBucket{
		{MockID: "m1", Path: "/x", Status: 200, Count: 2, AvgLatencyMS: 1.5, P95LatencyMS: 2.5},
	}}}
	srv := New(trafficTestDeps(&stubListTraffic{}, &stubGetTraffic{}, &stubClearTraffic{}, metrics, &stubPromoteTraffic{}))

	result := callTool(t, srv, "metrics", map[string]any{"space": "team-a", "window": "1h"})
	if result.IsError {
		t.Fatalf("metrics returned an error: %s", errTextIfError(result))
	}
	if metrics.got.Partition != "team-a" || metrics.got.Window != time.Hour {
		t.Errorf("got %+v, want partition=team-a window=1h", metrics.got)
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok || out["total"] != 2.0 || out["window"] != "1h" {
		t.Errorf("structured content = %+v, want total=2 window=1h", result.StructuredContent)
	}
}

func TestMetricsRejectsMalformedWindow(t *testing.T) {
	srv := New(trafficTestDeps(&stubListTraffic{}, &stubGetTraffic{}, &stubClearTraffic{}, &stubMetrics{}, &stubPromoteTraffic{}))

	result := callTool(t, srv, "metrics", map[string]any{"window": "not-a-duration"})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "validation: ") {
		t.Errorf("error = %q, want it prefixed with the validation kind tag via explainErr (not a bare parse error)", msg)
	}
}

func TestMetricsMapsUseCaseErrorViaExplainWithKindTag(t *testing.T) {
	metrics := &stubMetrics{err: domain.ErrNotFound}
	srv := New(trafficTestDeps(&stubListTraffic{}, &stubGetTraffic{}, &stubClearTraffic{}, metrics, &stubPromoteTraffic{}))

	result := callTool(t, srv, "metrics", map[string]any{})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "not_found: ") {
		t.Errorf("error = %q, want it prefixed with the not_found kind tag", msg)
	}
}

func TestClearTrafficClearsAndReturnsClearedTrue(t *testing.T) {
	clearTraffic := &stubClearTraffic{}
	srv := New(trafficTestDeps(&stubListTraffic{}, &stubGetTraffic{}, clearTraffic, &stubMetrics{}, &stubPromoteTraffic{}))

	result := callTool(t, srv, "clear_traffic", map[string]any{"space": "team-a"})
	if result.IsError {
		t.Fatalf("clear_traffic returned an error: %s", errTextIfError(result))
	}
	if clearTraffic.gotPartition != "team-a" {
		t.Errorf("gotPartition = %q, want team-a", clearTraffic.gotPartition)
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok || out["cleared"] != true {
		t.Errorf("structured content = %+v, want cleared=true", result.StructuredContent)
	}
}

func TestClearTrafficMapsUseCaseErrorViaExplainWithKindTag(t *testing.T) {
	clearTraffic := &stubClearTraffic{err: domain.ErrNotFound}
	srv := New(trafficTestDeps(&stubListTraffic{}, &stubGetTraffic{}, clearTraffic, &stubMetrics{}, &stubPromoteTraffic{}))

	result := callTool(t, srv, "clear_traffic", map[string]any{})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "not_found: ") {
		t.Errorf("error = %q, want it prefixed with the not_found kind tag", msg)
	}
}

func TestPromoteTrafficPersistsAndReturnsTheCreatedMock(t *testing.T) {
	promote := &stubPromoteTraffic{mock: domain.Mock{ID: "m1", Name: "promoted-t1"}}
	srv := New(trafficTestDeps(&stubListTraffic{}, &stubGetTraffic{}, &stubClearTraffic{}, &stubMetrics{}, promote))

	ttl := 60
	result := callTool(t, srv, "promote_traffic", map[string]any{
		"space": "team-a", "traffic_id": "t1", "name": "custom-name", "ttl_seconds": ttl,
	})
	if result.IsError {
		t.Fatalf("promote_traffic returned an error: %s", errTextIfError(result))
	}
	if promote.got.Partition != "team-a" || promote.got.TrafficID != "t1" || promote.got.Name != "custom-name" {
		t.Errorf("use case received %+v, want the decoded promote input", promote.got)
	}
	if promote.got.TTLSeconds == nil || *promote.got.TTLSeconds != ttl {
		t.Errorf("TTLSeconds = %v, want %d", promote.got.TTLSeconds, ttl)
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok || out["id"] != "m1" {
		t.Errorf("structured content = %+v, want id=m1", result.StructuredContent)
	}
}

func TestPromoteTrafficMapsUseCaseErrorViaExplainWithKindTag(t *testing.T) {
	promote := &stubPromoteTraffic{err: domain.ErrNotFound}
	srv := New(trafficTestDeps(&stubListTraffic{}, &stubGetTraffic{}, &stubClearTraffic{}, &stubMetrics{}, promote))

	result := callTool(t, srv, "promote_traffic", map[string]any{"traffic_id": "missing"})
	msg := errText(t, result)
	if !strings.HasPrefix(msg, "not_found: ") {
		t.Errorf("error = %q, want it prefixed with the not_found kind tag", msg)
	}
}
