package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/SultanIsaev/umbrella/internal/config"
)

func TestLoad_Defaults(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, ":2055", cfg.ListenAddr)
	require.Equal(t, ":8080", cfg.HTTPAddr)
	require.Equal(t, "info", cfg.LogLevel)
	require.Empty(t, cfg.ClickHouseDSN)
	require.Equal(t, "events", cfg.ClickHouseTable)
	require.Equal(t, 1000, cfg.ClickHouseBatchSize)
	require.Equal(t, 5*time.Second, cfg.ClickHouseFlushInterval)
	require.Nil(t, cfg.KafkaBrokers)
	require.Equal(t, 10*time.Second, cfg.ShutdownTimeout)
}

func TestLoad_Overrides(t *testing.T) {
	t.Setenv("UMBRELLA_LISTEN_ADDR", "0.0.0.0:9995")
	t.Setenv("UMBRELLA_HTTP_ADDR", ":9090")
	t.Setenv("UMBRELLA_LOG_LEVEL", "debug")
	t.Setenv("UMBRELLA_CLICKHOUSE_DSN", "clickhouse://localhost:9000/default")
	t.Setenv("UMBRELLA_CLICKHOUSE_TABLE", "custom_events")
	t.Setenv("UMBRELLA_CLICKHOUSE_BATCH_SIZE", "500")
	t.Setenv("UMBRELLA_CLICKHOUSE_FLUSH_INTERVAL", "2s")
	t.Setenv("UMBRELLA_KAFKA_BROKERS", "broker-1:9092, broker-2:9092")
	t.Setenv("UMBRELLA_SHUTDOWN_TIMEOUT", "30s")

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, "0.0.0.0:9995", cfg.ListenAddr)
	require.Equal(t, ":9090", cfg.HTTPAddr)
	require.Equal(t, "debug", cfg.LogLevel)
	require.Equal(t, "clickhouse://localhost:9000/default", cfg.ClickHouseDSN)
	require.Equal(t, "custom_events", cfg.ClickHouseTable)
	require.Equal(t, 500, cfg.ClickHouseBatchSize)
	require.Equal(t, 2*time.Second, cfg.ClickHouseFlushInterval)
	require.Equal(t, []string{"broker-1:9092", "broker-2:9092"}, cfg.KafkaBrokers)
	require.Equal(t, 30*time.Second, cfg.ShutdownTimeout)
}

func TestLoad_InvalidShutdownTimeout(t *testing.T) {
	t.Setenv("UMBRELLA_SHUTDOWN_TIMEOUT", "not-a-duration")

	_, err := config.Load()
	require.Error(t, err)
}

func TestLoad_InvalidClickHouseBatchSize(t *testing.T) {
	tests := []struct {
		name string
		val  string
	}{
		{name: "не число", val: "abc"},
		{name: "ноль", val: "0"},
		{name: "отрицательное", val: "-5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("UMBRELLA_CLICKHOUSE_BATCH_SIZE", tt.val)
			_, err := config.Load()
			require.Error(t, err)
		})
	}
}

func TestLoad_InvalidClickHouseFlushInterval(t *testing.T) {
	t.Setenv("UMBRELLA_CLICKHOUSE_FLUSH_INTERVAL", "not-a-duration")

	_, err := config.Load()
	require.Error(t, err)
}
