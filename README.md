# umbrella

A high-throughput SIEM telemetry collector in Go: NetFlow v5/v9, IPFIX,
Syslog (RFC 5424) and CEF ingestion, normalization, and storage — built for
predictable p95/p99 latency and high PPS/EPS without packet loss.

Status: **early skeleton** — see [Roadmap.md](ROADMAP.md) for what's built
and what's next, and [docs/architecture.md](./docs/architecture.md) for the
target pipeline.

## Quickstart

```sh
go build ./...
go test -race -count=1 ./...

go run ./cmd/collector          # starts on :2055 (UDP) / :8080 (HTTP)
curl localhost:8080/healthz
```

Configuration is environment-based — see
[configs/config.example.env](./configs/config.example.env) for every
variable and its default.

## Load testing

```sh
go run ./cmd/loadgen -target 127.0.0.1:2055 -pps 50000 -duration 30s
```

## Layout

```
cmd/collector/     the service entrypoint
cmd/loadgen/        UDP load generator for manual load testing / benchmarks
internal/netflow/   NetFlow v5/v9 parser
internal/ipfix/      IPFIX parser
internal/syslog/     RFC 5424 syslog parser
internal/cef/        CEF parser
internal/ingest/     UDP/TCP listeners, backpressure
internal/pipeline/   worker pool wiring ingest → parse → enrich → storage
internal/enrich/     event enrichment
internal/storage/    storage.Storage interface + clickhouse/kafka backends
internal/server/     health check + pprof HTTP surface
internal/config/     environment-based configuration
internal/observability/  structured logging
deploy/              Dockerfile, systemd unit
docs/                architecture notes, benchmark log
test/testdata/       fixtures for unit tests and fuzzing
```

## Development

```sh
make fmt vet lint test race build
```

CI (`.github/workflows/ci.yml`) runs `go vet`, `golangci-lint`, and
`go test -race` on every push and pull request.

## License

[MIT](./LICENSE)
