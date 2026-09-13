.PHONY: fmt vet lint test race bench build build-collector build-loadgen docker run-collector clean \
	clickhouse-up clickhouse-down clickhouse-logs clickhouse-cli \
	redpanda-up redpanda-down redpanda-logs redpanda-cli \
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

run-collector: build-collector
	./$(BINDIR)/collector

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

# Поднять/остановить весь локальный стек (ClickHouse + Redpanda) разом.
infra-up:
	docker compose -f deploy/docker/docker-compose.yml up -d --wait

infra-down:
	docker compose -f deploy/docker/docker-compose.yml down
