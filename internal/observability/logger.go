// Package observability wires structured logging (and, as the collector
// grows, metrics/tracing setup) so every other package depends on
// *slog.Logger rather than the global log package or ad-hoc fmt.Println.
package observability

import (
	"log/slog"
	"os"
)

// NewLogger returns a JSON slog.Logger writing to stdout at the given level
// (debug|info|warn|error). An unrecognized level falls back to info rather
// than failing startup over a logging misconfiguration.
func NewLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(handler)
}
