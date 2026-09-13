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
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/SultanIsaev/umbrella/internal/config"
	"github.com/SultanIsaev/umbrella/internal/ingest"
	"github.com/SultanIsaev/umbrella/internal/observability"
	"github.com/SultanIsaev/umbrella/internal/pipeline"
	"github.com/SultanIsaev/umbrella/internal/server"
	"github.com/SultanIsaev/umbrella/internal/storage"
	"github.com/SultanIsaev/umbrella/internal/storage/clickhouse"
	"github.com/SultanIsaev/umbrella/internal/storage/stub"
	"github.com/SultanIsaev/umbrella/internal/version"
	"golang.org/x/sync/errgroup"
)

// clickhouseConnectTimeout bounds the initial connection+Ping in
// newStorage — startup should fail fast if ClickHouse is unreachable,
// not hang indefinitely before the process even starts serving.
const clickhouseConnectTimeout = 5 * time.Second

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
	out := make(chan []byte, 1024) // TODO: емкость пересчитать под реальный pipeline

	results := make(chan pipeline.Result, 1000)
	pool, err := pipeline.New(runtime.GOMAXPROCS(0))
	if err != nil {
		return fmt.Errorf("create pipeline error: %w", err)
	}

	store, chStore, err := newStorage(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}

	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		var written, writeErrors uint64
		for result := range results {
			if writeErr := store.Write(ctx, result.Events()); writeErr != nil {
				log.Error("storage write failed", "err", writeErr)
				writeErrors++
				continue
			}
			written++
		}
		log.Info("ingest consumer stopped",
			"packets_processed", written,
			"packets_invalid", pool.Invalid(),
			"write_errors", writeErrors,
		)
	}()

	listener, err := ingest.NewListener(cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("ingest listener: %w", err)
	}

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		defer close(out)
		return listener.Run(gCtx, out)
	})

	g.Go(func() error {
		pool.Run(out, results)
		return nil
	})

	if chStore != nil {
		g.Go(func() error {
			return chStore.Run(gCtx)
		})
	}

	g.Go(func() error {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		<-gCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("http server shutdown: %w", err)
		}
		return nil
	})

	runErr := g.Wait()
	<-consumerDone

	if err := store.Close(); err != nil {
		log.Error("storage close failed", "err", err)
		if runErr == nil {
			runErr = fmt.Errorf("storage close: %w", err)
		}
	}

	if runErr != nil {
		return runErr
	}

	log.Info("shutdown complete")
	return nil
}

// newStorage picks the storage.Storage backend: real ClickHouse when
// cfg.ClickHouseDSN is set, a log-only stub otherwise (local dev/loadgen
// runs — see Config.ClickHouseDSN doc-comment). The second return value is
// non-nil only for the ClickHouse backend, so the caller knows whether it
// must also run chStore.Run (see Batcher.Run doc — it owns a background
// goroutine, unlike stub.LogStorage which is purely synchronous).
func newStorage(ctx context.Context, cfg config.Config, log *slog.Logger) (storage.Storage, *clickhouse.Storage, error) {
	if cfg.ClickHouseDSN == "" {
		return stub.NewLogStorage(log), nil, nil
	}

	connectCtx, cancel := context.WithTimeout(ctx, clickhouseConnectTimeout)
	defer cancel()

	chStore, err := clickhouse.New(connectCtx, clickhouse.Config{
		DSN:           cfg.ClickHouseDSN,
		Table:         cfg.ClickHouseTable,
		BatchSize:     cfg.ClickHouseBatchSize,
		FlushInterval: cfg.ClickHouseFlushInterval,
	})
	if err != nil {
		return nil, nil, err
	}
	return chStore, chStore, nil
}
