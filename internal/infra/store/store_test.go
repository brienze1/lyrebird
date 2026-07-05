package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/infra/crypto"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mustSealer(t *testing.T) crypto.Sealer {
	t.Helper()
	key, err := crypto.NewRandomKey()
	if err != nil {
		t.Fatalf("NewRandomKey(): %v", err)
	}
	s, err := crypto.New(key)
	if err != nil {
		t.Fatalf("crypto.New(): %v", err)
	}
	return s
}

// TestOpenCreatesParentDirectoryIfMissing is a regression test for a real
// boot failure found by running the built Docker image without a mounted
// /data volume: the fully-ephemeral, no-volume deployment mode documented
// in docs/DESIGN.md requires Open to create its own parent directory, not
// merely tolerate a missing *file* in an already-existing directory.
func TestOpenCreatesParentDirectoryIfMissing(t *testing.T) {
	base := t.TempDir()
	// Deliberately nested and NOT pre-created — this is the scenario a
	// container with no mounted volume actually hits.
	path := filepath.Join(base, "does", "not", "exist", "lyrebird.db")

	st, err := Open(context.Background(), path, mustSealer(t), silentLogger())
	if err != nil {
		t.Fatalf("Open() with missing parent directory chain: %v", err)
	}
	defer func() { _ = st.Close() }()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected database file to exist at %s: %v", path, err)
	}
}

func TestOpenSucceedsOnMissingFileInExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lyrebird.db")

	st, err := Open(context.Background(), path, mustSealer(t), silentLogger())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer func() { _ = st.Close() }()
}

func TestOpenQuarantinesCorruptFileAndStartsFresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lyrebird.db")
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("write corrupt fixture: %v", err)
	}

	st, err := Open(context.Background(), path, mustSealer(t), silentLogger())
	if err != nil {
		t.Fatalf("Open() with corrupt file: %v", err)
	}
	defer func() { _ = st.Close() }()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var sawQuarantined bool
	for _, e := range entries {
		if e.Name() != "lyrebird.db" && filepath.Ext(e.Name()) != "" {
			sawQuarantined = true
		}
	}
	if !sawQuarantined {
		t.Errorf("expected the corrupt file to be quarantined (renamed aside), dir contents: %v", entries)
	}
}

func TestListEphemeralMockIDsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lyrebird.db")
	sealer := mustSealer(t)

	st, err := Open(context.Background(), path, sealer, silentLogger())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	if err := st.InsertRawEphemeralMock(ctx, sealer, "default", "m1", "m1", []byte(`{"kind":"respond"}`)); err != nil {
		t.Fatalf("InsertRawEphemeralMock(): %v", err)
	}

	ids, err := st.ListEphemeralMockIDs(ctx, "default")
	if err != nil {
		t.Fatalf("ListEphemeralMockIDs(): %v", err)
	}
	if len(ids) != 1 || ids[0] != "m1" {
		t.Errorf("ids = %v, want [m1]", ids)
	}
}

func TestQuarantineRenamesMainFileAndSidecarsAside(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lyrebird.db")
	if err := os.WriteFile(path, []byte("main"), 0o600); err != nil {
		t.Fatalf("write main fixture: %v", err)
	}
	if err := os.WriteFile(path+"-wal", []byte("wal"), 0o600); err != nil {
		t.Fatalf("write wal fixture: %v", err)
	}
	if err := os.WriteFile(path+"-shm", []byte("shm"), 0o600); err != nil {
		t.Fatalf("write shm fixture: %v", err)
	}

	dest, err := quarantine(path)
	if err != nil {
		t.Fatalf("quarantine(): %v", err)
	}
	if dest == "" {
		t.Fatalf("quarantine() returned empty dest for an existing file")
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("main file still present at original path %s after quarantine: err=%v", path, err)
	}
	if _, err := os.Stat(path + "-wal"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("wal sidecar still present at original path after quarantine: err=%v", err)
	}
	if _, err := os.Stat(path + "-shm"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("shm sidecar still present at original path after quarantine: err=%v", err)
	}

	if b, err := os.ReadFile(dest); err != nil || string(b) != "main" {
		t.Errorf("quarantined main file content = %q, %v, want %q, nil", b, err, "main")
	}
	suffix := dest[len(path):]
	if b, err := os.ReadFile(path + "-wal" + suffix); err != nil || string(b) != "wal" {
		t.Errorf("quarantined wal sidecar content = %q, %v, want %q, nil", b, err, "wal")
	}
	if b, err := os.ReadFile(path + "-shm" + suffix); err != nil || string(b) != "shm" {
		t.Errorf("quarantined shm sidecar content = %q, %v, want %q, nil", b, err, "shm")
	}
}

func TestQuarantineIsNoOpWhenMainFileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lyrebird.db")

	dest, err := quarantine(path)
	if err != nil {
		t.Fatalf("quarantine() on missing file: %v", err)
	}
	if dest != "" {
		t.Errorf("quarantine() dest = %q, want empty for a missing file", dest)
	}
}

// TestQuarantineRollsBackSidecarRenamesWhenMainFileRenameFails simulates a
// failure partway through the rename sequence using the renameFile test
// seam: both sidecar renames (wal, shm) succeed for real, then the main
// file's rename is made to fail. This proves quarantine() rolls back the
// already-successful sidecar renames — restoring them to their original
// paths — rather than leaving the filesystem in a partially-quarantined
// state. A directory-permission approach can't isolate this failure point,
// since the main file and its sidecars all live in the same directory and a
// permission change would block every rename in the sequence equally, not
// just a later one.
func TestQuarantineRollsBackSidecarRenamesWhenMainFileRenameFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lyrebird.db")
	writeQuarantineFixture(t, path, "main")
	writeQuarantineFixture(t, path+"-wal", "wal")
	writeQuarantineFixture(t, path+"-shm", "shm")

	orig := renameFile
	t.Cleanup(func() { renameFile = orig })
	var calls int
	renameFile = func(oldpath, newpath string) error {
		calls++
		if calls == 3 { // the third rename is the main file's forward rename
			return fmt.Errorf("injected failure renaming %s", oldpath)
		}
		return orig(oldpath, newpath)
	}

	if _, err := quarantine(path); err == nil {
		t.Fatalf("quarantine() with injected failure on the main file rename: want an error, got nil")
	}

	if b, err := os.ReadFile(path); err != nil || string(b) != "main" {
		t.Errorf("main file not restored to original path %s after failed quarantine: content=%q err=%v", path, b, err)
	}
	if b, err := os.ReadFile(path + "-wal"); err != nil || string(b) != "wal" {
		t.Errorf("wal sidecar not rolled back to original path after failed quarantine: content=%q err=%v", b, err)
	}
	if b, err := os.ReadFile(path + "-shm"); err != nil || string(b) != "shm" {
		t.Errorf("shm sidecar not rolled back to original path after failed quarantine: content=%q err=%v", b, err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	want := map[string]bool{"lyrebird.db": true, "lyrebird.db-wal": true, "lyrebird.db-shm": true}
	if len(entries) != len(want) {
		t.Errorf("dir entries after failed quarantine+rollback = %v, want exactly %v", entries, want)
	}
	for _, e := range entries {
		if !want[e.Name()] {
			t.Errorf("unexpected leftover quarantine file after rollback: %s", e.Name())
		}
	}
}

// TestQuarantineErrorMentionsRollbackFailure covers the doubly-bad case: the
// forward rename of the main file fails AND the subsequent rollback of one
// of the already-renamed sidecars also fails. quarantine() must not swallow
// this — the returned error must surface the rollback failure clearly, since
// quarantine() has no logger of its own to report it through separately.
func TestQuarantineErrorMentionsRollbackFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lyrebird.db")
	writeQuarantineFixture(t, path, "main")
	writeQuarantineFixture(t, path+"-wal", "wal")
	writeQuarantineFixture(t, path+"-shm", "shm")

	orig := renameFile
	t.Cleanup(func() { renameFile = orig })
	var calls int
	renameFile = func(oldpath, newpath string) error {
		calls++
		switch calls {
		case 3: // main file forward rename fails
			return errors.New("injected forward failure")
		case 5: // rollback of the wal sidecar (restored last, since rollback runs in reverse order) also fails
			return errors.New("injected rollback failure")
		default:
			return orig(oldpath, newpath)
		}
	}

	_, err := quarantine(path)
	if err == nil {
		t.Fatalf("quarantine(): want error, got nil")
	}
	if !strings.Contains(err.Error(), "rollback") {
		t.Errorf("quarantine() error = %q, want it to clearly mention the rollback failure", err.Error())
	}
}

func writeQuarantineFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}

func TestPruneTrafficAndExpiredEphemeralMocksAreNoOpOnEmptyStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lyrebird.db")

	st, err := Open(context.Background(), path, mustSealer(t), silentLogger())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	now := time.Now()
	if n, err := st.PruneTraffic(ctx, now); err != nil || n != 0 {
		t.Errorf("PruneTraffic() = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := st.PruneExpiredEphemeralMocks(ctx, now); err != nil || n != 0 {
		t.Errorf("PruneExpiredEphemeralMocks() = (%d, %v), want (0, nil)", n, err)
	}
}

// TestPruneExpiredEphemeralMocksAlsoCleansUpScenarioState is a regression
// test: a scenario mock whose Scenario action has advanced past index 0
// (so a scenario_state row exists) and then expires via TTL used to leave
// that scenario_state row orphaned forever, since nothing else prunes
// scenario_state on TTL-based expiry (mock_crud.go's Update/Delete already
// call ResetScenario, but that's only reached through the use-case path, not
// GC). It confirms both that the expired ephemeral mock row is gone AND
// that the orphaned scenario_state row is cleaned up in the same call —
// checking ScenarioIndex is nonzero *before* pruning first, so the
// post-prune zero can't be confused with "there was never a row".
func TestPruneExpiredEphemeralMocksAlsoCleansUpScenarioState(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	m := sampleMock("m1", "default")
	ttl := 1 // seconds
	m.TTLSeconds = &ttl
	m.CreatedAt = time.Now().Add(-time.Hour) // already expired by the time we prune
	if err := st.CreateMock(ctx, m); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}

	if _, err := st.AdvanceScenario(ctx, "default", "m1"); err != nil {
		t.Fatalf("AdvanceScenario(): %v", err)
	}

	// Confirm there really is something to clean up before pruning.
	if idx, err := st.ScenarioIndex(ctx, "default", "m1"); err != nil || idx == 0 {
		t.Fatalf("ScenarioIndex() before prune = (%d, %v), want a nonzero index (a scenario_state row must exist)", idx, err)
	}

	n, err := st.PruneExpiredEphemeralMocks(ctx, time.Now())
	if err != nil {
		t.Fatalf("PruneExpiredEphemeralMocks(): %v", err)
	}
	if n != 1 {
		t.Fatalf("PruneExpiredEphemeralMocks() = %d, want 1", n)
	}

	if _, err := st.GetMock(ctx, "default", "m1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetMock() after prune = %v, want domain.ErrNotFound", err)
	}
	if idx, err := st.ScenarioIndex(ctx, "default", "m1"); err != nil || idx != 0 {
		t.Errorf("ScenarioIndex() after prune = (%d, %v), want (0, nil) — orphaned scenario_state row must be cleaned up", idx, err)
	}
}

// TestPruneExpiredEphemeralMocksOnlyTouchesScenarioStateOfPrunedMocks proves
// the scenario_state cleanup added to PruneExpiredEphemeralMocks is scoped
// exactly to the mocks it prunes: a still-live (non-expired) mock's own
// scenario_state row in a different partition must survive untouched.
func TestPruneExpiredEphemeralMocksOnlyTouchesScenarioStateOfPrunedMocks(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	expired := sampleMock("expired", "agent-a")
	ttl := 1
	expired.TTLSeconds = &ttl
	expired.CreatedAt = time.Now().Add(-time.Hour)
	if err := st.CreateMock(ctx, expired); err != nil {
		t.Fatalf("CreateMock(expired): %v", err)
	}
	if _, err := st.AdvanceScenario(ctx, "agent-a", "expired"); err != nil {
		t.Fatalf("AdvanceScenario(agent-a/expired): %v", err)
	}

	live := sampleMock("live", "agent-b")
	if err := st.CreateMock(ctx, live); err != nil {
		t.Fatalf("CreateMock(live): %v", err)
	}
	if _, err := st.AdvanceScenario(ctx, "agent-b", "live"); err != nil {
		t.Fatalf("AdvanceScenario(agent-b/live): %v", err)
	}

	// Confirm both scenario_state rows exist before pruning.
	if idx, err := st.ScenarioIndex(ctx, "agent-a", "expired"); err != nil || idx == 0 {
		t.Fatalf("ScenarioIndex(agent-a/expired) before prune = (%d, %v), want nonzero", idx, err)
	}
	if idx, err := st.ScenarioIndex(ctx, "agent-b", "live"); err != nil || idx == 0 {
		t.Fatalf("ScenarioIndex(agent-b/live) before prune = (%d, %v), want nonzero", idx, err)
	}

	if _, err := st.PruneExpiredEphemeralMocks(ctx, time.Now()); err != nil {
		t.Fatalf("PruneExpiredEphemeralMocks(): %v", err)
	}

	if idx, err := st.ScenarioIndex(ctx, "agent-a", "expired"); err != nil || idx != 0 {
		t.Errorf("ScenarioIndex(agent-a/expired) after prune = (%d, %v), want (0, nil)", idx, err)
	}
	if idx, err := st.ScenarioIndex(ctx, "agent-b", "live"); err != nil || idx != 1 {
		t.Errorf("ScenarioIndex(agent-b/live) after prune = (%d, %v), want untouched 1", idx, err)
	}
}
