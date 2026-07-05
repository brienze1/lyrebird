package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

func TestCreatePartitionThenGetReturnsIt(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	created := time.Unix(1_700_000_000, 0)
	p := domain.Partition{ID: "agent-a", CreatedAt: created, Description: "agent a's sandbox"}
	if err := st.CreatePartition(ctx, p); err != nil {
		t.Fatalf("CreatePartition(): %v", err)
	}

	got, err := st.GetPartition(ctx, "agent-a")
	if err != nil {
		t.Fatalf("GetPartition(): %v", err)
	}
	if got.ID != p.ID || got.Description != p.Description || !got.CreatedAt.Equal(p.CreatedAt) {
		t.Fatalf("GetPartition() = %+v, want %+v", got, p)
	}
}

func TestGetPartitionReturnsNotFoundForUnknownID(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.GetPartition(context.Background(), "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetPartition() = %v, want ErrNotFound", err)
	}
}

func TestCreatePartitionIsIdempotentAndDoesNotResetCreatedAt(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	first := time.Unix(1_700_000_000, 0)
	if err := st.CreatePartition(ctx, domain.Partition{ID: "agent-a", CreatedAt: first, Description: "v1"}); err != nil {
		t.Fatalf("CreatePartition() first: %v", err)
	}

	second := time.Unix(1_800_000_000, 0)
	if err := st.CreatePartition(ctx, domain.Partition{ID: "agent-a", CreatedAt: second, Description: "v2"}); err != nil {
		t.Fatalf("CreatePartition() second: %v", err)
	}

	got, err := st.GetPartition(ctx, "agent-a")
	if err != nil {
		t.Fatalf("GetPartition(): %v", err)
	}
	if got.Description != "v2" {
		t.Fatalf("GetPartition().Description = %q, want the refreshed value %q", got.Description, "v2")
	}
	if !got.CreatedAt.Equal(first) {
		t.Fatalf("GetPartition().CreatedAt = %v, want the original creation time %v (unchanged by re-create)", got.CreatedAt, first)
	}
}

func TestListPartitionsOrderedByID(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for _, id := range []string{"zebra", "alpha", "middle"} {
		if err := st.CreatePartition(ctx, domain.Partition{ID: id, CreatedAt: time.Now()}); err != nil {
			t.Fatalf("CreatePartition(%q): %v", id, err)
		}
	}

	got, err := st.ListPartitions(ctx)
	if err != nil {
		t.Fatalf("ListPartitions(): %v", err)
	}
	want := []string{"alpha", "middle", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("ListPartitions() = %+v, want %d entries", got, len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("ListPartitions()[%d].ID = %q, want %q", i, got[i].ID, id)
		}
	}
}

func TestDeletePartitionCascadesMocksTrafficAndUpstreams(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.CreatePartition(ctx, domain.Partition{ID: "agent-a", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("CreatePartition(): %v", err)
	}
	if err := st.CreateMock(ctx, domain.Mock{
		ID: "m1", Partition: "agent-a", Name: "m1", CreatedAt: time.Now(),
		Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200}},
	}); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}
	if err := st.SetUpstream(ctx, domain.Upstream{Partition: "agent-a", MatchHost: "api.example.com", TargetURL: "https://api.example.com"}); err != nil {
		t.Fatalf("SetUpstream(): %v", err)
	}
	if err := st.AppendTraffic(ctx, domain.TrafficRecord{
		ID: "t1", Partition: "agent-a", Timestamp: time.Now(), Method: "GET", Host: "api.example.com", Path: "/x", Status: 200,
	}); err != nil {
		t.Fatalf("AppendTraffic(): %v", err)
	}

	if err := st.DeletePartition(ctx, "agent-a"); err != nil {
		t.Fatalf("DeletePartition(): %v", err)
	}

	if mocks, err := st.ListMocks(ctx, "agent-a"); err != nil || len(mocks) != 0 {
		t.Errorf("ListMocks(agent-a) after delete = %+v, %v, want empty", mocks, err)
	}
	if traffic, err := st.ListTraffic(ctx, "agent-a", usecase.TrafficFilter{}); err != nil || len(traffic) != 0 {
		t.Errorf("ListTraffic(agent-a) after delete = %+v, %v, want empty", traffic, err)
	}
	if upstreams, err := st.ListUpstreams(ctx, "agent-a"); err != nil || len(upstreams) != 0 {
		t.Errorf("ListUpstreams(agent-a) after delete = %+v, %v, want empty", upstreams, err)
	}
	if _, err := st.GetPartition(ctx, "agent-a"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetPartition(agent-a) after delete = %v, want ErrNotFound", err)
	}
}

// TestDeletePartitionCascadesScenarioState is a regression test: deleting a
// space used to leave that partition's scenario_state rows orphaned
// forever, since DeletePartition's cascade never called ResetAllScenarios
// (unlike the Reset use case, which does call it — see
// internal/usecase/reset.go). It confirms ScenarioIndex is nonzero *before*
// deleting the partition, so the post-delete zero can't be confused with
// "there was never a row".
func TestDeletePartitionCascadesScenarioState(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.CreatePartition(ctx, domain.Partition{ID: "agent-a", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("CreatePartition(): %v", err)
	}
	if err := st.CreateMock(ctx, domain.Mock{
		ID: "m1", Partition: "agent-a", Name: "m1", CreatedAt: time.Now(),
		Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200}},
	}); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}
	if _, err := st.AdvanceScenario(ctx, "agent-a", "m1"); err != nil {
		t.Fatalf("AdvanceScenario(): %v", err)
	}

	// Confirm there really is something to clean up before deleting.
	if idx, err := st.ScenarioIndex(ctx, "agent-a", "m1"); err != nil || idx == 0 {
		t.Fatalf("ScenarioIndex() before delete = (%d, %v), want a nonzero index (a scenario_state row must exist)", idx, err)
	}

	if err := st.DeletePartition(ctx, "agent-a"); err != nil {
		t.Fatalf("DeletePartition(): %v", err)
	}

	if idx, err := st.ScenarioIndex(ctx, "agent-a", "m1"); err != nil || idx != 0 {
		t.Errorf("ScenarioIndex() after DeletePartition = (%d, %v), want (0, nil) — orphaned scenario_state row must be cleaned up", idx, err)
	}
}

// TestDeletePartitionScenarioStateCascadeOnlyAffectsThatPartition proves the
// ResetAllScenarios call added to DeletePartition's cascade is scoped
// exactly to the deleted partition: another partition's scenario_state row
// must survive untouched.
func TestDeletePartitionScenarioStateCascadeOnlyAffectsThatPartition(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if _, err := st.AdvanceScenario(ctx, "agent-a", "m1"); err != nil {
		t.Fatalf("AdvanceScenario(agent-a/m1): %v", err)
	}
	if _, err := st.AdvanceScenario(ctx, "agent-b", "m1"); err != nil {
		t.Fatalf("AdvanceScenario(agent-b/m1): %v", err)
	}

	if err := st.DeletePartition(ctx, "agent-a"); err != nil {
		t.Fatalf("DeletePartition(): %v", err)
	}

	if idx, err := st.ScenarioIndex(ctx, "agent-a", "m1"); err != nil || idx != 0 {
		t.Errorf("ScenarioIndex(agent-a/m1) after DeletePartition(agent-a) = (%d, %v), want (0, nil)", idx, err)
	}
	if idx, err := st.ScenarioIndex(ctx, "agent-b", "m1"); err != nil || idx != 1 {
		t.Errorf("ScenarioIndex(agent-b/m1) after DeletePartition(agent-a) = (%d, %v), want untouched 1", idx, err)
	}
}

func TestDeletePartitionOnlyAffectsThatPartition(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.SetUpstream(ctx, domain.Upstream{Partition: "agent-a", MatchHost: "api.example.com", TargetURL: "https://a.example.com"}); err != nil {
		t.Fatalf("SetUpstream(agent-a): %v", err)
	}
	if err := st.SetUpstream(ctx, domain.Upstream{Partition: "agent-b", MatchHost: "api.example.com", TargetURL: "https://b.example.com"}); err != nil {
		t.Fatalf("SetUpstream(agent-b): %v", err)
	}

	if err := st.DeletePartition(ctx, "agent-a"); err != nil {
		t.Fatalf("DeletePartition(): %v", err)
	}

	if upstreams, err := st.ListUpstreams(ctx, "agent-b"); err != nil || len(upstreams) != 1 {
		t.Errorf("ListUpstreams(agent-b) after deleting agent-a = %+v, %v, want untouched", upstreams, err)
	}
}
