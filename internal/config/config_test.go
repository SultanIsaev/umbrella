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
	require.Nil(t, cfg.KafkaBrokers)
	require.Equal(t, 10*time.Second, cfg.ShutdownTimeout)
}

func TestLoad_Overrides(t *testing.T) {
	t.Setenv("UMBRELLA_LISTEN_ADDR", "0.0.0.0:9995")
	t.Setenv("UMBRELLA_HTTP_ADDR", ":9090")
	t.Setenv("UMBRELLA_LOG_LEVEL", "debug")
	t.Setenv("UMBRELLA_CLICKHOUSE_DSN", "clickhouse://localhost:9000/default")
	t.Setenv("UMBRELLA_KAFKA_BROKERS", "broker-1:9092, broker-2:9092")
	t.Setenv("UMBRELLA_SHUTDOWN_TIMEOUT", "30s")

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, "0.0.0.0:9995", cfg.ListenAddr)
	require.Equal(t, ":9090", cfg.HTTPAddr)
	require.Equal(t, "debug", cfg.LogLevel)
	require.Equal(t, "clickhouse://localhost:9000/default", cfg.ClickHouseDSN)
	require.Equal(t, []string{"broker-1:9092", "broker-2:9092"}, cfg.KafkaBrokers)
	require.Equal(t, 30*time.Second, cfg.ShutdownTimeout)
}

func TestLoad_InvalidShutdownTimeout(t *testing.T) {
	t.Setenv("UMBRELLA_SHUTDOWN_TIMEOUT", "not-a-duration")

	_, err := config.Load()
	require.Error(t, err)
}
