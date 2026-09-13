package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/SultanIsaev/umbrella/internal/server"
)

func TestHealthz(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(":0", log, prometheus.NewRegistry())

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	require.Equal(t, 200, rec.Code)

	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	require.Equal(t, "ok", string(body))
}

func TestMetrics(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := prometheus.NewRegistry()
	counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_metric_total"})
	counter.Inc()
	reg.MustRegister(counter)

	srv := server.New(":0", log, reg)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/metrics", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	require.Equal(t, 200, rec.Code)

	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "test_metric_total 1")
}
