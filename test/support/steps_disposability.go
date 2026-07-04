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

// appState carries the running app + fixtures shared by every feature that
// needs a booted Lyrebird instance (disposability.feature, spy_record.feature,
// ...). One instance is created per scenario by InitializeScenario and
// passed to each feature's Register*Steps function, so "a fresh temporary
// Lyrebird data directory" / "Lyrebird boots" are registered exactly once
// regardless of how many feature files reuse that phrasing.
type appState struct {
	dataDir string
	dbPath  string
	seedDir string

	// Optional pre-boot config overrides; zero value means "use the
	// built-in test default" (set in bootWithDataKey). Must be set via a
	// Given step BEFORE the "Lyrebird boots" step runs — config is baked in
	// at boot time, there is no live reload.
	upstreamTimeout time.Duration
	bodyCapBytes    int64
	gcInterval      time.Duration
	trafficTTL      time.Duration

	app     *bootstrap.App
	bootErr error

	// preShutdownCleanup runs (in order) before app.Shutdown in this
	// package's own After hook below — for state owned by other
	// Register*Steps functions (e.g. steps_mcp.go's client session) that
	// must close before the control-plane listener shuts down, not after:
	// Shutdown's graceful-drain waits up to its own timeout for any
	// still-open connection, so an unclosed MCP Streamable HTTP session
	// otherwise adds that whole timeout to every scenario that uses one.
	preShutdownCleanup []func()
}

func (s *appState) aFreshTemporaryLyrebirdDataDirectory() error {
	s.dataDir = mustTempDir()
	s.dbPath = filepath.Join(s.dataDir, "lyrebird.db")
	s.seedDir = filepath.Join(s.dataDir, "config")
	return os.MkdirAll(s.seedDir, 0o755)
}

func (s *appState) lyrebirdBoots(ctx context.Context) error {
	return s.bootWithDataKey(ctx, "")
}

func (s *appState) lyrebirdBootsWithDataKey(ctx context.Context, keyLabel string) error {
	b64 := base64.StdEncoding.EncodeToString(keyForLabel(keyLabel))
	return s.bootWithDataKey(ctx, b64)
}

func (s *appState) upstreamTimeoutIsConfiguredTo(d string) error {
	parsed, err := time.ParseDuration(d)
	if err != nil {
		return fmt.Errorf("parse upstream timeout %q: %w", d, err)
	}
	s.upstreamTimeout = parsed
	return nil
}

func (s *appState) bodyCapIsConfiguredToBytes(n int64) error {
	s.bodyCapBytes = n
	return nil
}

func (s *appState) theGCIntervalIsConfiguredTo(d string) error {
	parsed, err := time.ParseDuration(d)
	if err != nil {
		return fmt.Errorf("parse GC interval %q: %w", d, err)
	}
	s.gcInterval = parsed
	return nil
}

func (s *appState) theTrafficTTLIsConfiguredTo(d string) error {
	parsed, err := time.ParseDuration(d)
	if err != nil {
		return fmt.Errorf("parse traffic TTL %q: %w", d, err)
	}
	s.trafficTTL = parsed
	return nil
}

func (s *appState) bootWithDataKey(ctx context.Context, dataKeyB64 string) error {
	upstreamTimeout := s.upstreamTimeout
	if upstreamTimeout == 0 {
		upstreamTimeout = 10 * time.Second
	}
	bodyCapBytes := s.bodyCapBytes
	if bodyCapBytes == 0 {
		bodyCapBytes = 1 << 20
	}
	gcInterval := s.gcInterval
	if gcInterval == 0 {
		gcInterval = time.Hour // most scenarios don't need GC to actually fire
	}
	trafficTTL := s.trafficTTL
	if trafficTTL == 0 {
		trafficTTL = time.Hour
	}

	cfg := config.Config{
		DataPlaneAddr:    "127.0.0.1:0",
		ControlPlaneAddr: "127.0.0.1:0",
		DefaultSpace:     "default",
		TrafficTTL:       trafficTTL,
		TokenTTL:         time.Hour,
		BodyCapBytes:     bodyCapBytes,
		UpstreamTimeout:  upstreamTimeout,
		DBPath:           s.dbPath,
		SeedDir:          s.seedDir,
		GCInterval:       gcInterval,
		DataKeyB64:       dataKeyB64,
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := bootstrap.Run(ctx, cfg, log)
	s.app, s.bootErr = app, err
	return nil // the outcome is asserted by a later step, not here
}

func (s *appState) bootSucceeds() error {
	if s.bootErr != nil {
		return fmt.Errorf("expected boot to succeed, got error: %w", s.bootErr)
	}
	if s.app == nil {
		return fmt.Errorf("expected a running app, got nil")
	}
	return nil
}

func (s *appState) theControlPlaneReportsReady(ctx context.Context) error {
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

// RegisterCoreAppSteps wires the app-lifecycle steps shared by every
// feature that boots Lyrebird: fresh temp dir, boot (with/without a data
// key), boot-succeeds, and readiness. Also owns the after-scenario cleanup
// (shutdown + temp dir removal), since that's lifecycle, not
// disposability-specific behavior.
func RegisterCoreAppSteps(sc *godog.ScenarioContext, s *appState) {
	sc.Step(`^a fresh temporary Lyrebird data directory$`, s.aFreshTemporaryLyrebirdDataDirectory)
	sc.Step(`^the upstream timeout is configured to "([^"]*)"$`, s.upstreamTimeoutIsConfiguredTo)
	sc.Step(`^the body cap is configured to "(\d+)" bytes$`, s.bodyCapIsConfiguredToBytes)
	sc.Step(`^the GC interval is configured to "([^"]*)"$`, s.theGCIntervalIsConfiguredTo)
	sc.Step(`^the traffic TTL is configured to "([^"]*)"$`, s.theTrafficTTLIsConfiguredTo)
	sc.Step(`^Lyrebird boots$`, s.lyrebirdBoots)
	sc.Step(`^Lyrebird boots with data key "([^"]*)"$`, s.lyrebirdBootsWithDataKey)
	sc.Step(`^boot succeeds$`, s.bootSucceeds)
	sc.Step(`^the control plane reports ready$`, s.theControlPlaneReportsReady)

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		for _, cleanup := range s.preShutdownCleanup {
			cleanup()
		}
		if s.app != nil {
			_ = s.app.Shutdown(ctx)
		}
		if s.dataDir != "" {
			_ = os.RemoveAll(s.dataDir)
		}
		return ctx, nil
	})
}

// disposabilityFixtures holds disposability.feature's own fixture steps —
// separate from appState so RegisterDisposabilitySteps only needs a
// reference to the shared appState, not ownership of it.
type disposabilityFixtures struct{ s *appState }

func (f *disposabilityFixtures) noDatabaseFileExistsAtTheConfiguredPath() error {
	return nil // dbPath simply doesn't exist yet — nothing to do
}

func (f *disposabilityFixtures) aCorruptedNonSQLiteFileExistsAtTheConfiguredPath() error {
	return os.WriteFile(f.s.dbPath, []byte("this is not a valid sqlite database file, just garbage bytes"), 0o600)
}

func (f *disposabilityFixtures) aDatabaseAtTheConfiguredPathContainsAnEphemeralMockInPartitionEncryptedWithDataKey(
	ctx context.Context, mockName, partition, keyLabel string,
) error {
	sealer, err := crypto.New(keyForLabel(keyLabel))
	if err != nil {
		return err
	}

	st, err := store.Open(ctx, f.s.dbPath, sealer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return fmt.Errorf("fixture: open store: %w", err)
	}
	defer func() { _ = st.Close() }()

	return st.InsertRawEphemeralMock(ctx, sealer, partition, mockName, mockName, []byte(`{"kind":"respond"}`))
}

func (f *disposabilityFixtures) aSeedFileDeclaresAMockNamedInPartition(mockName, partition string) error {
	content := fmt.Sprintf(
		"space: %s\nmocks:\n  - name: %s\n    match: {}\n    action:\n      respond:\n        status: 200\n        body: \"ok\"\n",
		partition, mockName,
	)
	return os.WriteFile(filepath.Join(f.s.seedDir, "seed.yaml"), []byte(content), 0o600)
}

func (f *disposabilityFixtures) listingEphemeralMocksForPartitionReturnsZeroResults(ctx context.Context, partition string) error {
	ids, err := f.s.app.Store.ListEphemeralMockIDs(ctx, partition)
	if err != nil {
		return fmt.Errorf("expected zero results (row treated as absent), got error: %w", err)
	}
	if len(ids) != 0 {
		return fmt.Errorf("expected zero ephemeral mocks in partition %q, got %v", partition, ids)
	}
	return nil
}

func (f *disposabilityFixtures) theSeededMockIsPresentInPartition(mockName, partition string) error {
	for _, m := range f.s.app.Seeds.Mocks {
		if m.Name == mockName && m.Partition == partition {
			return nil
		}
	}
	return fmt.Errorf("seeded mock %q not found in partition %q (loaded: %+v)", mockName, partition, f.s.app.Seeds.Mocks)
}

// keyForLabel maps a human-readable Gherkin label ("keyA"/"keyB") to a
// stable, deterministic 32-byte key so scenarios are reproducible.
func keyForLabel(label string) []byte {
	key := make([]byte, 32)
	copy(key, label)
	return key
}

func mustTempDir() string {
	dir, err := os.MkdirTemp("", "lyrebird-bdd-*")
	if err != nil {
		panic(err)
	}
	return dir
}

// RegisterDisposabilitySteps wires disposability.feature's own steps
// against the shared appState s (created by InitializeScenario, wired to
// RegisterCoreAppSteps for the app-lifecycle steps this feature also uses).
func RegisterDisposabilitySteps(sc *godog.ScenarioContext, s *appState) {
	f := &disposabilityFixtures{s: s}

	sc.Step(`^no database file exists at the configured path$`, f.noDatabaseFileExistsAtTheConfiguredPath)
	sc.Step(`^a corrupted \(non-SQLite\) file exists at the configured path$`, f.aCorruptedNonSQLiteFileExistsAtTheConfiguredPath)
	sc.Step(`^a database at the configured path contains an ephemeral mock "([^"]*)" in partition "([^"]*)" encrypted with data key "([^"]*)"$`,
		f.aDatabaseAtTheConfiguredPathContainsAnEphemeralMockInPartitionEncryptedWithDataKey)
	sc.Step(`^a seed file declares a mock named "([^"]*)" in partition "([^"]*)"$`, f.aSeedFileDeclaresAMockNamedInPartition)
	sc.Step(`^listing ephemeral mocks for partition "([^"]*)" returns zero results$`, f.listingEphemeralMocksForPartitionReturnsZeroResults)
	sc.Step(`^the seeded mock "([^"]*)" is present in partition "([^"]*)"$`, f.theSeededMockIsPresentInPartition)
}
