.PHONY: fmt vet lint test race bench build build-collector build-loadgen docker run-collector clean \
	clickhouse-up clickhouse-down clickhouse-logs clickhouse-cli

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
	docker compose -f deploy/docker/docker-compose.yml up -d --wait

clickhouse-down:
	docker compose -f deploy/docker/docker-compose.yml down

clickhouse-logs:
	docker compose -f deploy/docker/docker-compose.yml logs -f clickhouse

clickhouse-cli:
	docker compose -f deploy/docker/docker-compose.yml exec clickhouse \
		clickhouse-client --user umbrella --password umbrella --database umbrella
