// Package config loads collector configuration from the environment, so
// deployment (systemd EnvironmentFile, Docker env, k8s ConfigMap) never
// requires touching a hardcoded value in code.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds every runtime setting of the collector.
type Config struct {
	// ListenAddr is the UDP address the ingestion listener binds to.
	ListenAddr string
	// HTTPAddr serves /healthz and /debug/pprof.
	HTTPAddr string
	// LogLevel is one of debug|info|warn|error.
	LogLevel string
	// ClickHouseDSN is the connection string for the storage backend.
	// Empty disables ClickHouse writes (useful for local dev / loadgen runs) —
	// the collector falls back to a log-only storage.Storage in that case.
	ClickHouseDSN string
	// ClickHouseTable is the batch-insert target (see deploy/docker/clickhouse/init.sql
	// for the expected schema).
	ClickHouseTable string
	// ClickHouseBatchSize/ClickHouseFlushInterval bound the size-or-timeout
	// batching in internal/storage/clickhouse.
	ClickHouseBatchSize     int
	ClickHouseFlushInterval time.Duration
	// KafkaBrokers is a comma-separated list of broker addresses.
	KafkaBrokers []string
	// ShutdownTimeout bounds how long graceful shutdown waits for in-flight
	// work before the process exits anyway.
	ShutdownTimeout time.Duration
}

const (
	envListenAddr              = "UMBRELLA_LISTEN_ADDR"
	envHTTPAddr                = "UMBRELLA_HTTP_ADDR"
	envLogLevel                = "UMBRELLA_LOG_LEVEL"
	envClickHouseDSN           = "UMBRELLA_CLICKHOUSE_DSN"
	envClickHouseTable         = "UMBRELLA_CLICKHOUSE_TABLE"
	envClickHouseBatchSize     = "UMBRELLA_CLICKHOUSE_BATCH_SIZE"
	envClickHouseFlushInterval = "UMBRELLA_CLICKHOUSE_FLUSH_INTERVAL"
	envKafkaBrokers            = "UMBRELLA_KAFKA_BROKERS"
	envShutdownTimeout         = "UMBRELLA_SHUTDOWN_TIMEOUT"
)

// Дефолты батчинга ClickHouse: 1000 строк — типичный порядок величины для
// batch INSERT (компромисс между накладными расходами на мелкие вставки и
// задержкой/памятью на слишком крупные), 5с — чтобы данные не зависали
// надолго при низком трафике, когда batchSize никогда не набирается.
const (
	defaultClickHouseBatchSize     = 1000
	defaultClickHouseFlushInterval = 5 * time.Second
)

// Load reads Config from the environment, applying documented defaults for
// every unset variable and rejecting values that fail to parse.
func Load() (Config, error) {
	cfg := Config{
		ListenAddr:              getEnv(envListenAddr, ":2055"),
		HTTPAddr:                getEnv(envHTTPAddr, ":8080"),
		LogLevel:                getEnv(envLogLevel, "info"),
		ClickHouseDSN:           getEnv(envClickHouseDSN, ""),
		ClickHouseTable:         getEnv(envClickHouseTable, "events"),
		ClickHouseBatchSize:     defaultClickHouseBatchSize,
		ClickHouseFlushInterval: defaultClickHouseFlushInterval,
		KafkaBrokers:            splitCSV(getEnv(envKafkaBrokers, "")),
		ShutdownTimeout:         10 * time.Second,
	}

	if raw := os.Getenv(envShutdownTimeout); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("parse %s=%q: %w", envShutdownTimeout, raw, err)
		}
		cfg.ShutdownTimeout = d
	}

	if raw := os.Getenv(envClickHouseBatchSize); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("parse %s=%q: must be a positive integer", envClickHouseBatchSize, raw)
		}
		cfg.ClickHouseBatchSize = n
	}

	if raw := os.Getenv(envClickHouseFlushInterval); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("parse %s=%q: %w", envClickHouseFlushInterval, raw, err)
		}
		cfg.ClickHouseFlushInterval = d
	}

	return cfg, nil
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func splitCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
