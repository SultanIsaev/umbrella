# План подготовки: Golang Developer @ Umbrella

Вакансия: [vacancy.md](./vacancy.md). Профиль — SIEM-платформа: сбор и обработка
телеметрии (NetFlow/IPFIX/Syslog/Kafka → ClickHouse) с упором на PPS/EPS,
latency и память.

---

## Порядок реализации капстоуна (M0–M14)

Таблица ниже — порядок, в котором стоит писать код: вертикальными срезами.
Каждый шаг добавляет вертикальный кусок реального пайплайна (`ingestion → parsing → enrichment → storage`),
который сразу собирается, проходит `go test -race` end-to-end.

| #       | Что делаешь                                      | Пакеты                                                                          | Definition of Done                                                                                                 |
|---------|--------------------------------------------------|---------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------|
| **M0**  | Разобраться в каркасе, который уже есть          | `cmd/collector`, `internal/config`, `internal/server`, `internal/observability` | можешь пересказать, как работает graceful shutdown, не подсматривая                                                |
| **M1**  | Парсер **NetFlow v5** — первый вертикальный срез | `internal/netflow/v5.go`                                                        | table-driven тесты + фикстуры в `test/testdata/netflow`, бенчмарк записан в `docs/benchmarks.md`                   |
| **M2**  | UDP-приёмник                                     | `internal/ingest`                                                               | `loadgen` → collector реально принимает пакеты; `SO_RCVBUF`, backpressure (drop+метрика при переполнении канала)   |
| **M3**  | Worker pool / fan-in, подключаем парсер          | `internal/pipeline`                                                             | `go test -race` и `goleak` чистые; graceful shutdown дожидается воркеров                                           |
| **M4**  | Storage-заглушка, замыкаем пайплайн end-to-end   | реализация `storage.Storage` (лог/in-memory)                                    | `loadgen → ingest → parse → storage` работает целиком, видно в логах                                               |
| **M5**  | **NetFlow v9 / IPFIX** — темплейты               | `internal/netflow` (v9), `internal/ipfix`                                       | edge case "data record раньше темплейта" покрыт тестом                                                             |
| **M6**  | **Syslog RFC 5424 + CEF**                        | `internal/syslog`, `internal/cef`                                               | fuzz-корпус засеян, `go test -fuzz` не падает                                                                      |
| **M7**  | Реальный storage — **ClickHouse batch insert**   | `internal/storage/clickhouse`                                                   | батчинг по size-or-timeout через `select`+таймер (без `time.Sleep`)                                                |
| **M8**  | **Kafka** producer/consumer                      | `internal/storage/kafka`                                                        | consumer group, graceful shutdown по `context`                                                                     |
| **M9**  | Performance-проход по всему пайплайну            | правки в `netflow`/`ingest`/`pipeline`                                          | `pprof` под нагрузкой `loadgen`, минимум 2 оптимизации с `benchstat` в `docs/benchmarks.md`                        |
| **M10** | Наблюдаемость                                    | `internal/observability` (metrics)                                              | Prometheus-метрики PPS/EPS, p95/p99, размер очередей — видно через `/metrics`                                      |
| **M11** | Сборка и поставка                                | `deploy/`                                                                       | статический бинарник, cross-compile, размер docker-образа задокументирован, systemd-юнит реально стартует          |
| **M12** | TLS/mTLS + auth (если делаешь REST/gRPC API)     | `internal/server` / `api/proto`                                                 | mTLS между сервисами, JWT-проверка на API                                                                          |
| **M13** | CI benchmark-gate                                | `.github/workflows/ci.yml`                                                      | CI падает при регрессии `benchstat` относительно baseline                                                          |
| **M14** | Финальная полировка портфолио и интервью         | `README.md`, `docs/architecture.md`                                             | цифры в README настоящие (не придуманные), 2–3 истории "production incident" подкреплены реальными профилями из M9 |

Практика на каждом шаге одна и та же: пишешь логику → `go test -race
-count=1 ./...` → коммит. Не переходи к следующему M, пока предыдущий не
проходит тесты и не запускается end-to-end — иначе накопится тот же долг
недописанных абстракций, которого эта таблица призвана избежать.
