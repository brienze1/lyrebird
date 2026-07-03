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
	if m.Lifetime != domain.LifetimeSeeded {
		t.Errorf("Lifetime = %q, want %q", m.Lifetime, domain.LifetimeSeeded)
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
	writeSeedFile(t, dir, "basic.yaml", `
mocks:
  - name: some-mock
`)
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
	writeSeedFile(t, dir, "a.yaml", "mocks:\n  - name: dup\n")
	writeSeedFile(t, dir, "b.yaml", "mocks:\n  - name: dup\n")

	_, err := Load(dir)
	if !errors.Is(err, domain.ErrDuplicateID) {
		t.Fatalf("Load() with duplicate mock name = %v, want ErrDuplicateID", err)
	}
}

func TestLoadAllowsSameMockNameInDifferentPartitions(t *testing.T) {
	dir := t.TempDir()
	writeSeedFile(t, dir, "a.yaml", "space: team-a\nmocks:\n  - name: same-name\n")
	writeSeedFile(t, dir, "b.yaml", "space: team-b\nmocks:\n  - name: same-name\n")

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
	writeSeedFile(t, dir, "a.yaml", "space: shared\nmocks:\n  - name: mock-a\n")
	writeSeedFile(t, dir, "b.yaml", "space: shared\nmocks:\n  - name: mock-b\n")

	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if len(s.Partitions) != 1 || s.Partitions[0].ID != "shared" {
		t.Fatalf("Partitions = %+v, want a single partition %q", s.Partitions, "shared")
	}
}

func TestLoadRejectsMalformedYAML(t *testing.T) {
	dir := t.TempDir()
	writeSeedFile(t, dir, "broken.yaml", "mocks: [this is not valid: yaml: structure")

	if _, err := Load(dir); err == nil {
		t.Fatal("Load() with malformed YAML, want error")
	}
}
