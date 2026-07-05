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
