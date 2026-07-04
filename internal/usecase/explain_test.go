package usecase

import (
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

func TestExplainMapsDomainErrorsToKinds(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ErrorKind
	}{
		{"invalid mock", domain.ErrInvalidMock, KindValidation},
		{"invalid upstream", domain.ErrInvalidUpstream, KindValidation},
		{"seeded immutable", domain.ErrSeededMockImmutable, KindConflict},
		{"default partition protected", domain.ErrDefaultPartitionProtected, KindValidation},
		{"not found", domain.ErrNotFound, KindNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Explain(tc.err)
			if got.Kind != tc.want {
				t.Errorf("Explain(%v).Kind = %v, want %v", tc.err, got.Kind, tc.want)
			}
			if got.Message == "" {
				t.Error("Explain().Message is empty, want an explanatory message")
			}
		})
	}
}

func TestExplainDefaultsUnknownErrorsToInternal(t *testing.T) {
	got := Explain(errUnknown{})
	if got.Kind != KindInternal {
		t.Errorf("Explain(unknown).Kind = %v, want KindInternal", got.Kind)
	}
}

type errUnknown struct{}

func (errUnknown) Error() string { return "something unexpected" }
