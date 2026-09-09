// Package pipeline wires the collector's stages together:
// internal/ingest -> {internal/netflow, internal/ipfix, internal/syslog,
// internal/cef} -> internal/enrich -> internal/storage. It owns the worker
// pool topology (fan-in/fan-out, buffer sizes, worker counts) and the
// shutdown sequencing that drains in-flight events before internal/storage
// is closed.
//
// Not yet implemented — tracked in Roadmap.md, stage 1/3.
package pipeline
