.PHONY: fmt vet lint test race bench build build-collector build-loadgen cross-build docker docker-size run-collector stop-collector logs-collector clean clean-all \
	run-loadgen-valid run-loadgen-invalid run-loadgen-mixed \
	clickhouse-up clickhouse-down clickhouse-logs clickhouse-cli \
	redpanda-up redpanda-down redpanda-logs redpanda-cli \
	grafana-up grafana-down grafana-logs \
	systemd-verify \
	infra-up infra-down

BINDIR := bin
MODULE := github.com/SultanIsaev/umbrella

# Compose-команда: предпочитаем V2-плагин (`docker compose`, поставляется с
# современным Docker Desktop/Engine), но не у всех он есть "из коробки" —
# например, старый standalone docker-compose (pip/brew-бинарник) или
# Colima/Rancher Desktop без плагина. Определяем один раз при разборе
# Makefile и переиспользуем во всех infra-таргетах ниже, вместо того чтобы
# хардкодить одну конкретную команду.
COMPOSE := $(shell docker compose version >/dev/null 2>&1 && echo "docker compose" || echo "docker-compose")

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

# Платформы для cross-build/docker-cross. GOOS/GOARCH — только пары, которые
# реально имеет смысл поставлять: linux/amd64 и linux/arm64 — типичные
# серверные/ARM-облачные таргеты для этого collector'а.
CROSS_PLATFORMS := linux/amd64 linux/arm64

# Собирает статический бинарник collector'а под каждую платформу из
# CROSS_PLATFORMS в $(BINDIR)/collector-<os>-<arch> — CGO_ENABLED=0 уже
# гарантирует статическую линковку (никакого cgo/libc), cross-build здесь
# работает без cgo-тулчейнов под чужую архитектуру именно поэтому.
cross-build:
	@for platform in $(CROSS_PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		out=$(BINDIR)/collector-$$os-$$arch; \
		echo "building $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o $$out ./cmd/collector || exit 1; \
	done
	@echo "--- static linkage check (should say 'statically linked' for each) ---"
	@for platform in $(CROSS_PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		file $(BINDIR)/collector-$$os-$$arch; \
	done

DOCKER_IMAGE := umbrella-collector:local

docker:
	docker build -f deploy/docker/Dockerfile \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(DOCKER_IMAGE) .

# Печатает размер собранного образа (для docs/deployment.md) — байты берутся
# из docker image inspect, а не из `docker images` (тот округляет и
# форматирует по-разному в зависимости от версии Docker); MiB считаем сами,
# потому что шаблонизатор `docker inspect` — обычный text/template без
# арифметических функций (div недоступен).
docker-size: docker
	@bytes=$$(docker image inspect $(DOCKER_IMAGE) --format '{{.Size}}'); \
	echo "image size: $$bytes bytes ($$(( bytes / 1048576 )) MiB)"

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
	$(COMPOSE) -f deploy/docker/docker-compose.yml up -d --wait clickhouse

clickhouse-down:
	$(COMPOSE) -f deploy/docker/docker-compose.yml stop clickhouse

clickhouse-logs:
	$(COMPOSE) -f deploy/docker/docker-compose.yml logs -f clickhouse

clickhouse-cli:
	$(COMPOSE) -f deploy/docker/docker-compose.yml exec clickhouse \
		clickhouse-client --user umbrella --password umbrella --database umbrella

# Локальный Redpanda (Kafka-совместимый брокер) для internal/storage/kafka
# (M8) — не для прод/CI. Брокер снаружи: localhost:9092.
redpanda-up:
	$(COMPOSE) -f deploy/docker/docker-compose.yml up -d --wait redpanda

redpanda-down:
	$(COMPOSE) -f deploy/docker/docker-compose.yml stop redpanda

redpanda-logs:
	$(COMPOSE) -f deploy/docker/docker-compose.yml logs -f redpanda

# Открывает shell внутри контейнера с rpk наготове, например:
#   rpk topic create my-topic --brokers localhost:9092
#   rpk topic list --brokers localhost:9092
#   rpk group describe my-group --brokers localhost:9092
redpanda-cli:
	$(COMPOSE) -f deploy/docker/docker-compose.yml exec redpanda bash

# Локальный Prometheus + Grafana (M10). Prometheus скрейпит collector на
# хосте (host.docker.internal:8080) — сначала запусти `make run-collector`
# в отдельном терминале, потом `make grafana-up`, затем открой
# http://localhost:3000 (анонимный доступ, сразу под Admin) — дашборд
# "Umbrella Collector" уже там.
grafana-up:
	$(COMPOSE) -f deploy/docker/docker-compose.yml up -d --wait prometheus grafana

grafana-down:
	$(COMPOSE) -f deploy/docker/docker-compose.yml stop prometheus grafana

grafana-logs:
	$(COMPOSE) -f deploy/docker/docker-compose.yml logs -f prometheus grafana

# Проверяет, что deploy/systemd/umbrella-collector.service реально
# стартует, а не только валиден по синтаксису — на macOS нет своего
# systemd, поэтому поднимаем эфемерный Linux-контейнер с systemd как PID 1
# (deploy/docker/systemd-test.Dockerfile), кладём туда собранный бинарник и
# юнит-файл as-is, и гоняем через настоящий systemctl. Тест разрушает и
# пересоздаёт контейнер при каждом запуске (умышленно — чистое состояние),
# идемпотентен. Требует --privileged и смонтированный cgroupfs, поэтому не
# входит в infra-up/CI, только по явному вызову.
systemd-verify: cross-build
	docker build -f deploy/docker/systemd-test.Dockerfile -t umbrella-systemd-test:local deploy/docker
	docker rm -f umbrella-systemd-verify >/dev/null 2>&1 || true
	docker run -d --name umbrella-systemd-verify --privileged \
		--tmpfs /tmp --tmpfs /run --tmpfs /run/lock \
		-v /sys/fs/cgroup:/sys/fs/cgroup:rw \
		umbrella-systemd-test:local
	@echo "waiting for systemd to report 'running'..."
	@for i in $$(seq 1 25); do \
		state=$$(docker exec umbrella-systemd-verify systemctl is-system-running 2>/dev/null || true); \
		if [ "$$state" = "running" ] || [ "$$state" = "degraded" ]; then break; fi; \
		sleep 0.4; \
	done
	docker cp $(BINDIR)/collector-linux-amd64 umbrella-systemd-verify:/usr/local/bin/collector
	docker exec umbrella-systemd-verify chmod +x /usr/local/bin/collector
	docker cp deploy/systemd/umbrella-collector.service umbrella-systemd-verify:/etc/systemd/system/umbrella-collector.service
	docker exec umbrella-systemd-verify useradd --system --no-create-home --shell /usr/sbin/nologin umbrella
	docker exec umbrella-systemd-verify systemctl daemon-reload
	docker exec umbrella-systemd-verify systemctl start umbrella-collector
	docker exec umbrella-systemd-verify systemctl is-active umbrella-collector
	docker exec umbrella-systemd-verify systemctl status umbrella-collector --no-pager
	docker rm -f umbrella-systemd-verify >/dev/null

# Поднять/остановить весь локальный стек (ClickHouse + Redpanda + Prometheus
# + Grafana) разом.
infra-up:
	$(COMPOSE) -f deploy/docker/docker-compose.yml up -d --wait

infra-down:
	$(COMPOSE) -f deploy/docker/docker-compose.yml down

# Полная очистка после сквозного цикла (run-collector + run-loadgen-* +
# infra-up): останавливает свой collector, гасит весь docker-compose стек
# ВМЕСТЕ с volumes (-v) и удаляет bin/ (бинарники, pid, лог). Разрушительно:
# накопленные данные ClickHouse/Redpanda/Prometheus теряются безвозвратно —
# это ожидаемо для локального dev/demo-стека, но не запускай не глядя, если
# там осталось что-то, что жалко потерять.
clean-all: stop-collector
	$(COMPOSE) -f deploy/docker/docker-compose.yml down -v
	rm -rf $(BINDIR)
