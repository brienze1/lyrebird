// Command lyrebird runs the Lyrebird mock and spy-proxy server.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/brienze1/lyrebird/internal/bootstrap"
	"github.com/brienze1/lyrebird/internal/infra/config"
	"github.com/brienze1/lyrebird/internal/infra/logging"
)

func main() {
	os.Exit(run())
}

// run contains all of main's logic and returns a process exit code. Kept
// separate from main so no os.Exit call can ever skip a pending defer
// (gocritic: exitAfterDefer) — main itself never defers anything.
func run() int {
	log := logging.New()

	cfg, err := config.Load()
	if err != nil {
		log.Error("config load failed", "err", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.MCPStdio {
		if os.Getenv("LYREBIRD_DATA_PORT") != "" || os.Getenv("LYREBIRD_CONTROL_PORT") != "" ||
			os.Getenv("LYREBIRD_GRPC_PORT") != "" {
			log.Warn("LYREBIRD_DATA_PORT/LYREBIRD_CONTROL_PORT/LYREBIRD_GRPC_PORT are ignored in stdio mode " +
				"(LYREBIRD_MCP_STDIO=true) — no network listeners are started")
		}
		if err := bootstrap.RunStdio(ctx, cfg, log); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				log.Info("mcp stdio shut down", "err", err)
				return 0
			}
			log.Error("mcp stdio run failed", "err", err)
			return 1
		}
		return 0
	}

	app, err := bootstrap.Run(ctx, cfg, log)
	if err != nil {
		log.Error("bootstrap failed", "err", err)
		return 1
	}

	<-ctx.Done()
	log.Info("shutting down")

	if err := app.Shutdown(context.Background()); err != nil {
		log.Error("shutdown error", "err", err)
		return 1
	}
	return 0
}
