package usecase

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

type fakeTrafficRepo struct {
	appended  []domain.TrafficRecord
	appendErr error
}

func (f *fakeTrafficRepo) AppendTraffic(_ context.Context, t domain.TrafficRecord) error {
	if f.appendErr != nil {
		return f.appendErr
	}
	f.appended = append(f.appended, t)
	return nil
}
func (f *fakeTrafficRepo) GetTraffic(_ context.Context, _, _ string) (domain.TrafficRecord, error) {
	return domain.TrafficRecord{}, domain.ErrNotFound
}
func (f *fakeTrafficRepo) ListTraffic(_ context.Context, _ string, _ TrafficFilter) ([]domain.TrafficRecord, error) {
	return nil, nil
}
func (f *fakeTrafficRepo) PruneTraffic(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}
func (f *fakeTrafficRepo) ClearTraffic(_ context.Context, _ string) error { return nil }

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

type sequentialIDs struct{ n int }

func (s *sequentialIDs) NewID() string {
	s.n++
	return "id-" + strconv.Itoa(s.n)
}

func TestRecordTrafficEncodesAndAppends(t *testing.T) {
	repo := &fakeTrafficRepo{}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	uc := NewRecordTraffic(repo, fixedClock{now}, &sequentialIDs{})

	mockID := "mock-1"
	rec, err := uc.Execute(context.Background(), RecordTrafficInput{
		Partition: "default", Method: "GET", Host: "example.com", Path: "/foo",
		RequestHeaders: map[string][]string{"X-Req": {"1"}}, RequestBody: []byte("req-body"),
		Decision: domain.DecisionProxied, MatchedMockID: &mockID,
		ResponseHeaders: map[string][]string{"X-Resp": {"2"}}, ResponseBody: []byte("resp-body"),
		ResponseBodyTruncated: true, ResponseBodyTotalSize: 999,
		Status: 200, LatencyMS: 5,
	})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if len(repo.appended) != 1 {
		t.Fatalf("appended = %d records, want 1", len(repo.appended))
	}
	if rec.ID == "" || rec.Partition != "default" || rec.Timestamp != now {
		t.Errorf("rec = %+v, unexpected", rec)
	}

	decodedReq, err := DecodeRecordedMessage(rec.Request)
	if err != nil {
		t.Fatalf("DecodeRecordedMessage(request): %v", err)
	}
	if string(decodedReq.Body) != "req-body" || decodedReq.Headers["X-Req"][0] != "1" {
		t.Errorf("decodedReq = %+v, unexpected", decodedReq)
	}

	decodedResp, err := DecodeRecordedMessage(rec.Response)
	if err != nil {
		t.Fatalf("DecodeRecordedMessage(response): %v", err)
	}
	if string(decodedResp.Body) != "resp-body" || !decodedResp.BodyTruncated || decodedResp.BodyTotalSize != 999 {
		t.Errorf("decodedResp = %+v, unexpected", decodedResp)
	}
}

func TestRecordTrafficPropagatesRepoError(t *testing.T) {
	repo := &fakeTrafficRepo{appendErr: domain.ErrNotFound} // any error works as a stand-in
	uc := NewRecordTraffic(repo, fixedClock{time.Now()}, &sequentialIDs{})

	_, err := uc.Execute(context.Background(), RecordTrafficInput{Partition: "default"})
	if err == nil {
		t.Fatal("Execute() with a failing repo, want error")
	}
}
