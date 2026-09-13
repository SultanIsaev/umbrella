// Package clickhouse implements storage.Storage against ClickHouse,
// batching events (size- or time-bounded, whichever triggers first) instead
// of issuing a row-per-event INSERT. The batching logic (Batcher) is
// deliberately independent of the ClickHouse client — see batcher.go —
// so it's testable without a running ClickHouse instance; storage.go is
// the thin, ClickHouse-specific wiring on top.
package clickhouse
