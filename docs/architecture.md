# Архитектура

## Статус

M0–M11 сделаны: приём по UDP, worker pool, парсеры NetFlow v5/v9, IPFIX,
Syslog (RFC 5424) и CEF, батч-запись в ClickHouse, продюсер/консьюмер для
Kafka, performance-проход с `benchstat`-подтверждёнными оптимизациями,
наблюдаемость через Prometheus/Grafana и проверенная поставка (статический
бинарник, docker-образ, systemd-юнит). Полная таблица с Definition of Done
по каждому шагу — в [ROADMAP.md](../ROADMAP.md).

## Пайплайн

```
                UDP :2055                    fan-out / fan-in
        ┌────────────────────┐          ┌────────────────────────────┐
        │  internal/ingest   │          │  internal/pipeline         │
 пакеты▶│  (Listener,        │───out───▶│  (worker pool,             │
        │   SO_RCVBUF,       │  chan    │   GOMAXPROCS воркеров)     │
        │   drop+метрика на  │          │                            │
        │   backpressure)    │          │  internal/netflow v5/v9    │
        └────────────────────┘          │  internal/ipfix            │
                                        │  internal/syslog (RFC 5424)│
                                        │  internal/cef              │
                                        └────────────────────────────┘
                                                      │ storage.Event
                                                      ▼
                                         internal/storage.Storage
                                      (backend выбирается по приоритету
                                       в cmd/collector.newStorage)
                                ┌─────────────┬──────────────┬─────────────┐
                                ▼             ▼              ▼
                      internal/storage/  internal/storage/  internal/storage/
                      clickhouse         kafka               stub
                      (batch insert,     (producer,          (log/memory,
                       select+timer)      durable-очередь)    для dev/тестов)
```

`internal/pipeline.Pool` владеет worker pool'ом, который соединяет эти
стадии, и последовательностью остановки (дожидается воркеров, закрывает
`results` только после этого). `cmd/collector` — оркестратор: сигнальный
graceful shutdown (`os/signal.NotifyContext` + `context`, `errgroup`),
выбор storage-бэкенда, подключение метрик.

## Архитектурные решения

Зафиксированы по мере принятия, с рассмотренным trade-off и почему победил
именно этот вариант:

- **Выбор storage-бэкенда по приоритету, не по флагу** (`cmd/collector/main.go`,
  `newStorage`): ClickHouse (если задан `ClickHouseDSN`) → Kafka (если задан
  `KafkaBrokers`) → лог-заглушка. Только один бэкенд активен одновременно —
  так `storage.Storage` остаётся простым интерфейсом с одной реализацией "в
  бою", без необходимости писать fan-out на несколько хранилищ сразу,
  которого никто не просил.
- **ClickHouse-батчер отделён от клиента** (`internal/storage/clickhouse/batcher.go`):
  `Batcher` ничего не знает про `clickhouse-go/v2`, батчинг size-or-timeout
  происходит через инжектируемый `FlushFunc`. Это позволяет тестировать всю
  логику накопления/сброса батча (`batcher_test.go`) без поднятия реального
  ClickHouse.
- **`chStore.Run` — на собственном контексте, не на `gCtx`** (`cmd/collector/main.go`):
  первая версия завязывала цикл батчера на тот же контекст, что отменяется
  по SIGTERM, — из-за этого он всегда уходил в "жёсткую" остановку (без
  финального flush) раньше, чем срабатывал мягкий путь через
  `Batcher.CloseInput`. Живой тест (0/60 строк долетело до ClickHouse при
  остановке) вскрыл проблему; исправлено выдачей батчеру собственного
  `context.WithCancel(context.Background())` и явным вызовом `CloseInput`
  сразу после того, как `runConsumer` перестал писать.
- **Минимальные интерфейсы `messageFetcher`/`messageWriter` для Kafka**
  (`internal/storage/kafka/{consumer,producer}.go`): вместо мока всего
  `*kafka.Reader`/`*kafka.Writer` — только тот срез методов, что реально
  используется. Позволяет тестировать consumer/producer без поднятого
  брокера.
- **`storage.Fields` — упорядоченный `[]Field`, не `map[string]any`**
  (`internal/storage/storage.go`): профилирование под нагрузкой показало,
  что построение map было главным источником аллокаций на hot path.
  Подробности и цифры — [benchmarks.md](./benchmarks.md).
- **`TemplateCache` для NetFlow v9/IPFIX**
  (`internal/netflow/templatecache.go`, `internal/ipfix/templatecache.go`):
  data record, пришедший раньше своего темплейта, — не ошибка парсинга, а
  ожидаемый кейс у template-based протоколов (typical при перезапуске
  коллектора или потере первого пакета с темплейтом); он дропается с
  метрикой, а не приводит к панике/error.
- **Prometheus-метрики подключены только на уровне `cmd/collector`/
  `internal/observability`**: `internal/ingest`/`internal/pipeline` уже
  считают нужные величины через `atomic.Uint64` (`Received`/`Dropped`/
  `Truncated`/`Invalid`) — эти геттеры оборачиваются в `prometheus.CounterFunc`/
  `GaugeFunc` снаружи (`observability.Metrics.RegisterCounterFunc/GaugeFunc`),
  так что сами протокольные/storage-пакеты остаются свободны от
  Prometheus-зависимости.
- **Docker-образ — `distroless/static` + статическая линковка**
  (`deploy/docker/Dockerfile`): `CGO_ENABLED=0` даёт статический бинарник, в
  финальном образе нет ни shell, ни package manager, ни libc — типичные
  векторы атаки физически отсутствуют. Версия базового образа (`golang:1.26-alpine`) обязана совпадать с `go.mod`'s
  `go 1.26.0` —
  официальные образы `golang` выставляют `GOTOOLCHAIN=local`, при
  несовпадении сборка не предупреждает, а падает.

## Операционная поверхность

HTTP-сервер на `UMBRELLA_HTTP_ADDR` (по умолчанию `:8080`) отдаёт:

- `GET /healthz` — liveness/readiness.
- `GET /metrics` — Prometheus-метрики (`internal/observability`): PPS/EPS,
  p95/p99 latency записи в storage, глубина очередей `out`/`results`,
  счётчики drop/invalid/write-errors.
- `GET /debug/pprof/*` — CPU/heap/goroutine/block/mutex профили; в проде
  этот адрес стоит биндить на приватный интерфейс, отдельный от публичного
  UDP-listener'а.

## Производительность

Конкретные цифры, подтверждённые `benchstat`, — в
[benchmarks.md](./benchmarks.md). Данные о статическом бинарнике,
размере docker-образа и проверке systemd-юнита — в
[deployment.md](./deployment.md).
