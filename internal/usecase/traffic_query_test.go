package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

func TestListTrafficRejectsNegativeLimit(t *testing.T) {
	cases := map[string]int{
		"-1":   -1,
		"-100": -100,
	}
	for name, limit := range cases {
		t.Run(name, func(t *testing.T) {
			repo := &fakeTrafficRepo{}
			uc := NewListTraffic(repo)
			_, err := uc.Execute(context.Background(), "default", TrafficFilter{Limit: limit})
			if !errors.Is(err, domain.ErrInvalidTrafficFilter) {
				t.Fatalf("Execute(Limit=%d) = %v, want ErrInvalidTrafficFilter", limit, err)
			}
			if repo.listCalled {
				t.Errorf("Execute(Limit=%d) called repo.ListTraffic, want the validation to short-circuit before reaching the repo", limit)
			}
		})
	}
}

func TestListTrafficAcceptsZeroOrPositiveLimit(t *testing.T) {
	cases := map[string]int{
		"zero (unbounded default)": 0,
		"positive":                 20,
	}
	for name, limit := range cases {
		t.Run(name, func(t *testing.T) {
			repo := &fakeTrafficRepo{}
			uc := NewListTraffic(repo)
			_, err := uc.Execute(context.Background(), "default", TrafficFilter{Limit: limit})
			if err != nil {
				t.Fatalf("Execute(Limit=%d): %v", limit, err)
			}
			if !repo.listCalled {
				t.Fatalf("Execute(Limit=%d) never reached repo.ListTraffic", limit)
			}
			if repo.listFilter.Limit != limit {
				t.Errorf("repo.listFilter.Limit = %d, want %d (unchanged)", repo.listFilter.Limit, limit)
			}
		})
	}
}

func TestListTrafficFiltersByRequestBody(t *testing.T) {
	mine, err := EncodeRecordedMessage(RecordedMessage{
		Body: []byte(`{"offer":{"issuer_tax_id":"11122233344"}}`),
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	theirs, err := EncodeRecordedMessage(RecordedMessage{
		Body: []byte(`{"offer":{"issuer_tax_id":"99988877766"}}`),
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	undecodable := []byte("not a recorded message")

	repo := &fakeTrafficRepo{listResult: []domain.TrafficRecord{
		{ID: "theirs", Request: theirs},
		{ID: "mine", Request: mine},
		{ID: "broken", Request: undecodable},
	}}
	uc := NewListTraffic(repo)

	got, err := uc.Execute(context.Background(), "default", TrafficFilter{
		RequestBodyPath:   "offer.issuer_tax_id",
		RequestBodyEquals: "11122233344",
	})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if len(got) != 1 || got[0].ID != "mine" {
		t.Fatalf("got %v, want just the entry whose body matches", ids(got))
	}

	got, err = uc.Execute(context.Background(), "default", TrafficFilter{})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d records, want all 3 (including the undecodable one) when no body filter is set", len(got))
	}

	if _, err = uc.Execute(context.Background(), "default", TrafficFilter{
		RequestBodyPath: "offer.issuer_tax_id",
	}); err == nil {
		t.Fatal("Execute() accepted a body path with no value; half a filter is a caller mistake, not an empty result")
	}
}

func ids(records []domain.TrafficRecord) []string {
	out := make([]string, 0, len(records))
	for _, r := range records {
		out = append(out, r.ID)
	}
	return out
}

func TestListTrafficAppliesLimitAfterTheBodyFilter(t *testing.T) {
	// Two entries under the same path, the caller's older than somebody else's. With
	// limit=1 pushed into the store the newest would be fetched and then discarded by
	// the body filter, and the caller would be told its request never happened.
	mine, err := EncodeRecordedMessage(RecordedMessage{Body: []byte(`{"offer":{"issuer_tax_id":"11122233344"}}`)})
	if err != nil {
		t.Fatalf("EncodeRecordedMessage(): %v", err)
	}
	theirs, err := EncodeRecordedMessage(RecordedMessage{Body: []byte(`{"offer":{"issuer_tax_id":"99988877766"}}`)})
	if err != nil {
		t.Fatalf("EncodeRecordedMessage(): %v", err)
	}

	repo := &fakeTrafficRepo{listResult: []domain.TrafficRecord{
		{ID: "theirs", Request: theirs},
		{ID: "mine", Request: mine},
	}}
	uc := NewListTraffic(repo)

	got, err := uc.Execute(context.Background(), "default", TrafficFilter{
		Limit:             1,
		RequestBodyPath:   "offer.issuer_tax_id",
		RequestBodyEquals: "11122233344",
	})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if len(got) != 1 || got[0].ID != "mine" {
		t.Fatalf("got %v, want just the caller's own entry", ids(got))
	}
	if repo.listFilter.Limit != 0 {
		t.Fatalf("the store was asked for %d rows; a body filter has to scan unbounded and cap afterwards",
			repo.listFilter.Limit)
	}
}
