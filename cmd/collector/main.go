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
	"github.com/SultanIsaev/umbrella/internal/storage/kafka"
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

	metrics := observability.NewMetrics()

	out := make(chan ingest.Packet, 1024) // TODO: емкость пересчитать под реальный pipeline

	results := make(chan pipeline.Result, 1000)
	pool, err := pipeline.New(runtime.GOMAXPROCS(0))
	if err != nil {
		return fmt.Errorf("create pipeline error: %w", err)
	}

	store, chStore, err := newStorage(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}

	listener, err := ingest.NewListener(cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("ingest listener: %w", err)
	}

	registerMetrics(metrics, listener, pool, out, results)
	srv := server.New(cfg.HTTPAddr, log, metrics.Registry)

	chStoreCtx, chStoreCancel := context.WithCancel(context.Background())
	defer chStoreCancel()

	var chStoreErrCh chan error
	if chStore != nil {
		chStoreErrCh = make(chan error, 1)
		go func() {
			chStoreErrCh <- chStore.Run(chStoreCtx)
		}()
	}

	consumerDone := runConsumer(ctx, results, store, pool, log, metrics)

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		defer close(out)
		return listener.Run(gCtx, out)
	})

	g.Go(func() error {
		pool.Run(out, results)
		return nil
	})

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
	<-consumerDone // после этой точки никто больше не позовёт store.Write

	if err := closeStorage(store, chStore, chStoreCancel, chStoreErrCh, cfg.ShutdownTimeout, log); err != nil {
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

// runConsumer вычитывает results и пишет каждый Result в store, пока
// results не закроет оркестратор (см. pipeline.Pool.Run doc — pool
// закрывает его сам, дождавшись остановки всех своих воркеров). Возвращает
// канал, который закрывается, когда цикл завершился — это и есть точка
// "больше никто не позовёт store.Write", после которой можно безопасно
// останавливать store (см. closeStorage).
//
// metrics.StorageWriteDuration/StorageEventsWritten/StorageWriteErrors
// инструментируют именно этот вызов напрямую (.Observe/.Add), а не через
// RegisterCounterFunc/RegisterGaugeFunc — потому что "успешных записей" и
// "длительности записи" нет ни в каком уже существующем atomic-счётчике,
// который можно было бы обернуть.
func runConsumer(ctx context.Context, results <-chan pipeline.Result, store storage.Storage, pool *pipeline.Pool, log *slog.Logger, metrics *observability.Metrics) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		var written, writeErrors uint64
		for result := range results {
			events := result.Events()

			start := time.Now()
			writeErr := store.Write(ctx, events)
			metrics.StorageWriteDuration.Observe(time.Since(start).Seconds())

			if writeErr != nil {
				log.Error("storage write failed", "err", writeErr)
				writeErrors++
				metrics.StorageWriteErrors.Inc()
				continue
			}
			written++
			metrics.StorageEventsWritten.Add(float64(len(events)))
		}
		log.Info("ingest consumer stopped",
			"packets_processed", written,
			"packets_invalid", pool.Invalid(),
			"write_errors", writeErrors,
		)
	}()
	return done
}

// registerMetrics оборачивает уже существующие atomic-счётчики
// ingest.Listener/pipeline.Pool и текущую длину каналов out/results как
// Prometheus counter/gauge-функции (см. observability.Metrics doc-
// комментарий) — сами эти пакеты остаются без зависимости от Prometheus.
func registerMetrics(metrics *observability.Metrics, listener *ingest.Listener, pool *pipeline.Pool, out chan ingest.Packet, results chan pipeline.Result) {
	metrics.RegisterCounterFunc("ingest", "packets_received_total",
		"Total number of UDP packets received.",
		func() float64 { return float64(listener.Received()) })
	metrics.RegisterCounterFunc("ingest", "packets_dropped_total",
		"Total number of UDP packets dropped due to a full pipeline queue.",
		func() float64 { return float64(listener.Dropped()) })
	metrics.RegisterCounterFunc("ingest", "packets_truncated_total",
		"Total number of UDP packets truncated by an undersized read buffer.",
		func() float64 { return float64(listener.Truncated()) })
	metrics.RegisterCounterFunc("pipeline", "packets_invalid_total",
		"Total number of packets that failed to parse in the worker pool.",
		func() float64 { return float64(pool.Invalid()) })

	metrics.RegisterGaugeFunc("ingest", "out_queue_length",
		"Current number of raw packets buffered between the UDP listener and the worker pool.",
		func() float64 { return float64(len(out)) })
	metrics.RegisterGaugeFunc("pipeline", "results_queue_length",
		"Current number of parsed results buffered between the worker pool and the storage consumer.",
		func() float64 { return float64(len(results)) })
}

// closeStorage останавливает store. Для ClickHouse (chStore != nil) это
// значит: store.Close() сам вызывает Batcher.CloseInput (мягкая остановка —
// досылает последний неполный батч) и ждёт фактического возврата
// chStore.Run, прежде чем закрыть соединение — см. Storage.Close doc.
// Вызов обёрнут в горутину и ограничен shutdownTimeout: если сам flush
// застрял дольше отведённого времени, chStoreCancel жёстко обрывает его,
// чтобы процесс не завис навсегда. Ошибки из Close() и из самого Run
// (через chStoreErrCh) объединяются через errors.Join, а не теряется одна
// в пользу другой.
func closeStorage(store storage.Storage, chStore *clickhouse.Storage, chStoreCancel context.CancelFunc, chStoreErrCh chan error, shutdownTimeout time.Duration, log *slog.Logger) error {
	if chStore == nil {
		return store.Close()
	}

	closeErrCh := make(chan error, 1)
	go func() { closeErrCh <- store.Close() }()

	var closeErr error
	select {
	case closeErr = <-closeErrCh:
	case <-time.After(shutdownTimeout):
		log.Warn("clickhouse batcher did not drain gracefully in time, forcing stop")
		chStoreCancel()
		closeErr = <-closeErrCh
	}

	return errors.Join(closeErr, <-chStoreErrCh)
}

// newStorage выбирает backend storage.Storage в порядке приоритета:
//
//  1. ClickHouse, если задан cfg.ClickHouseDSN — финальный sink.
//  2. Kafka, если задан cfg.KafkaBrokers (и ClickHouseDSN не задан) — роль
//     "durable очереди" из doc-комментария internal/storage/kafka: этот
//     collector только пишет в топик, какой-то отдельный consumer (не
//     запускается здесь) вычитывает его в ClickHouse или куда-то ещё.
//  3. Лог-заглушка в остальных случаях (локальная разработка/loadgen-
//     прогоны — см. doc-комментарий Config.ClickHouseDSN).
//
// Второе возвращаемое значение непустое только для ClickHouse — по нему
// вызывающий код понимает, что нужно ещё запустить chStore.Run на
// отдельном контексте (никогда не gCtx — см. closeStorage) и дождаться его
// через chStoreErrCh. kafka.Producer и stub.LogStorage, в отличие от него,
// полностью синхронны — достаточно одного Close: kafka.Writer.Close уже
// сам дожидается отправки накопленных сообщений, отдельный цикл батчинга
// им не нужен.
func newStorage(ctx context.Context, cfg config.Config, log *slog.Logger) (storage.Storage, *clickhouse.Storage, error) {
	switch {
	case cfg.ClickHouseDSN != "":
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

	case len(cfg.KafkaBrokers) > 0:
		producer, err := kafka.NewProducer(kafka.ProducerConfig{
			Brokers: cfg.KafkaBrokers,
			Topic:   cfg.KafkaTopic,
		})
		if err != nil {
			return nil, nil, err
		}
		return producer, nil, nil

	default:
		return stub.NewLogStorage(log), nil, nil
	}
}
