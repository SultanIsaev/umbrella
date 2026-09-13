package stub

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SultanIsaev/umbrella/internal/storage"
)

type LogStorage struct {
	log *slog.Logger
}

var _ storage.Storage = (*LogStorage)(nil)

func NewLogStorage(log *slog.Logger) *LogStorage {
	return &LogStorage{
		log: log,
	}
}

func (s *LogStorage) Write(ctx context.Context, events []storage.Event) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("log storage: write: %w", err)
	}

	if len(events) == 0 {
		return nil
	}

	s.log.Info("storage write",
		"count", len(events),
		"sample_source", events[0].Source,
		"sample_timestamp", events[0].Timestamp,
	)
	return nil
}

// Close — no-op: LogStorage не держит ресурсов, закрывать нечего.
func (s *LogStorage) Close() error {
	return nil
}
