// Package clickhouse implements storage.Storage against ClickHouse,
// batching events (size- or time-bounded, whichever triggers first) instead
// of issuing a row-per-event INSERT.
//
// Not yet implemented — tracked in Roadmap.md, stage 4.
package clickhouse
