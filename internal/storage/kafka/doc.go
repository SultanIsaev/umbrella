// Package kafka provides producer/consumer wrappers (github.com/segmentio/
// kafka-go — pure Go, no cgo, keeps CGO_ENABLED=0 builds working) used
// either as an ingestion source (consuming pre-collected telemetry) or as
// an intermediate durable queue between parsing and
// internal/storage/clickhouse. Producer implements storage.Storage;
// Consumer is a consumer-group reader with manual commit (at-least-once).
package kafka
