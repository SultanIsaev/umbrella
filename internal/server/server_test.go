package server_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/SultanIsaev/umbrella/internal/server"
)

func TestHealthz(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(":0", log)

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	require.Equal(t, 200, rec.Code)

	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	require.Equal(t, "ok", string(body))
}
