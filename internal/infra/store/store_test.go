package store

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

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
