.PHONY: fmt vet lint test race bench build build-collector build-loadgen docker run-collector stop-collector logs-collector clean clean-all \
	run-loadgen-valid run-loadgen-invalid run-loadgen-mixed \
	clickhouse-up clickhouse-down clickhouse-logs clickhouse-cli \
	redpanda-up redpanda-down redpanda-logs redpanda-cli \
	grafana-up grafana-down grafana-logs \
	infra-up infra-down

BINDIR := bin
MODULE := github.com/SultanIsaev/umbrella

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.BuildDate=$(BUILD_DATE)

fmt:
	gofmt -l -w .

vet:
	go vet ./...

lint:
	golangci-lint run ./...

test:
	go test ./...

race:
	go test -race -count=1 ./...

bench:
	go test -run=^$$ -bench=. -benchmem ./...

build: build-collector build-loadgen

build-collector:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINDIR)/collector ./cmd/collector

build-loadgen:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINDIR)/loadgen ./cmd/loadgen

docker:
	docker build -f deploy/docker/Dockerfile -t umbrella-collector:local .

PIDFILE := $(BINDIR)/collector.pid
LOGFILE := $(BINDIR)/collector.log

# Запускает collector в фоне (stdout/stderr — в $(LOGFILE), pid — в
# $(PIDFILE)) и возвращает терминал сразу после того, как /healthz начал
# отвечать — весь сценарий (collector + loadgen + stop) проходится из
# одного терминала, второй под "живой" collector не нужен. На всякий
# случай сперва останавливает уже запущенный collector (stop-collector) —
# иначе при повторном запуске тут же ловим "address already in use".
# Ожидание готовности — не time.Sleep вслепую, а поллинг с реальной
# проверкой условия (curl /healthz) и границей по числу попыток, ровно тот
# же принцип, что и в Go-коде этого репозитория, просто на уровне shell,
# где нет context.Context/channel, которым можно было бы дождаться этого
# самого события напрямую.
run-collector: build-collector stop-collector
	@(./$(BINDIR)/collector > $(LOGFILE) 2>&1 & echo $$! > $(PIDFILE))
	@echo "collector starting (pid $$(cat $(PIDFILE))), waiting for /healthz..."
	@for i in $$(seq 1 25); do \
		if curl -sf http://127.0.0.1:8080/healthz >/dev/null 2>&1; then \
			echo "collector is up — logs: make logs-collector, stop: make stop-collector"; \
			exit 0; \
		fi; \
		if ! kill -0 $$(cat $(PIDFILE)) 2>/dev/null; then \
			echo "collector exited early, see $(LOGFILE):" >&2; \
			cat $(LOGFILE) >&2; \
			exit 1; \
		fi; \
		sleep 0.2; \
	done; \
	echo "collector did not become healthy in time, see $(LOGFILE)" >&2; \
	exit 1

# Останавливает collector, запущенный через run-collector (по $(PIDFILE)),
# либо, если pidfile нет или устарел (запускали не через make), ищет процесс
# по командной строке через pgrep -f. Шлёт обычный SIGTERM (kill без флагов) —
# тот же сигнал, что и Ctrl+C: run() уже слушает его через
# signal.NotifyContext и делает graceful shutdown сам.
stop-collector:
	@if [ -f $(PIDFILE) ] && kill -0 $$(cat $(PIDFILE)) 2>/dev/null; then \
		echo "stopping collector (pid $$(cat $(PIDFILE)))"; \
		kill $$(cat $(PIDFILE)); \
	elif pid=$$(pgrep -f '$(BINDIR)/collector$$'); [ -n "$$pid" ]; then \
		echo "stopping collector (pid $$pid, no pidfile)"; \
		kill $$pid; \
	else \
		echo "collector is not running"; \
	fi
	@rm -f $(PIDFILE)

# Хвост лога уже запущенного (через run-collector) collector'а.
logs-collector:
	@tail -f $(LOGFILE)

# Параметры для run-loadgen-*, переопределяемые из командной строки, например:
#   make run-loadgen-valid LOADGEN_PPS=10000 LOADGEN_DURATION=2m
LOADGEN_TARGET := 127.0.0.1:2055
LOADGEN_PPS := 3000
LOADGEN_DURATION := 60s
LOADGEN_INVALID_PPS := 500

# Валидный NetFlow v5 трафик — проходит весь путь ingest -> parse -> storage,
# растут umbrella_ingest_packets_received_total и umbrella_storage_events_written_total.
run-loadgen-valid: build-loadgen
	./$(BINDIR)/loadgen -target $(LOADGEN_TARGET) -pps $(LOADGEN_PPS) -duration $(LOADGEN_DURATION) -netflow5

# Битые пакеты (filler-payload без -netflow5) — отбраковываются в pipeline.Pool,
# растёт только umbrella_pipeline_packets_invalid_total.
run-loadgen-invalid: build-loadgen
	./$(BINDIR)/loadgen -target $(LOADGEN_TARGET) -pps $(LOADGEN_INVALID_PPS) -duration $(LOADGEN_DURATION)

# Смешанный трафик: два loadgen'а параллельно (валидный + битый) на разных
# pps — так на графике одновременно видно и рабочий EPS, и ненулевой
# packets_invalid_total. Строчки объединены через "\", чтобы `wait` в общей
# shell-сессии дождался фонового процесса, а не завершился раньше него.
run-loadgen-mixed: build-loadgen
	./$(BINDIR)/loadgen -target $(LOADGEN_TARGET) -pps $(LOADGEN_PPS) -duration $(LOADGEN_DURATION) -netflow5 & \
	./$(BINDIR)/loadgen -target $(LOADGEN_TARGET) -pps $(LOADGEN_INVALID_PPS) -duration $(LOADGEN_DURATION); \
	wait

clean:
	rm -rf $(BINDIR)

# Локальный ClickHouse для internal/storage/clickhouse (M7) — не для прод/CI.
clickhouse-up:
	docker compose -f deploy/docker/docker-compose.yml up -d --wait clickhouse

clickhouse-down:
	docker compose -f deploy/docker/docker-compose.yml stop clickhouse

clickhouse-logs:
	docker compose -f deploy/docker/docker-compose.yml logs -f clickhouse

clickhouse-cli:
	docker compose -f deploy/docker/docker-compose.yml exec clickhouse \
		clickhouse-client --user umbrella --password umbrella --database umbrella

# Локальный Redpanda (Kafka-совместимый брокер) для internal/storage/kafka
# (M8) — не для прод/CI. Брокер снаружи: localhost:9092.
redpanda-up:
	docker compose -f deploy/docker/docker-compose.yml up -d --wait redpanda

redpanda-down:
	docker compose -f deploy/docker/docker-compose.yml stop redpanda

redpanda-logs:
	docker compose -f deploy/docker/docker-compose.yml logs -f redpanda

# Открывает shell внутри контейнера с rpk наготове, например:
#   rpk topic create my-topic --brokers localhost:9092
#   rpk topic list --brokers localhost:9092
#   rpk group describe my-group --brokers localhost:9092
redpanda-cli:
	docker compose -f deploy/docker/docker-compose.yml exec redpanda bash

# Локальный Prometheus + Grafana (M10). Prometheus скрейпит collector на
# хосте (host.docker.internal:8080) — сначала запусти `make run-collector`
# в отдельном терминале, потом `make grafana-up`, затем открой
# http://localhost:3000 (анонимный доступ, сразу под Admin) — дашборд
# "Umbrella Collector" уже там.
grafana-up:
	docker compose -f deploy/docker/docker-compose.yml up -d --wait prometheus grafana

grafana-down:
	docker compose -f deploy/docker/docker-compose.yml stop prometheus grafana

grafana-logs:
	docker compose -f deploy/docker/docker-compose.yml logs -f prometheus grafana

# Поднять/остановить весь локальный стек (ClickHouse + Redpanda + Prometheus
# + Grafana) разом.
infra-up:
	docker compose -f deploy/docker/docker-compose.yml up -d --wait

infra-down:
	docker compose -f deploy/docker/docker-compose.yml down

# Полная очистка после сквозного цикла (run-collector + run-loadgen-* +
# infra-up): останавливает свой collector, гасит весь docker-compose стек
# ВМЕСТЕ с volumes (-v) и удаляет bin/ (бинарники, pid, лог). Разрушительно:
# накопленные данные ClickHouse/Redpanda/Prometheus теряются безвозвратно —
# это ожидаемо для локального dev/demo-стека, но не запускай не глядя, если
# там осталось что-то, что жалко потерять.
clean-all: stop-collector
	docker compose -f deploy/docker/docker-compose.yml down -v
	rm -rf $(BINDIR)
