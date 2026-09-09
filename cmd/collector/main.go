// Command collector runs the umbrella telemetry collector: it currently
// starts the operational HTTP surface (health check, pprof) with proper
// graceful shutdown; the ingestion -> parsing -> enrichment -> storage
// pipeline is wired in here as internal/ingest, internal/pipeline and
// internal/storage are implemented (see Roadmap.md, stage 3).
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/SultanIsaev/umbrella/internal/config"
	"github.com/SultanIsaev/umbrella/internal/observability"
	"github.com/SultanIsaev/umbrella/internal/server"
	"github.com/SultanIsaev/umbrella/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := observability.NewLogger(cfg.LogLevel)
	log.Info("starting umbrella collector",
		"version", version.String(),
		"listen_addr", cfg.ListenAddr,
		"http_addr", cfg.HTTPAddr,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(cfg.HTTPAddr, log)
	srvErrCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			srvErrCh <- fmt.Errorf("http server: %w", err)
			return
		}
		srvErrCh <- nil
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-srvErrCh:
		if err != nil {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http server shutdown: %w", err)
	}

	log.Info("shutdown complete")
	return nil
}
