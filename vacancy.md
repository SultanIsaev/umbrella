# Golang Developer — Ambrella (Защита Информационных Систем)

> [Вакансия на hh.ru](https://hh.ru/vacancy/137026631)

## О компании

Аккредитованная IT-компания в сфере информационной безопасности, развивает
собственный SIEM-продукт и усиливает команду разработки.

## О команде и продукте

Команда развивает сервисы приёма и обработки телеметрии для SIEM:

- **NetFlow Collector** и агенты, работающие с **NetFlow v5/v9, IPFIX, Syslog, Kafka**;
- нормализация событий;
- запись данных в **ClickHouse**.

Ключевые нефункциональные требования к сервисам: высокий **PPS/EPS без потерь
пакетов**, предсказуемая **latency** и минимальное потребление памяти.

Разработка исключительно продуктовая, без legacy-проектов. Использование
AI-инструментов приветствуется — при полном понимании генерируемого кода и
обязательном прохождении code review.

## Зона ответственности

- Разработка высоконагруженных сетевых и backend-сервисов на Go.
- Проектирование конкурентных пайплайнов обработки данных: `ingestion → parsing → enrichment → storage`.
- Разработка парсеров бинарных и текстовых протоколов: NetFlow v5/v9, IPFIX, Syslog RFC 5424, CEF.
- Оптимизация CPU, аллокаций, памяти и latency; работа с метриками PPS, EPS, p95/p99.
- Профилирование сервисов в dev- и production-средах: CPU, heap, block, mutex, goroutine.
- Написание и поддержка benchmark- и нагрузочных тестов, контроль performance-регрессий в CI.
- Разработка REST API и gRPC-интерфейсов.
- Сборка статических бинарников и поставка сервисов в виде systemd units.
- Реализация graceful shutdown и корректной обработки сигналов.
- Диагностика production-инцидентов: потеря пакетов, утечки goroutine, деградация производительности, рост памяти и очередей.
- Разработка unit- и интеграционных тестов.
- Проведение code review и подготовка технической документации.
- Взаимодействие с системными аналитиками, DevOps, SIEM-инженерами и техническими писателями.
- Ежедневный status report о ходе разработки.

## Требования

### Go и Linux

- Отличное знание Go 1.24+.
- Уверенная работа с Linux: CLI, systemd, journalctl.
- Идиоматичный Go: error handling, interfaces, generics, организация пакетов.
- Опыт написания тестов: table-driven tests, `testing.B`, fuzzing.
- `golangci-lint`, `go vet`, использование `-race` в CI.
- Работа с Git.

### Конкурентность

- Глубокое понимание goroutine и планировщика Go.
- Channels, select, worker pool, fan-in / fan-out, backpressure и bounded queues.
- Context: cancellation, deadlines и корректное распространение по стеку вызовов.
- `sync.Mutex`, `RWMutex`, `WaitGroup`, `Once`, `sync/atomic`, `errgroup`, `singleflight`.
- Понимание Go memory model и happens-before.
- Опыт диагностики race condition, deadlock, goroutine leaks и блокировок scheduler.

### Производительность

- Escape analysis (`-gcflags=-m`) и оптимизация аллокаций.
- `sync.Pool`, преаллокация slices/maps, buffer reuse.
- Zero-copy подходы: `[]byte` vs `string`, `bufio`, `encoding/binary`.
- Понимание работы GC: `GOGC`, `GOMEMLIMIT`, GC pressure.
- Работа с `GOMAXPROCS` в контейнерах и cgroups.
- Benchmarking, `benchstat` и корректная интерпретация результатов.
- Понимание PGO, inlining, bounds check elimination.

### Профилирование и диагностика

- pprof: CPU, heap, block, mutex, goroutine profiles; `net/http/pprof`.
- `runtime/trace`, `go tool trace`.
- `runtime/metrics`, `expvar`, `GODEBUG` (`gctrace`, `schedtrace`).
- Flame graphs и `perf` под Linux.
- Анализ memory leaks, растущих очередей и goroutine dumps.
- Структурированное логирование (`log/slog`) и tracing.

### Сеть, данные и безопасность

- Уверенная работа с TCP/UDP и понимание сетевого стека Linux.
- Опыт обработки трафика с высоким PPS: socket buffers (`rmem`), `SO_REUSEPORT`, batch reading.
- Опыт работы с Kafka и ClickHouse, включая batch inserts и схемы под event data.
- Redis.
- TLS/mTLS, X.509.
- Понимание OAuth2 / OIDC / JWT.

### Сборка и поставка

- Статическая сборка с `CGO_ENABLED=0`.
- Cross-compilation через `GOOS`/`GOARCH`.
- Build tags, versioning через `-ldflags`.
- Контроль размера бинарника и времени старта.
- Docker и минимальные образы: distroless / scratch.
- Сборка под Debian и Alpine.

## Будет преимуществом

- Опыт разработки систем 24/7 с требованиями к предсказуемой latency.
- Опыт highload / streaming-систем с объёмами GB/TB+ данных в сутки.
- Опыт оптимизации производительности с измеримым результатом «было / стало».
- Опыт работы с `gopacket` / `AF_PACKET` / PCAP.
- Знание NetFlow / IPFIX / sFlow, включая работу с IPFIX templates.
- gRPC / Protobuf.
- Prometheus (`client_golang`), OpenTelemetry, Grafana.
- Понимание CI/CD, включая benchmark-gates.
- Опыт с `unsafe` и cgo там, где их использование оправдано.
- Опыт разработки Prometheus exporters или CLI-инструментов.
- eBPF, io_uring, XDP и другие low-level подходы к обработке трафика.
- SIMD или assembler в performance-critical участках.
- Понимание внутреннего устройства Go runtime.
- Опыт построения log pipelines или интеграций с SIEM.
- Опыт системного программирования на C / C++ / Rust.
- Контрибуции в open source, собственные Go-библиотеки, технические публикации или выступления по Go performance.

## Обязательно

- Ссылка на GitHub / GitLab с примерами кода: сервисы, парсеры, benchmarks.
- Готовность к full-time работе.
- Опыт работы с production-системами.
- Готовность разрабатывать серверные компоненты корпоративного уровня под Linux.

## Условия

- Работа в аккредитованной IT-компании в сфере информационной безопасности.
- Оформление по ТК РФ.
- Удалённый формат работы.
- Команда сильных разработчиков и возможность напрямую влиять на архитектуру и производительность продукта.
