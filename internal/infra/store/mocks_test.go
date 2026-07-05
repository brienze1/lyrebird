package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

func sampleMock(id, partition string) domain.Mock {
	return domain.Mock{
		ID: id, Partition: partition, Name: "mock " + id,
		Lifetime: domain.LifetimeEphemeral, Priority: 1, Group: "g1",
		Match:     domain.Match{Method: "GET", Path: "/foo"},
		Action:    domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200, Body: []byte(`{"ok":true}`)}},
		CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
}

func TestCreateMockThenGetReturnsIt(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	m := sampleMock("m1", "default")
	if err := st.CreateMock(ctx, m); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}

	got, err := st.GetMock(ctx, "default", "m1")
	if err != nil {
		t.Fatalf("GetMock(): %v", err)
	}
	if got.ID != m.ID || got.Partition != m.Partition || got.Name != m.Name ||
		got.Priority != m.Priority || got.Group != m.Group {
		t.Errorf("GetMock() = %+v, want fields matching %+v", got, m)
	}
	if got.Lifetime != domain.LifetimeEphemeral {
		t.Errorf("GetMock().Lifetime = %q, want %q", got.Lifetime, domain.LifetimeEphemeral)
	}
	if !got.CreatedAt.Equal(m.CreatedAt) {
		t.Errorf("GetMock().CreatedAt = %v, want %v", got.CreatedAt, m.CreatedAt)
	}
	if !reflect.DeepEqual(got.Match, m.Match) {
		t.Errorf("GetMock().Match = %+v, want %+v", got.Match, m.Match)
	}
	if got.Action.Kind != domain.ActionRespond || got.Action.Respond == nil ||
		got.Action.Respond.Status != 200 || string(got.Action.Respond.Body) != `{"ok":true}` {
		t.Errorf("GetMock().Action = %+v, want a respond action matching the original", got.Action)
	}
	if got.Script != nil {
		t.Errorf("GetMock().Script = %+v, want nil (none was set)", got.Script)
	}
	if got.Scenario != nil {
		t.Errorf("GetMock().Scenario = %+v, want nil (none was set)", got.Scenario)
	}
}

// TestCreateMockThenGetRoundTripsEncryptedScriptAndScenario is the core gap
// this test file exists to close: script_blob and scenario_blob are sealed
// at rest (mocks.go: encodeMock/decodeMockScript/decodeMockScenario), so a
// mock carrying both must decrypt back to the exact original values through
// a real seal/open round trip, not just survive a plaintext-only path.
func TestCreateMockThenGetRoundTripsEncryptedScriptAndScenario(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	m := sampleMock("m1", "default")
	m.Script = &domain.Script{
		MatchSrc:   "match.method === 'GET'",
		RespondSrc: "respond.body = {greeting: 'hi'}",
	}
	m.Scenario = &domain.Scenario{
		Responses: []domain.RespondAction{
			{Status: 200, Body: []byte(`{"step":1}`)},
			{Status: 201, Body: []byte(`{"step":2}`)},
		},
		OnExhaust: domain.OnExhaustRepeatLast,
	}

	if err := st.CreateMock(ctx, m); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}

	got, err := st.GetMock(ctx, "default", "m1")
	if err != nil {
		t.Fatalf("GetMock(): %v", err)
	}

	if got.Script == nil {
		t.Fatalf("GetMock().Script = nil, want the sealed script to decrypt back")
	}
	if *got.Script != *m.Script {
		t.Errorf("GetMock().Script = %+v, want %+v", got.Script, m.Script)
	}

	if got.Scenario == nil {
		t.Fatalf("GetMock().Scenario = nil, want the sealed scenario to decrypt back")
	}
	if got.Scenario.OnExhaust != m.Scenario.OnExhaust {
		t.Errorf("GetMock().Scenario.OnExhaust = %q, want %q", got.Scenario.OnExhaust, m.Scenario.OnExhaust)
	}
	if len(got.Scenario.Responses) != len(m.Scenario.Responses) {
		t.Fatalf("GetMock().Scenario.Responses = %+v, want %d entries", got.Scenario.Responses, len(m.Scenario.Responses))
	}
	for i, want := range m.Scenario.Responses {
		gotResp := got.Scenario.Responses[i]
		if gotResp.Status != want.Status || string(gotResp.Body) != string(want.Body) {
			t.Errorf("GetMock().Scenario.Responses[%d] = %+v, want %+v", i, gotResp, want)
		}
	}
}

func TestGetMockReturnsErrNotFoundForMissingID(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.GetMock(context.Background(), "default", "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetMock() = %v, want domain.ErrNotFound", err)
	}
}

func TestUpdateMockChangesFieldsAndAddsScriptAndScenario(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	m := sampleMock("m1", "default")
	if err := st.CreateMock(ctx, m); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}

	updated := m
	updated.Name = "renamed"
	updated.Priority = 5
	updated.Group = "g2"
	updated.Script = &domain.Script{MatchSrc: "true", RespondSrc: "respond.body = {}"}
	updated.Scenario = &domain.Scenario{
		Responses: []domain.RespondAction{{Status: 204}},
		OnExhaust: domain.OnExhaustWrap,
	}

	if err := st.UpdateMock(ctx, updated); err != nil {
		t.Fatalf("UpdateMock() adding script/scenario: %v", err)
	}

	got, err := st.GetMock(ctx, "default", "m1")
	if err != nil {
		t.Fatalf("GetMock(): %v", err)
	}
	if got.Name != "renamed" || got.Priority != 5 || got.Group != "g2" {
		t.Errorf("GetMock() after update = %+v, want Name=renamed Priority=5 Group=g2", got)
	}
	if got.Script == nil || *got.Script != *updated.Script {
		t.Errorf("GetMock().Script after update = %+v, want %+v", got.Script, updated.Script)
	}
	if got.Scenario == nil || got.Scenario.OnExhaust != domain.OnExhaustWrap || len(got.Scenario.Responses) != 1 {
		t.Errorf("GetMock().Scenario after update = %+v, want a wrap scenario with 1 response", got.Scenario)
	}

	removed := updated
	removed.Script = nil
	removed.Scenario = nil
	if err := st.UpdateMock(ctx, removed); err != nil {
		t.Fatalf("UpdateMock() removing script/scenario: %v", err)
	}

	got, err = st.GetMock(ctx, "default", "m1")
	if err != nil {
		t.Fatalf("GetMock(): %v", err)
	}
	if got.Script != nil {
		t.Errorf("GetMock().Script after removal = %+v, want nil", got.Script)
	}
	if got.Scenario != nil {
		t.Errorf("GetMock().Scenario after removal = %+v, want nil", got.Scenario)
	}
}

func TestUpdateMockReturnsErrNotFoundForMissingID(t *testing.T) {
	st := openTestStore(t)
	m := sampleMock("nope", "default")
	if err := st.UpdateMock(context.Background(), m); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("UpdateMock() on missing id = %v, want domain.ErrNotFound", err)
	}
}

func TestDeleteMockRemovesRowAndReturnsErrNotFoundForMissingID(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	m := sampleMock("m1", "default")
	if err := st.CreateMock(ctx, m); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}

	if err := st.DeleteMock(ctx, "default", "m1"); err != nil {
		t.Fatalf("DeleteMock(): %v", err)
	}
	if _, err := st.GetMock(ctx, "default", "m1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetMock() after delete = %v, want domain.ErrNotFound", err)
	}

	if err := st.DeleteMock(ctx, "default", "m1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("DeleteMock() on already-deleted id = %v, want domain.ErrNotFound", err)
	}
}

func TestDeleteMocksByPartitionCascadesWithoutTouchingOtherPartitions(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.CreateMock(ctx, sampleMock("m1", "agent-a")); err != nil {
		t.Fatalf("CreateMock(m1, agent-a): %v", err)
	}
	if err := st.CreateMock(ctx, sampleMock("m2", "agent-a")); err != nil {
		t.Fatalf("CreateMock(m2, agent-a): %v", err)
	}
	if err := st.CreateMock(ctx, sampleMock("m3", "agent-b")); err != nil {
		t.Fatalf("CreateMock(m3, agent-b): %v", err)
	}

	if err := st.DeleteMocksByPartition(ctx, "agent-a"); err != nil {
		t.Fatalf("DeleteMocksByPartition(agent-a): %v", err)
	}

	gotA, err := st.ListMocks(ctx, "agent-a")
	if err != nil {
		t.Fatalf("ListMocks(agent-a): %v", err)
	}
	if len(gotA) != 0 {
		t.Errorf("ListMocks(agent-a) after DeleteMocksByPartition = %+v, want empty", gotA)
	}

	gotB, err := st.ListMocks(ctx, "agent-b")
	if err != nil {
		t.Fatalf("ListMocks(agent-b): %v", err)
	}
	if len(gotB) != 1 || gotB[0].ID != "m3" {
		t.Errorf("ListMocks(agent-b) after deleting agent-a = %+v, want untouched [m3]", gotB)
	}
}

// TestCreateMockThenGetRoundTripsTTLSeconds exercises scanMockRow's TTL math:
// CreateMock persists expires_at as CreatedAt + TTLSeconds (in nanoseconds),
// and scanMockRow recomputes TTLSeconds from (expires_at - created_at) on
// read. This proves that recomputation round-trips exactly.
func TestCreateMockThenGetRoundTripsTTLSeconds(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	m := sampleMock("m1", "default")
	ttl := 3600
	m.TTLSeconds = &ttl

	if err := st.CreateMock(ctx, m); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}

	got, err := st.GetMock(ctx, "default", "m1")
	if err != nil {
		t.Fatalf("GetMock(): %v", err)
	}
	if got.TTLSeconds == nil || *got.TTLSeconds != ttl {
		t.Errorf("GetMock().TTLSeconds = %v, want %d", got.TTLSeconds, ttl)
	}
}

func TestCreateMockWithoutTTLRoundTripsNilTTLSeconds(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	m := sampleMock("m1", "default")
	if err := st.CreateMock(ctx, m); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}

	got, err := st.GetMock(ctx, "default", "m1")
	if err != nil {
		t.Fatalf("GetMock(): %v", err)
	}
	if got.TTLSeconds != nil {
		t.Errorf("GetMock().TTLSeconds = %v, want nil", got.TTLSeconds)
	}
}

// TestGetMockTreatsCorruptScriptAndScenarioBlobsAsAbsent confirms the
// documented, deliberate graceful-degrade behavior of decodeMockScript and
// decodeMockScenario (see REFACTORING_REPORT.md): a script/scenario blob
// that fails to decrypt or unmarshal does not fail the whole GetMock call —
// unlike action_blob, Script/Scenario are optional, and the mock is still
// returned with those fields nil'd out rather than erroring or being
// dropped as not-found. This is intentional, not a bug to fix.
func TestGetMockTreatsCorruptScriptAndScenarioBlobsAsAbsent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	m := sampleMock("m1", "default")
	if err := st.CreateMock(ctx, m); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}

	if _, err := st.db.ExecContext(ctx,
		`UPDATE ephemeral_mocks SET script_blob = ?, scenario_blob = ? WHERE id = ? AND "partition" = ?`,
		[]byte("not a sealed blob"), []byte("also not a sealed blob"), "m1", "default",
	); err != nil {
		t.Fatalf("corrupt script/scenario blobs: %v", err)
	}

	got, err := st.GetMock(ctx, "default", "m1")
	if err != nil {
		t.Fatalf("GetMock() with corrupt script/scenario blobs: want nil error (graceful degrade), got %v", err)
	}
	if got.Script != nil {
		t.Errorf("GetMock().Script with corrupt blob = %+v, want nil", got.Script)
	}
	if got.Scenario != nil {
		t.Errorf("GetMock().Scenario with corrupt blob = %+v, want nil", got.Scenario)
	}
	if got.Action.Kind != domain.ActionRespond {
		t.Errorf("GetMock().Action with corrupt script/scenario blobs = %+v, want the action to still decode fine", got.Action)
	}
}

// TestCreateMockConcurrentCallsAllPersist exercises CreateMock under real
// concurrent load: the Admin REST API can receive many simultaneous mock
// creation requests for the same partition, so a lost or corrupted write
// here would silently drop or scramble a fixture. Each goroutine creates a
// distinct, uniquely-identifiable mock; after all finish, ListMocks must
// return exactly all of them, with each mock's distinguishing field intact —
// proving no write is lost, duplicated, or cross-contaminated with another
// mock's fields under concurrent access. Like
// TestAppendTrafficConcurrentCallsAllPersist in traffic_test.go, this proves
// Go-level race-freedom through store.Open's single-connection pool
// (db.SetMaxOpenConns(1)), not multi-connection SQLite atomicity.
func TestCreateMockConcurrentCallsAllPersist(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	const n = 50
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := sampleMock(fmt.Sprintf("mock-%d", i), "default")
			m.Priority = i
			errs[i] = st.CreateMock(ctx, m)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("CreateMock() goroutine %d: %v", i, err)
		}
	}

	got, err := st.ListMocks(ctx, "default")
	if err != nil {
		t.Fatalf("ListMocks(): %v", err)
	}
	if len(got) != n {
		t.Fatalf("ListMocks() returned %d mocks, want %d (some concurrent writes lost or duplicated)", len(got), n)
	}

	byID := make(map[string]domain.Mock, n)
	for _, m := range got {
		if _, dup := byID[m.ID]; dup {
			t.Fatalf("duplicate mock id %q in ListMocks() result", m.ID)
		}
		byID[m.ID] = m
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("mock-%d", i)
		m, ok := byID[id]
		if !ok {
			t.Fatalf("mock %q missing from ListMocks() result after concurrent CreateMock calls", id)
		}
		if m.Priority != i {
			t.Errorf("mock %q Priority = %d, want %d (cross-contamination between concurrent writes)", id, m.Priority, i)
		}
	}
}

// TestUpdateMockConcurrentCallsAllPersist exercises UpdateMock under real
// concurrent load: multiple Admin REST calls can update distinct mocks in
// the same partition at the same time, so a lost or cross-contaminated
// update here would silently corrupt another mock's fixture. N mocks are
// pre-created sequentially, then updated concurrently — one goroutine per
// mock, each changing that mock's Name and Priority to a distinct value.
// After all finish, GetMock on each ID must reflect exactly that goroutine's
// update, proving no update is lost or applied to the wrong row.
func TestUpdateMockConcurrentCallsAllPersist(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	const n = 50
	for i := 0; i < n; i++ {
		m := sampleMock(fmt.Sprintf("mock-%d", i), "default")
		if err := st.CreateMock(ctx, m); err != nil {
			t.Fatalf("CreateMock(%d) setup: %v", i, err)
		}
	}

	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := sampleMock(fmt.Sprintf("mock-%d", i), "default")
			m.Name = fmt.Sprintf("renamed-%d", i)
			m.Priority = 1000 + i
			errs[i] = st.UpdateMock(ctx, m)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("UpdateMock() goroutine %d: %v", i, err)
		}
	}

	for i := 0; i < n; i++ {
		id := fmt.Sprintf("mock-%d", i)
		got, err := st.GetMock(ctx, "default", id)
		if err != nil {
			t.Fatalf("GetMock(%s) after concurrent updates: %v", id, err)
		}
		wantName := fmt.Sprintf("renamed-%d", i)
		wantPriority := 1000 + i
		if got.Name != wantName || got.Priority != wantPriority {
			t.Errorf("GetMock(%s) = {Name: %s, Priority: %d}, want {Name: %s, Priority: %d} (cross-contamination between concurrent updates)",
				id, got.Name, got.Priority, wantName, wantPriority)
		}
	}
}

// TestDeleteMockConcurrentCallsAllSucceed exercises DeleteMock under real
// concurrent load: N mocks are pre-created sequentially in the same
// partition, then deleted concurrently — one goroutine per mock. Every call
// must succeed and, after all finish, ListMocks must return an empty slice,
// proving no delete is lost (leaving a stray row behind) and no delete
// interferes with another goroutine's row.
func TestDeleteMockConcurrentCallsAllSucceed(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	const n = 50
	for i := 0; i < n; i++ {
		m := sampleMock(fmt.Sprintf("mock-%d", i), "default")
		if err := st.CreateMock(ctx, m); err != nil {
			t.Fatalf("CreateMock(%d) setup: %v", i, err)
		}
	}

	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = st.DeleteMock(ctx, "default", fmt.Sprintf("mock-%d", i))
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("DeleteMock() goroutine %d: %v", i, err)
		}
	}

	got, err := st.ListMocks(ctx, "default")
	if err != nil {
		t.Fatalf("ListMocks(): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListMocks() after concurrent deletes = %+v, want empty", got)
	}
}

// TestListMocksConcurrentCallsReturnConsistentSnapshots exercises ListMocks
// itself under concurrent read load: the Admin REST API can serve many
// simultaneous list requests for the same partition, so this proves the
// read path has no race or corruption independent of any concurrent writes.
// N mocks are pre-created sequentially, then M goroutines each independently
// call ListMocks concurrently; every goroutine's result must contain exactly
// all N mocks with the correct IDs.
func TestListMocksConcurrentCallsReturnConsistentSnapshots(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	const n = 50
	for i := 0; i < n; i++ {
		m := sampleMock(fmt.Sprintf("mock-%d", i), "default")
		if err := st.CreateMock(ctx, m); err != nil {
			t.Fatalf("CreateMock(%d) setup: %v", i, err)
		}
	}

	const m = 20
	results := make([][]domain.Mock, m)
	errs := make([]error, m)
	var wg sync.WaitGroup
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = st.ListMocks(ctx, "default")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("ListMocks() goroutine %d: %v", i, err)
		}
	}

	wantIDs := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		wantIDs[fmt.Sprintf("mock-%d", i)] = true
	}
	for i, got := range results {
		if len(got) != n {
			t.Fatalf("ListMocks() goroutine %d returned %d mocks, want %d (inconsistent snapshot under concurrent reads)", i, len(got), n)
		}
		gotIDs := make(map[string]bool, n)
		for _, mock := range got {
			gotIDs[mock.ID] = true
		}
		for id := range wantIDs {
			if !gotIDs[id] {
				t.Errorf("ListMocks() goroutine %d result missing id %q", i, id)
			}
		}
	}
}
