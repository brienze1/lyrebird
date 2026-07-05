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
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brienze1/lyrebird/internal/adapters/httpadmin"
	"github.com/brienze1/lyrebird/internal/adapters/httpmw"
	"github.com/brienze1/lyrebird/internal/adapters/matcher"
	"github.com/brienze1/lyrebird/internal/adapters/mcp"
	"github.com/brienze1/lyrebird/internal/adapters/proxy"
	"github.com/brienze1/lyrebird/internal/adapters/scripting"
	"github.com/brienze1/lyrebird/internal/adapters/template"
	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/infra/clock"
	"github.com/brienze1/lyrebird/internal/infra/config"
	"github.com/brienze1/lyrebird/internal/infra/crypto"
	"github.com/brienze1/lyrebird/internal/infra/gc"
	"github.com/brienze1/lyrebird/internal/infra/idgen"
	"github.com/brienze1/lyrebird/internal/infra/seeds"
	"github.com/brienze1/lyrebird/internal/infra/store"
	"github.com/brienze1/lyrebird/internal/usecase"
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

	// cancelServerCtx cancels the context threaded into proxy.NewHandler
	// (which a FaultTimeout hang is bound to — see fault.go's serveFault
	// doc comment). Owned and canceled here, not left to whatever context
	// the caller of Run happened to pass in, so Shutdown alone is always
	// sufficient to release an in-flight timeout-fault connection —
	// otherwise that guarantee would silently depend on caller discipline
	// invisible at this call site (e.g. cmd/lyrebird/main.go's run() only
	// works today because it happens to reuse one ctx variable for both
	// bootstrap.Run and its own shutdown trigger).
	cancelServerCtx context.CancelFunc
}

// core is every long-lived resource and use-case shared by both HTTP (Run)
// and stdio (RunStdio) modes — built exactly once so tool registration and
// use-case wiring can never drift between the two MCP transports
// (constitution Principle II).
type core struct {
	sealer crypto.Sealer
	store  *store.Store
	seeds  seeds.Seeds
	gcLoop *gc.Loop

	setUpstreamUC    *usecase.SetUpstream
	listUpstreamsUC  *usecase.ListUpstreams
	recordTrafficUC  *usecase.RecordTraffic
	listTrafficUC    *usecase.ListTraffic
	getTrafficUC     *usecase.GetTraffic
	clearTrafficUC   *usecase.ClearTraffic
	matchRequestUC   *usecase.MatchRequest
	matchTestUC      *usecase.MatchTest
	mockCRUDUC       *usecase.MockCRUD
	resetUC          *usecase.Reset
	metricsUC        *usecase.Metrics
	promoteTrafficUC *usecase.PromoteTraffic
	createSpaceUC    *usecase.CreateSpace
	listSpacesUC     *usecase.ListSpaces
	deleteSpaceUC    *usecase.DeleteSpace
	templater        usecase.Templater
	scriptEngine     *scripting.Engine

	mcpServer *sdkmcp.Server
}

// buildCore resolves the at-rest key, opens the store, loads seeds, starts
// the GC loop, and constructs every use-case plus the one MCP server both
// Run and RunStdio need. It never fails on a missing, empty, wrong-key, or
// corrupt database file (FR-029) — only a genuine infrastructure failure
// (e.g. disk permissions) returns an error here.
func buildCore(ctx context.Context, cfg config.Config, log *slog.Logger) (*core, error) {
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

	scriptEngine := scripting.New(cfg.ScriptTimeout)

	sd, err := seeds.Load(cfg.SeedDir, scriptEngine)
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("bootstrap: seeds: %w", err)
	}
	log.Info("seeds loaded",
		"partitions", len(sd.Partitions), "mocks", len(sd.Mocks), "upstreams", len(sd.Upstreams))

	gcLoop := gc.New(cfg.GCInterval, cfg.TrafficTTL, st, clock.System{}, log)
	gcLoop.Start(ctx)

	setUpstreamUC := usecase.NewSetUpstream(st)
	listUpstreamsUC := usecase.NewListUpstreams(st)
	recordTrafficUC := usecase.NewRecordTraffic(st, clock.System{}, idgen.UUID{})
	listTrafficUC := usecase.NewListTraffic(st)
	getTrafficUC := usecase.NewGetTraffic(st)
	clearTrafficUC := usecase.NewClearTraffic(st)

	matchEval := matcher.New()
	templater := template.New()
	matchRequestUC := usecase.NewMatchRequest(st, sd, matchEval, scriptEngine, st)
	matchTestUC := usecase.NewMatchTest(st, sd, matchEval, templater, st)
	mockCRUDUC := usecase.NewMockCRUD(st, sd, matchEval, scriptEngine, idgen.UUID{}, clock.System{}, st)
	resetUC := usecase.NewReset(st, st, st)
	metricsUC := usecase.NewMetrics(st, clock.System{})
	promoteTrafficUC := usecase.NewPromoteTraffic(st, mockCRUDUC)
	createSpaceUC := usecase.NewCreateSpace(st, clock.System{})
	listSpacesUC := usecase.NewListSpaces(st)
	deleteSpaceUC := usecase.NewDeleteSpace(st)

	// The default space is always implicitly active (every request/mock/
	// upstream falls back to it) and can never be deleted, so it's
	// registered here rather than left invisible to list_spaces until
	// something explicitly calls create_space for it.
	if _, err := createSpaceUC.Execute(ctx, domain.Partition{ID: domain.DefaultPartitionID}); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("bootstrap: register default space: %w", err)
	}

	mcpServer := mcp.New(mcp.Deps{
		DefaultSpace:   cfg.DefaultSpace,
		MockCRUD:       mockCRUDUC,
		Reset:          resetUC,
		MatchTest:      matchTestUC,
		SetUpstream:    setUpstreamUC,
		ListUpstreams:  listUpstreamsUC,
		ListTraffic:    listTrafficUC,
		GetTraffic:     getTrafficUC,
		ClearTraffic:   clearTrafficUC,
		Metrics:        metricsUC,
		PromoteTraffic: promoteTrafficUC,
		CreateSpace:    createSpaceUC,
		ListSpaces:     listSpacesUC,
		DeleteSpace:    deleteSpaceUC,
	})

	return &core{
		sealer: sealer, store: st, seeds: sd, gcLoop: gcLoop,
		setUpstreamUC: setUpstreamUC, listUpstreamsUC: listUpstreamsUC,
		recordTrafficUC: recordTrafficUC, listTrafficUC: listTrafficUC, getTrafficUC: getTrafficUC,
		clearTrafficUC: clearTrafficUC, matchRequestUC: matchRequestUC, matchTestUC: matchTestUC,
		mockCRUDUC: mockCRUDUC, resetUC: resetUC, metricsUC: metricsUC, promoteTrafficUC: promoteTrafficUC,
		createSpaceUC: createSpaceUC, listSpacesUC: listSpacesUC, deleteSpaceUC: deleteSpaceUC,
		templater: templater, scriptEngine: scriptEngine, mcpServer: mcpServer,
	}, nil
}

// Run builds the core, then starts the data-plane and control-plane HTTP
// listeners (Admin REST + the MCP Streamable HTTP handler, both on the
// control plane). The data plane is intentionally never authenticated
// (FR-030).
func Run(ctx context.Context, cfg config.Config, log *slog.Logger) (*App, error) {
	c, err := buildCore(ctx, cfg, log)
	if err != nil {
		return nil, err
	}

	readiness := &httpadmin.Readiness{}
	partitionMW := httpmw.Partition(cfg.DefaultSpace)

	controlMux := http.NewServeMux()
	controlMux.HandleFunc("GET /__lyrebird/healthz", httpadmin.Healthz)
	controlMux.HandleFunc("GET /__lyrebird/readyz", httpadmin.Readyz(readiness))
	controlMux.HandleFunc("GET /__lyrebird/upstreams", httpadmin.ListUpstreams(c.listUpstreamsUC))
	controlMux.HandleFunc("POST /__lyrebird/upstreams", httpadmin.SetUpstream(c.setUpstreamUC))
	controlMux.HandleFunc("GET /__lyrebird/traffic", httpadmin.ListTraffic(c.listTrafficUC))
	controlMux.HandleFunc("GET /__lyrebird/traffic/{id}", httpadmin.GetTraffic(c.getTrafficUC))
	controlMux.HandleFunc("POST /__lyrebird/traffic/{id}/promote", httpadmin.PromoteTraffic(c.promoteTrafficUC))
	controlMux.HandleFunc("GET /__lyrebird/mocks", httpadmin.ListMocks(c.mockCRUDUC))
	controlMux.HandleFunc("POST /__lyrebird/mocks", httpadmin.CreateMock(c.mockCRUDUC))
	controlMux.HandleFunc("GET /__lyrebird/mocks/{id}", httpadmin.GetMock(c.mockCRUDUC))
	controlMux.HandleFunc("PUT /__lyrebird/mocks/{id}", httpadmin.UpdateMock(c.mockCRUDUC))
	controlMux.HandleFunc("DELETE /__lyrebird/mocks/{id}", httpadmin.DeleteMock(c.mockCRUDUC))
	controlMux.HandleFunc("POST /__lyrebird/match-test", httpadmin.MatchTest(c.matchTestUC))
	controlMux.HandleFunc("POST /__lyrebird/reset", httpadmin.Reset(c.resetUC))
	controlMux.HandleFunc("GET /__lyrebird/spaces", httpadmin.ListSpaces(c.listSpacesUC))
	controlMux.HandleFunc("POST /__lyrebird/spaces", httpadmin.CreateSpace(c.createSpaceUC))
	controlMux.HandleFunc("DELETE /__lyrebird/spaces/{id}", httpadmin.DeleteSpace(c.deleteSpaceUC))
	controlMux.Handle("/mcp", mcp.Handler(c.mcpServer))

	// serverCtx (not the raw ctx Run was called with) is what a FaultTimeout
	// hang binds to — owned and canceled by App.Shutdown itself, so
	// Shutdown alone is always sufficient to release an in-flight
	// timeout-fault connection, regardless of whether/when the caller's own
	// ctx gets canceled.
	serverCtx, cancelServerCtx := context.WithCancel(ctx)

	proxyEngine := proxy.NewEngine(cfg.UpstreamTimeout, c.scriptEngine, log)
	dataHandler := proxy.NewHandler(
		serverCtx,
		c.listUpstreamsUC, c.recordTrafficUC, c.matchRequestUC, c.templater, c.scriptEngine, c.store,
		proxyEngine, cfg.BodyCapBytes, clock.System{}, log, cfg.AllowProxyHosts,
	)
	dataMux := http.NewServeMux()
	dataMux.Handle("/", dataHandler)

	var lc net.ListenConfig
	dataLn, err := lc.Listen(ctx, "tcp", cfg.DataPlaneAddr)
	if err != nil {
		cancelServerCtx()
		c.gcLoop.Stop()
		_ = c.store.Close()
		return nil, fmt.Errorf("bootstrap: listen data plane: %w", err)
	}
	controlLn, err := lc.Listen(ctx, "tcp", cfg.ControlPlaneAddr)
	if err != nil {
		cancelServerCtx()
		_ = dataLn.Close()
		c.gcLoop.Stop()
		_ = c.store.Close()
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
		Sealer:          c.sealer,
		Store:           c.store,
		Seeds:           c.seeds,
		GC:              c.gcLoop,
		Readiness:       readiness,
		dataListener:    dataLn,
		controlListener: controlLn,
		dataServer:      dataSrv,
		controlServer:   controlSrv,
		cancelServerCtx: cancelServerCtx,
	}, nil
}

// RunStdio builds the core and serves MCP over stdin/stdout only — no HTTP
// listeners are bound. Mutually exclusive with Run in the same process
// invocation (contracts/mcp-tools.md's "stdio (local)" transport mode);
// cmd/lyrebird/main.go picks one or the other based on cfg.MCPStdio. Blocks
// until ctx is done or the stdio transport closes.
func RunStdio(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	c, err := buildCore(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer func() {
		c.gcLoop.Stop()
		_ = c.store.Close()
	}()
	return mcp.RunStdio(ctx, c.mcpServer)
}

// ControlAddr returns the actual address the control-plane listener is bound
// to (useful in tests that bind to ":0" for an ephemeral port).
func (a *App) ControlAddr() string { return a.controlListener.Addr().String() }

// DataAddr returns the actual address the data-plane listener is bound to.
func (a *App) DataAddr() string { return a.dataListener.Addr().String() }

// Shutdown stops the GC loop, then both HTTP servers, and closes the store.
// Canceling cancelServerCtx first releases any in-flight FaultTimeout
// hijacked connection (see serveFault's doc comment) — Shutdown alone is
// sufficient for that, independent of whatever context Run's original
// caller happens to manage on its own.
//
// The two servers' Shutdown calls are launched concurrently (each a
// goroutine, not sequential arguments) so they genuinely share the 10s
// drain budget in parallel rather than one stealing it from the other:
// Go evaluates function-call arguments left-to-right before invoking the
// function, so passing them directly as errors.Join(a.controlServer.
// Shutdown(shCtx), a.dataServer.Shutdown(shCtx), ...) would run
// controlServer's shutdown to completion (or until shCtx expires) BEFORE
// dataServer's shutdown even starts — leaving it with whatever sliver of
// shCtx's shared deadline remains. A slow control-plane drain could then
// starve the data-plane shutdown, which would return early (context
// deadline exceeded) without its in-flight connections actually having
// finished draining — and Store.Close() below would run regardless,
// closing the store out from under a still-in-flight data-plane handler.
// Waiting for both goroutines (wg.Wait()) before closing the store ensures
// the store is only closed once both shutdown attempts have genuinely
// completed.
func (a *App) Shutdown(ctx context.Context) error {
	if a.cancelServerCtx != nil {
		a.cancelServerCtx()
	}
	a.GC.Stop()
	shCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var controlErr, dataErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		controlErr = a.controlServer.Shutdown(shCtx)
	}()
	go func() {
		defer wg.Done()
		dataErr = a.dataServer.Shutdown(shCtx)
	}()
	wg.Wait()

	return errors.Join(controlErr, dataErr, a.Store.Close())
}
