package support

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cucumber/godog"

	"github.com/brienze1/lyrebird/internal/bootstrap"
	"github.com/brienze1/lyrebird/internal/infra/config"
	"github.com/brienze1/lyrebird/internal/infra/crypto"
	"github.com/brienze1/lyrebird/internal/infra/store"
)

// disposabilityState carries fixtures and outcomes between steps within one
// scenario. godog runs scenarios with a fresh instance each time.
type disposabilityState struct {
	dataDir string
	dbPath  string
	seedDir string

	app     *bootstrap.App
	bootErr error
}

func (s *disposabilityState) aFreshTemporaryLyrebirdDataDirectory() error {
	s.dataDir = mustTempDir()
	s.dbPath = filepath.Join(s.dataDir, "lyrebird.db")
	s.seedDir = filepath.Join(s.dataDir, "config")
	return os.MkdirAll(s.seedDir, 0o755)
}

func (s *disposabilityState) noDatabaseFileExistsAtTheConfiguredPath() error {
	return nil // dbPath simply doesn't exist yet — nothing to do
}

func (s *disposabilityState) aCorruptedNonSQLiteFileExistsAtTheConfiguredPath() error {
	return os.WriteFile(s.dbPath, []byte("this is not a valid sqlite database file, just garbage bytes"), 0o600)
}

func (s *disposabilityState) aDatabaseAtTheConfiguredPathContainsAnEphemeralMockInPartitionEncryptedWithDataKey(
	ctx context.Context, mockName, partition, keyLabel string,
) error {
	sealer, err := crypto.New(keyForLabel(keyLabel))
	if err != nil {
		return err
	}

	st, err := store.Open(ctx, s.dbPath, sealer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return fmt.Errorf("fixture: open store: %w", err)
	}
	defer func() { _ = st.Close() }()

	return st.InsertRawEphemeralMock(ctx, sealer, partition, mockName, mockName, []byte(`{"kind":"respond"}`))
}

func (s *disposabilityState) aSeedFileDeclaresAMockNamedInPartition(mockName, partition string) error {
	content := fmt.Sprintf("space: %s\nmocks:\n  - name: %s\n", partition, mockName)
	return os.WriteFile(filepath.Join(s.seedDir, "seed.yaml"), []byte(content), 0o600)
}

func (s *disposabilityState) lyrebirdBoots(ctx context.Context) error {
	return s.bootWithDataKey(ctx, "")
}

func (s *disposabilityState) lyrebirdBootsWithDataKey(ctx context.Context, keyLabel string) error {
	b64 := base64.StdEncoding.EncodeToString(keyForLabel(keyLabel))
	return s.bootWithDataKey(ctx, b64)
}

func (s *disposabilityState) bootWithDataKey(ctx context.Context, dataKeyB64 string) error {
	cfg := config.Config{
		DataPlaneAddr:    "127.0.0.1:0",
		ControlPlaneAddr: "127.0.0.1:0",
		DefaultSpace:     "default",
		TrafficTTL:       time.Hour,
		TokenTTL:         time.Hour,
		BodyCapBytes:     1 << 20,
		DBPath:           s.dbPath,
		SeedDir:          s.seedDir,
		GCInterval:       time.Hour, // scenario doesn't need GC to actually fire
		DataKeyB64:       dataKeyB64,
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := bootstrap.Run(ctx, cfg, log)
	s.app, s.bootErr = app, err
	return nil // the outcome is asserted by a later step, not here
}

func (s *disposabilityState) bootSucceeds() error {
	if s.bootErr != nil {
		return fmt.Errorf("expected boot to succeed, got error: %w", s.bootErr)
	}
	if s.app == nil {
		return fmt.Errorf("expected a running app, got nil")
	}
	return nil
}

func (s *disposabilityState) theControlPlaneReportsReady(ctx context.Context) error {
	url := fmt.Sprintf("http://%s/__lyrebird/readyz", s.app.ControlAddr())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build readyz request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET /__lyrebird/readyz: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("readyz status = %d, want 200", resp.StatusCode)
	}
	return nil
}

func (s *disposabilityState) listingEphemeralMocksForPartitionReturnsZeroResults(ctx context.Context, partition string) error {
	ids, err := s.app.Store.ListEphemeralMockIDs(ctx, partition)
	if err != nil {
		return fmt.Errorf("expected zero results (row treated as absent), got error: %w", err)
	}
	if len(ids) != 0 {
		return fmt.Errorf("expected zero ephemeral mocks in partition %q, got %v", partition, ids)
	}
	return nil
}

func (s *disposabilityState) theSeededMockIsPresentInPartition(mockName, partition string) error {
	for _, m := range s.app.Seeds.Mocks {
		if m.Name == mockName && m.Partition == partition {
			return nil
		}
	}
	return fmt.Errorf("seeded mock %q not found in partition %q (loaded: %+v)", mockName, partition, s.app.Seeds.Mocks)
}

// keyForLabel maps a human-readable Gherkin label ("keyA"/"keyB") to a
// stable, deterministic 32-byte key so scenarios are reproducible.
func keyForLabel(label string) []byte {
	key := make([]byte, 32)
	copy(key, label)
	return key
}

func mustTempDir() string {
	dir, err := os.MkdirTemp("", "lyrebird-disposability-*")
	if err != nil {
		panic(err)
	}
	return dir
}

// RegisterDisposabilitySteps wires disposability.feature's steps into ctx.
func RegisterDisposabilitySteps(sc *godog.ScenarioContext) {
	s := &disposabilityState{}

	sc.Step(`^a fresh temporary Lyrebird data directory$`, s.aFreshTemporaryLyrebirdDataDirectory)
	sc.Step(`^no database file exists at the configured path$`, s.noDatabaseFileExistsAtTheConfiguredPath)
	sc.Step(`^a corrupted \(non-SQLite\) file exists at the configured path$`, s.aCorruptedNonSQLiteFileExistsAtTheConfiguredPath)
	sc.Step(`^a database at the configured path contains an ephemeral mock "([^"]*)" in partition "([^"]*)" encrypted with data key "([^"]*)"$`,
		s.aDatabaseAtTheConfiguredPathContainsAnEphemeralMockInPartitionEncryptedWithDataKey)
	sc.Step(`^a seed file declares a mock named "([^"]*)" in partition "([^"]*)"$`, s.aSeedFileDeclaresAMockNamedInPartition)
	sc.Step(`^Lyrebird boots$`, s.lyrebirdBoots)
	sc.Step(`^Lyrebird boots with data key "([^"]*)"$`, s.lyrebirdBootsWithDataKey)
	sc.Step(`^boot succeeds$`, s.bootSucceeds)
	sc.Step(`^the control plane reports ready$`, s.theControlPlaneReportsReady)
	sc.Step(`^listing ephemeral mocks for partition "([^"]*)" returns zero results$`, s.listingEphemeralMocksForPartitionReturnsZeroResults)
	sc.Step(`^the seeded mock "([^"]*)" is present in partition "([^"]*)"$`, s.theSeededMockIsPresentInPartition)

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if s.app != nil {
			_ = s.app.Shutdown(ctx)
		}
		if s.dataDir != "" {
			_ = os.RemoveAll(s.dataDir)
		}
		return ctx, nil
	})
}
