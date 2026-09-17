<h1 align="center">umbrella</h1>
<p align="center"><b>SIEM telemetry collector на Go</b><br>
NetFlow v5/v9, IPFIX, Syslog (RFC 5424) и CEF по UDP → нормализация → ClickHouse или Kafka, с метриками в Prometheus/Grafana.</p>

<p align="center">
  <a href="https://github.com/SultanIsaev/umbrella/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/SultanIsaev/umbrella/actions/workflows/ci.yml/badge.svg"></a>
  <img alt="Go version" src="https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white">
  <a href="./LICENSE"><img alt="License" src="https://img.shields.io/badge/license-MIT-blue"></a>
</p>

---

## Что это

Капстоун-проект: пишется, чтобы попрактиковаться в том, что реально нужно
на позиции Go-разработчика в SIEM/телеметрии — конкурентность под нагрузкой,
разбор бинарных протоколов, батчинг в storage, наблюдаемость и поставка.
Реальный UDP-приёмник, worker pool, несколько протоколов, два storage-бэкенда,
Prometheus-метрики и docker/systemd поставка, всё покрыто тестами.

## Кому это может быть полезно

- Тем, кто оценивает Go-разработчика на позицию рядом с этим доменом (NetFlow/IPFIX/SIEM/highload) — как пример цельного
  решения, а не набора
  разрозненных сниппетов.
- Тем, кто хочет референс: как выглядит Go-пайплайн с backpressure,
  батчингом, graceful shutdown и метриками, без привязки к конкретному
  облаку или фреймворку.
- Как отправная точка для небольшого telemetry/SIEM-пайплайна — **не**
  замена production-решениям вроде Akvorado (см. ниже), а компактная база.

## Аналоги

| Проект                                                                            | Язык | Устройство                                                                  | Чем отличается от umbrella                                                                                                                                    |
|-----------------------------------------------------------------------------------|------|-----------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------|
| [goflow2](https://github.com/netsampler/goflow2) (выросло из Cloudflare's goflow) | Go   | Декодер sFlow/NetFlow/IPFIX → сериализация → транспорт (Kafka/stdout/HTTP)  | Не хранит и не батчит сам — отдаёт декодированные потоки дальше. Нет своего worker pool с backpressure и Prometheus-метрик "из коробки"                       |
| [Akvorado](https://github.com/akvorado/akvorado)                                  | Go   | NetFlow/IPFIX/sFlow → Kafka → ClickHouse → веб-консоль, обогащение SNMP/geo | Production-продукт с UI и обогащением; по данным автора держит ~100k flows/сек на 64GB/24vCPU. umbrella не претендует на такой масштаб и не делает обогащение |
| [pmacct/nfacctd](http://www.pmacct.net/)                                          | C    | Классический NetFlow/sFlow accounting daemon, конфигурация через файлы      | Не Go; сложнее встраивать в Go-инфраструктуру и покрывать тестами так, как это делает `go test`                                                               |

umbrella не конкурирует с Akvorado или goflow2 по зрелости — это тот же
архитектурный паттерн (`ingest → parse → pipeline → storage → observability`)
в маленьком, полностью протестированном и читаемом виде.
Прямых head-to-head бенчмарков против них пока нет — это в планах (ниже).

## Как устроено

```
                  UDP :2055                    fan-out / fan-in           storage.Storage
          ┌────────────────────┐        ┌────────────────────────────┐    ┌───────────────────────┐
          │  internal/ingest   │        │  internal/pipeline         │    │ internal/storage/     │
 пакеты ─▶│  (Listener,        │──out──▶│  (worker pool,             │───▶│   clickhouse (batch)  │
          │   SO_RCVBUF,       │  chan  │   GOMAXPROCS workers)      │    │   kafka (producer)    │
          │   drop+metric on   │        │                            │    │   stub (log/memory)   │
          │   backpressure)    │        │  internal/netflow v5/v9    │    └───────────────────────┘
          └────────────────────┘        │  internal/ipfix            │
                                        │  internal/syslog (RFC 5424)│
                                        │  internal/cef              │
                                        └────────────────────────────┘
                                                      │
                                                      ▼
                                      internal/observability (Prometheus)
                                         PPS/EPS, p95/p99, queue depth
                                                      │
                                                      ▼
                                          :8080  /metrics  /healthz
                                                 /debug/pprof/*
```

`cmd/collector` связывает всё это с graceful shutdown по сигналам (`os/signal.NotifyContext` + `context`, `errgroup`).
`cmd/loadgen` — генератор трафика для нагрузочных тестов и бенчмарков.

## Что реализовано

- **Приём и парсинг** — UDP-listener с backpressure (`internal/ingest`),
  worker pool на `GOMAXPROCS` (`internal/pipeline`), парсеры NetFlow v5 (фиксированный формат),
  NetFlow v9 и IPFIX (template-based, с кэшем шаблонов и обработкой "data record раньше темплейта"),
  Syslog RFC 5424 и CEF. Бинарные и текстовые парсеры фаззятся (`go test -fuzz`).
- **Хранилище** — единый интерфейс `storage.Storage`, за ним ClickHouse (батч-инсерт size-or-timeout) или Kafka
  (producer + at-least-once consumer); бэкенд выбирается конфигурацией.
- **Конкурентность** — весь конкурентный код проверяется `go test -race` и `goleak`.
- **Наблюдаемость** — Prometheus-метрики (PPS/EPS, p95/p99 latency записи, размер очередей) на `/metrics`,
  готовый дашборд Grafana.
- **Поставка** — статический бинарник (`CGO_ENABLED=0`), кросс-компиляция под `linux/amd64`/`arm64`,
  docker-образ на distroless (20 MiB), systemd-юнит.

## Цифры

Полные `benchstat`-таблицы и команды для воспроизведения —
[`docs/benchmarks.md`](./docs/benchmarks.md):

| Изменение                                                                            | Результат                                       |
|--------------------------------------------------------------------------------------|-------------------------------------------------|
| NetFlow v5 encode/decode: ручная упаковка байт вместо reflection в `encoding/binary` | -95.4% времени, -91.3% аллокаций                |
| `storage.Event.Fields`: упорядоченный `[]Field` вместо `map[string]any`              | -31.7% времени, -22.0% памяти, -13.4% аллокаций |
| Форматирование IPv4 вручную вместо `net.IP.String()`                                 | -6.1% времени, аллокации не изменились          |

Данные о поставке — [`docs/deployment.md`](./docs/deployment.md):

| Метрика                                            | Значение                                                                                                       |
|----------------------------------------------------|----------------------------------------------------------------------------------------------------------------|
| Размер docker-образа (distroless/static, non-root) | 20 MiB                                                                                                         |
| Статический бинарник                               | подтверждено `file` (`statically linked`), `linux/amd64` + `linux/arm64`                                       |
| systemd-юнит                                       | стартует и держит `active (running)` на реальном `systemctl`, `Restart=on-failure` проверен убийством процесса |

## Живой пример

Дашборд Grafana под реальным трафиком NetFlow v5 от `cmd/loadgen`:

![Дашборд Grafana: PPS/EPS, latency записи в storage p95/p99, размеры очередей, drops/errors](./docs/img/grafana-dashboard.png)

Глубина очередей держится на 0 под этой нагрузкой — пайплайн успевает без бэклога.
Latency на скриншоте — с дефолтным log-storage стабом (без ClickHouse),
для реальных цифр по бэкендам см. `docs/benchmarks.md`.

## Попробовать самому

```sh
# 1. Инфраструктура: ClickHouse + Redpanda(Kafka) + Prometheus + Grafana
make infra-up

# 2. Collector в фоне
make run-collector

# 3. Трафик — валидный, битый или оба сразу:
make run-loadgen-valid
make run-loadgen-invalid
make run-loadgen-mixed

# с параметрами:
make run-loadgen-mixed LOADGEN_PPS=10000 LOADGEN_INVALID_PPS=1000 LOADGEN_DURATION=2m
```

Дальше — **http://localhost:3000**, без логина, дашборд "Umbrella
Collector" уже готов.

```sh
# остановить всё
make stop-collector
make infra-down          # или make clean-all, чтобы снести ещё volumes + bin/
```

Проверка качества (то же самое гоняет `.github/workflows/ci.yml`):

```sh
go build ./...
go vet ./...
golangci-lint run ./...
go test -race -count=1 ./...
```

По умолчанию collector пишет в in-memory/log-заглушку. Чтобы подключить
настоящие бэкенды:

```sh
make clickhouse-up   # затем export UMBRELLA_CLICKHOUSE_DSN — см. configs/config.example.env
make redpanda-up     # затем export UMBRELLA_KAFKA_BROKERS
```

## Структура репозитория

```
cmd/collector/             точка входа сервиса
cmd/loadgen/               UDP-генератор нагрузки
internal/ingest/           UDP-приёмник: SO_RCVBUF, backpressure
internal/pipeline/         worker pool: fan-out разбора, fan-in результатов
internal/netflow/          декодеры NetFlow v5 и v9
internal/ipfix/            декодер IPFIX
internal/syslog/           декодер RFC 5424 syslog + фрейминг по RFC 6587
internal/cef/              декодер CEF
internal/storage/          интерфейс storage.Storage + clickhouse/kafka + заглушки
internal/observability/    Prometheus-метрики + структурированное логирование
internal/server/           /healthz, /metrics, /debug/pprof/*
internal/config/           конфигурация через переменные окружения
deploy/                    Dockerfile, systemd-юнит, docker-compose стек
docs/                      архитектура, бенчмарки, верификация поставки
test/testdata/             фикстуры для тестов и fuzz-корпуса
```

## Статус и планы

M0–M11 сделаны: ingest, worker pool, NetFlow v5/v9, IPFIX, Syslog/CEF,
ClickHouse, Kafka, performance-проход, Prometheus/Grafana, поставка.
Полная таблица Definition-of-Done — [ROADMAP.md](./ROADMAP.md).

Дальше:

- head-to-head бенчмарк против goflow2/Akvorado на одинаковой нагрузке —
  сейчас есть только внутренние before/after замеры;
- M12 — mTLS/auth для REST/gRPC API;
- M13 — CI benchmark-gate: сборка падает при регрессии `benchstat`;
- M14 — финальная полировка.

## Лицензия

[MIT](./LICENSE)
