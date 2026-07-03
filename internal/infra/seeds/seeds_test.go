package seeds

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

func writeSeedFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write seed file %s: %v", name, err)
	}
}

func TestLoadMissingDirReturnsEmptySeeds(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if len(s.Mocks) != 0 || len(s.Upstreams) != 0 {
		t.Fatalf("Load() on missing dir = %+v, want empty Seeds", s)
	}
}

// minimalAction is a valid action block (contracts/seed-config.md requires
// exactly one of respond/proxy/fault per mock) reused by fixtures that only
// care about name/priority/partition, not response content.
const minimalAction = "    action:\n      respond:\n        status: 200\n        body: \"ok\"\n"

func TestLoadParsesMocksAndUpstreams(t *testing.T) {
	dir := t.TempDir()
	writeSeedFile(t, dir, "payments.yaml", `
space: payments-team
upstreams:
  - match_host: "api.stripe.com"
    target_url: "https://api.stripe.com"
mocks:
  - name: charge-declined
    priority: 100
    match:
      method: POST
      path: /v1/charges
      body:
        - jsonpath: amount
          equals: "666"
    action:
      respond:
        status: 402
        headers: { Content-Type: application/json }
        body: '{"error":{"code":"card_declined"}}'
`)

	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if len(s.Mocks) != 1 {
		t.Fatalf("Mocks = %d, want 1", len(s.Mocks))
	}
	m := s.Mocks[0]
	if m.Partition != "payments-team" || m.Name != "charge-declined" || m.Priority != 100 {
		t.Errorf("Mock = %+v, unexpected", m)
	}
	if m.ID != "charge-declined" {
		t.Errorf("ID = %q, want %q (just the name, not \"partition/name\" — Go's ServeMux {id} wildcard can't match a \"/\")", m.ID, "charge-declined")
	}
	if m.Lifetime != domain.LifetimeSeeded {
		t.Errorf("Lifetime = %q, want %q", m.Lifetime, domain.LifetimeSeeded)
	}
	if m.Match.Method != "POST" || m.Match.Path != "/v1/charges" {
		t.Errorf("Match = %+v, unexpected", m.Match)
	}
	if len(m.Match.Body) != 1 || m.Match.Body[0].Path != "amount" || m.Match.Body[0].Matcher.Equals == nil || *m.Match.Body[0].Matcher.Equals != "666" {
		t.Errorf("Match.Body = %+v, unexpected", m.Match.Body)
	}
	if m.Action.Kind != domain.ActionRespond || m.Action.Respond == nil || m.Action.Respond.Status != 402 {
		t.Errorf("Action = %+v, unexpected", m.Action)
	}
	if len(s.Upstreams) != 1 || s.Upstreams[0].TargetURL != "https://api.stripe.com" {
		t.Errorf("Upstreams = %+v, unexpected", s.Upstreams)
	}
	if len(s.Partitions) != 1 || s.Partitions[0].ID != "payments-team" {
		t.Errorf("Partitions = %+v, want one partition %q", s.Partitions, "payments-team")
	}
}

func TestLoadDefaultsToDefaultPartitionWhenSpaceOmitted(t *testing.T) {
	dir := t.TempDir()
	writeSeedFile(t, dir, "basic.yaml", "mocks:\n  - name: some-mock\n"+minimalAction)
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if len(s.Mocks) != 1 || s.Mocks[0].Partition != domain.DefaultPartitionID {
		t.Fatalf("Mocks = %+v, want partition %q", s.Mocks, domain.DefaultPartitionID)
	}
}

func TestLoadRejectsDuplicateMockNameInSamePartition(t *testing.T) {
	dir := t.TempDir()
	writeSeedFile(t, dir, "a.yaml", "mocks:\n  - name: dup\n"+minimalAction)
	writeSeedFile(t, dir, "b.yaml", "mocks:\n  - name: dup\n"+minimalAction)

	_, err := Load(dir)
	if !errors.Is(err, domain.ErrDuplicateID) {
		t.Fatalf("Load() with duplicate mock name = %v, want ErrDuplicateID", err)
	}
}

func TestLoadAllowsSameMockNameInDifferentPartitions(t *testing.T) {
	dir := t.TempDir()
	writeSeedFile(t, dir, "a.yaml", "space: team-a\nmocks:\n  - name: same-name\n"+minimalAction)
	writeSeedFile(t, dir, "b.yaml", "space: team-b\nmocks:\n  - name: same-name\n"+minimalAction)

	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if len(s.Mocks) != 2 {
		t.Fatalf("Mocks = %d, want 2", len(s.Mocks))
	}
	if len(s.Partitions) != 2 {
		t.Fatalf("Partitions = %d, want 2 (team-a, team-b), got %+v", len(s.Partitions), s.Partitions)
	}
}

func TestLoadDoesNotDuplicatePartitionsAcrossMultipleFilesInSameSpace(t *testing.T) {
	dir := t.TempDir()
	writeSeedFile(t, dir, "a.yaml", "space: shared\nmocks:\n  - name: mock-a\n"+minimalAction)
	writeSeedFile(t, dir, "b.yaml", "space: shared\nmocks:\n  - name: mock-b\n"+minimalAction)

	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if len(s.Partitions) != 1 || s.Partitions[0].ID != "shared" {
		t.Fatalf("Partitions = %+v, want a single partition %q", s.Partitions, "shared")
	}
}

func TestLoadRejectsMockWithoutAction(t *testing.T) {
	dir := t.TempDir()
	writeSeedFile(t, dir, "a.yaml", "mocks:\n  - name: no-action\n")

	if _, err := Load(dir); err == nil {
		t.Fatal("Load() with a mock declaring no action, want error")
	}
}

func TestLoadRejectsMockNameContainingSlash(t *testing.T) {
	dir := t.TempDir()
	writeSeedFile(t, dir, "a.yaml", "mocks:\n  - name: bad/name\n"+minimalAction)

	if _, err := Load(dir); err == nil {
		t.Fatal("Load() with a mock name containing \"/\", want error (it becomes the mock's id, and GET/PUT/DELETE /mocks/{id} can't route a multi-segment id)")
	}
}

func TestLoadRejectsMalformedYAML(t *testing.T) {
	dir := t.TempDir()
	writeSeedFile(t, dir, "broken.yaml", "mocks: [this is not valid: yaml: structure")

	if _, err := Load(dir); err == nil {
		t.Fatal("Load() with malformed YAML, want error")
	}
}
