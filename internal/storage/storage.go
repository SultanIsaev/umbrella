// Package storage defines the sink side of the pipeline: the contract that
// internal/pipeline writes normalized events into, independent of whichever
// backend (internal/storage/clickhouse, internal/storage/kafka, or a fake
// for tests) implements it.
package storage

import "context"

// Event is a normalized telemetry record produced by a parser
// (internal/netflow, internal/ipfix, internal/syslog, internal/cef) and,
// optionally, internal/enrich.
type Event struct {
	Timestamp int64
	Source    string
	Fields    map[string]any
}

// Storage persists batches of Events. Implementations must be safe for
// concurrent use by multiple pipeline workers.
type Storage interface {
	Write(ctx context.Context, events []Event) error
	Close() error
}
