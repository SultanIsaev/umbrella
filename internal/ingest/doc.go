// Package ingest owns the listeners that receive raw telemetry: socket
// buffer sizing (SO_RCVBUF) and the backpressure policy applied when
// downstream parsing can't keep up (drop-with-metric, never block the
// reader). Each received Packet carries the sender's address (Packet.From),
// which pipeline.Pool needs as the exporter key for NetFlow v9/IPFIX
// template caches.
//
// UDP (Listener) is implemented. TCP, SO_REUSEPORT for multi-listener
// scaling and batch reads are not yet — tracked in Roadmap.md, stage 3.
package ingest
