// Package ingest owns the UDP/TCP listeners that receive raw telemetry:
// socket buffer sizing (SO_RCVBUF), SO_REUSEPORT for multi-listener
// scaling, batch reads, and the backpressure policy applied when downstream
// parsing can't keep up (drop-with-metric, never block the reader).
//
// Not yet implemented — tracked in Roadmap.md, stage 3.
package ingest
