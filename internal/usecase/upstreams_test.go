package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

type fakeUpstreamRepo struct {
	set []domain.Upstream
}

func (f *fakeUpstreamRepo) SetUpstream(_ context.Context, u domain.Upstream) error {
	f.set = append(f.set, u)
	return nil
}
func (f *fakeUpstreamRepo) ListUpstreams(_ context.Context, _ string) ([]domain.Upstream, error) {
	return f.set, nil
}
func (f *fakeUpstreamRepo) DeleteUpstreamsByPartition(_ context.Context, _ string) error {
	return nil
}

func TestSetUpstreamAcceptsValidInput(t *testing.T) {
	repo := &fakeUpstreamRepo{}
	uc := NewSetUpstream(repo)

	u := domain.Upstream{Partition: "default", MatchHost: "api.example.com", TargetURL: "https://api.example.com"}
	if err := uc.Execute(context.Background(), u); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if len(repo.set) != 1 || repo.set[0] != u {
		t.Errorf("repo.set = %+v, want [%+v]", repo.set, u)
	}
}

func TestSetUpstreamRejectsInvalidInput(t *testing.T) {
	cases := map[string]domain.Upstream{
		"empty partition":   {MatchHost: "api.example.com", TargetURL: "https://api.example.com"},
		"empty match host":  {Partition: "default", TargetURL: "https://api.example.com"},
		"empty target url":  {Partition: "default", MatchHost: "api.example.com"},
		"malformed url":     {Partition: "default", MatchHost: "api.example.com", TargetURL: "://not-a-url"},
		"non-http scheme":   {Partition: "default", MatchHost: "api.example.com", TargetURL: "ftp://api.example.com"},
		"no host component": {Partition: "default", MatchHost: "api.example.com", TargetURL: "https:///path-only"},
	}
	for name, u := range cases {
		t.Run(name, func(t *testing.T) {
			repo := &fakeUpstreamRepo{}
			err := NewSetUpstream(repo).Execute(context.Background(), u)
			if !errors.Is(err, domain.ErrInvalidUpstream) {
				t.Fatalf("Execute(%+v) = %v, want ErrInvalidUpstream", u, err)
			}
			if len(repo.set) != 0 {
				t.Errorf("invalid upstream was still passed to the repo: %+v", repo.set)
			}
		})
	}
}

func TestListUpstreamsDelegatesToRepo(t *testing.T) {
	repo := &fakeUpstreamRepo{set: []domain.Upstream{{Partition: "default", MatchHost: "a", TargetURL: "https://a"}}}
	got, err := NewListUpstreams(repo, &fakeSeededSource{}).Execute(context.Background(), "default")
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got = %+v, want 1 upstream", got)
	}
}

func TestListUpstreamsIncludesSeededUpstreams(t *testing.T) {
	repo := &fakeUpstreamRepo{set: []domain.Upstream{{Partition: "default", MatchHost: "runtime.example", TargetURL: "https://runtime.example"}}}
	seeds := &fakeSeededSource{upstreams: []domain.Upstream{
		{Partition: "default", MatchHost: "seeded.example", TargetURL: "https://seeded.example"},
		{Partition: "other", MatchHost: "other.example", TargetURL: "https://other.example"},
	}}
	got, err := NewListUpstreams(repo, seeds).Execute(context.Background(), "default")
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got = %+v, want 2 upstreams (1 runtime + 1 seeded, other partition's excluded)", got)
	}
	var sawRuntime, sawSeeded bool
	for _, u := range got {
		sawRuntime = sawRuntime || u.MatchHost == "runtime.example"
		sawSeeded = sawSeeded || u.MatchHost == "seeded.example"
	}
	if !sawRuntime || !sawSeeded {
		t.Errorf("got = %+v, want both runtime.example and seeded.example present", got)
	}
}
