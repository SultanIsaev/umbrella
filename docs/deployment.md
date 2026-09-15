# Поставка (M11)

## Статический бинарник / кросс-компиляция

`make build-collector` / `make cross-build` собирают с `CGO_ENABLED=0`,
поэтому каждый бинарник статически слинкован (без зависимости от libc) —
подтверждено через `file`:

```
bin/collector-linux-amd64: ELF 64-bit LSB executable, x86-64, version 1 (SYSV),
  statically linked, Go BuildID=..., stripped
bin/collector-linux-arm64: ELF 64-bit LSB executable, ARM aarch64, version 1 (SYSV),
  statically linked, Go BuildID=..., stripped
```

`CROSS_PLATFORMS` в `Makefile` сейчас покрывает `linux/amd64` и
`linux/arm64` — два реальных таргета, под которые этот collector
поставляется (обычные x86-64 сервера и ARM-инстансы в облаке). Команда
`make cross-build` собирает оба под `bin/collector-<os>-<arch>`.

Метаданные версии (`internal/version`) прошиваются через `-ldflags -X` из
`git describe`/`git rev-parse`/времени сборки и в `build-collector`, и в
`docker`/`cross-build` — стартовая строка лога запущенного бинарника всегда
показывает, что реально было собрано, а не `dev`.

## Docker-образ

`deploy/docker/Dockerfile` — двухстадийная сборка: `golang:1.26-alpine`
(версия закреплена под `go.mod`'s `go 1.26.0` — более старый тулчейн
отказывается собирать модуль под `GOTOOLCHAIN=local`) компилирует
статический бинарник, затем `gcr.io/distroless/static-debian12:nonroot`
поставляет только его — ни shell, ни package manager, ни libc в финальном
образе, работает под non-root пользователем по умолчанию.

Build-args `TARGETOS`/`TARGETARCH` позволяют `docker buildx build --platform`
кросс-компилировать сам образ (buildx выставляет их сам); локальный
`make docker` собирает под платформу хоста.

Замеренный размер образа (`make docker-size`, 2026-09-15,
`umbrella-collector:local`):

```
image size: 21914204 bytes (20 MiB)
```

Подтверждено, что образ реально запускается (не только собирается):
`docker run` + `curl /healthz` вернул `ok`, а стартовая строка лога
показала version/commit/build-date, прошитые через `-ldflags` — то есть
цепочка build-arg действительно доходит до финального бинарника.

## systemd-юнит

`deploy/systemd/umbrella-collector.service` — hardened-юнит
(`NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`,
отдельный пользователь `umbrella`, `Restart=on-failure`).

На macOS нет своего systemd, чтобы проверить юнит, поэтому
`make systemd-verify` поднимает эфемерный Linux-контейнер с настоящим
`systemd` как PID 1 (`deploy/docker/systemd-test.Dockerfile`, на базе
`debian:12-slim` + `systemd`/`systemd-sysv`, запускается с `--privileged` и
смонтированным cgroupfs хоста), копирует туда кросс-собранный бинарник
`linux/amd64` и юнит-файл без изменений, и гоняет его через настоящий
`systemctl`. Подтверждено, а не предположено:

- `systemctl start umbrella-collector` завершается с exit 0.
- `systemctl is-active umbrella-collector` → `active`.
- `systemctl status` показывает `Active: active (running)` с реальным PID и
  структурированной стартовой строкой лога collector'а в journal юнита.
- `Restart=on-failure` реально перезапускает процесс: `kill -9` по главному
  PID — и systemd перезапустил его под новым PID в течение секунд, сервис
  снова `active (running)`.

Этот таргет намеренно не входит в `infra-up`/CI — ему нужны `--privileged`
и смонтированный cgroupfs, оба нетипичны за пределами разовой
верификации.

## Как воспроизвести

```sh
make cross-build      # bin/collector-linux-{amd64,arm64}, проверяет статическую линковку
make docker-size      # собирает deploy/docker/Dockerfile, печатает размер образа
make systemd-verify   # поднимает эфемерный systemd-контейнер, стартует настоящий юнит, печатает статус
```
