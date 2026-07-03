// Package bootstrap wires Lyrebird's dependencies and listeners into a
// running App. It is a standalone, testable Run/Shutdown pair — deliberately
// not inlined into cmd/lyrebird/main.go — so BDD scenarios can boot the
// server in-process against fabricated fixtures (e.g. a corrupted or
// wrong-key database file) and assert on the outcome (T066).
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/brienze1/lyrebird/internal/adapters/httpadmin"
	"github.com/brienze1/lyrebird/internal/adapters/httpmw"
	"github.com/brienze1/lyrebird/internal/infra/config"
	"github.com/brienze1/lyrebird/internal/infra/crypto"
	"github.com/brienze1/lyrebird/internal/infra/gc"
	"github.com/brienze1/lyrebird/internal/infra/seeds"
	"github.com/brienze1/lyrebird/internal/infra/store"
)

// App holds every long-lived resource a running Lyrebird instance owns.
type App struct {
	Config    config.Config
	Sealer    crypto.Sealer
	Store     *store.Store
	Seeds     seeds.Seeds
	GC        *gc.Loop
	Readiness *httpadmin.Readiness

	dataListener    net.Listener
	controlListener net.Listener
	dataServer      *http.Server
	controlServer   *http.Server
}

// Run resolves the at-rest key, opens the store, loads seeds, starts the GC
// loop, and starts both listeners. It never fails on a missing, empty,
// wrong-key, or corrupt database file (FR-029) — only a genuine
// infrastructure failure (e.g. disk permissions) returns an error here.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger) (*App, error) {
	key, source, err := crypto.ResolveKey(cfg.DataKeyB64)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: resolve key: %w", err)
	}
	log.Info("at-rest encryption key initialized", "source", string(source))

	sealer, err := crypto.New(key)
	for i := range key {
		key[i] = 0
	}
	if err != nil {
		return nil, fmt.Errorf("bootstrap: crypto: %w", err)
	}

	st, err := store.Open(ctx, cfg.DBPath, sealer, log)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: store: %w", err)
	}

	sd, err := seeds.Load(cfg.SeedDir)
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("bootstrap: seeds: %w", err)
	}
	log.Info("seeds loaded",
		"partitions", len(sd.Partitions), "mocks", len(sd.Mocks), "upstreams", len(sd.Upstreams))

	gcLoop := gc.New(cfg.GCInterval, cfg.TrafficTTL, st, log)
	gcLoop.Start(ctx)

	readiness := &httpadmin.Readiness{}
	partitionMW := httpmw.Partition(cfg.DefaultSpace)

	controlMux := http.NewServeMux()
	controlMux.HandleFunc("GET /__lyrebird/healthz", httpadmin.Healthz)
	controlMux.HandleFunc("GET /__lyrebird/readyz", httpadmin.Readyz(readiness))

	dataMux := http.NewServeMux()
	dataMux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		// Placeholder until the proxy/mock engine lands (M1, T021). The data
		// plane is intentionally never authenticated (FR-030).
		w.WriteHeader(http.StatusNotImplemented)
	})

	var lc net.ListenConfig
	dataLn, err := lc.Listen(ctx, "tcp", cfg.DataPlaneAddr)
	if err != nil {
		gcLoop.Stop()
		_ = st.Close()
		return nil, fmt.Errorf("bootstrap: listen data plane: %w", err)
	}
	controlLn, err := lc.Listen(ctx, "tcp", cfg.ControlPlaneAddr)
	if err != nil {
		_ = dataLn.Close()
		gcLoop.Stop()
		_ = st.Close()
		return nil, fmt.Errorf("bootstrap: listen control plane: %w", err)
	}

	dataSrv := &http.Server{Handler: partitionMW(dataMux)}
	controlSrv := &http.Server{Handler: partitionMW(controlMux)}

	go func() {
		if err := dataSrv.Serve(dataLn); err != nil && err != http.ErrServerClosed {
			log.Error("data-plane server error", "err", err)
		}
	}()
	go func() {
		if err := controlSrv.Serve(controlLn); err != nil && err != http.ErrServerClosed {
			log.Error("control-plane server error", "err", err)
		}
	}()

	// Every step that determines correctness (key, store, seeds) has
	// succeeded — flip readiness now.
	readiness.MarkReady()

	log.Info("lyrebird started",
		"data_addr", dataLn.Addr().String(), "control_addr", controlLn.Addr().String())

	return &App{
		Config:          cfg,
		Sealer:          sealer,
		Store:           st,
		Seeds:           sd,
		GC:              gcLoop,
		Readiness:       readiness,
		dataListener:    dataLn,
		controlListener: controlLn,
		dataServer:      dataSrv,
		controlServer:   controlSrv,
	}, nil
}

// ControlAddr returns the actual address the control-plane listener is bound
// to (useful in tests that bind to ":0" for an ephemeral port).
func (a *App) ControlAddr() string { return a.controlListener.Addr().String() }

// DataAddr returns the actual address the data-plane listener is bound to.
func (a *App) DataAddr() string { return a.dataListener.Addr().String() }

// Shutdown stops the GC loop, both HTTP servers, and closes the store.
func (a *App) Shutdown(ctx context.Context) error {
	a.GC.Stop()
	shCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return errors.Join(
		a.controlServer.Shutdown(shCtx),
		a.dataServer.Shutdown(shCtx),
		a.Store.Close(),
	)
}
