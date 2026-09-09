# Architecture

## Status

Skeleton stage: the operational HTTP surface (health check, pprof) and
configuration/logging plumbing exist; the telemetry pipeline itself is being
built out per [Roadmap.md](../ROADMAP.md).

## Target pipeline

```
        UDP/TCP                 parse                enrich              storage
  ┌───────────────┐      ┌──────────────────┐    ┌────────────┐    ┌───────────────────┐
  │  internal/    │      │ internal/netflow │    │ internal/  │    │ internal/storage/ │
  │  ingest       │ ───▶ │ internal/ipfix   │ ─▶ │  enrich    │ ─▶ │  clickhouse       │
  │ (listener,    │      │ internal/syslog  │    │            │    │  (batch insert)   │
  │  SO_REUSEPORT,│      │ internal/cef     │    │            │    │                   │
  │  backpressure)│      └──────────────────┘    └────────────┘    └───────────────────┘
  └───────────────┘
```

`internal/pipeline` owns the worker pool that connects these stages and the
shutdown sequencing (drain in-flight events before `internal/storage` is
closed).

## Design decisions

Recorded as they're made, with the trade-off considered and why the chosen
option won:

- _(none yet — first decision lands with the ingest listener in stage 3)_

## Operational surface

- `GET /healthz` — liveness/readiness.
- `GET /debug/pprof/*` — CPU/heap/goroutine/block/mutex profiles, gated to
  `UMBRELLA_HTTP_ADDR` (bind this to a private interface in production).

## Performance targets

Tracked with real numbers once the ingest listener and a parser exist — see
[benchmarks.md](./benchmarks.md).
