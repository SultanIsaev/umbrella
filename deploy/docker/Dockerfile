# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src

# TARGETOS/TARGETARCH подставляет buildx из --platform (см. `make docker`/
# `make docker-cross`) — один Dockerfile обслуживает и обычный локальный
# build, и кросс-компиляцию под другую архитектуру, без явного GOARCH снаружи.
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-s -w \
	-X github.com/SultanIsaev/umbrella/internal/version.Version=$VERSION \
	-X github.com/SultanIsaev/umbrella/internal/version.Commit=$COMMIT \
	-X github.com/SultanIsaev/umbrella/internal/version.BuildDate=$BUILD_DATE" \
	-o /out/collector ./cmd/collector

# distroless/static — минимальный рантайм-слой: ни shell, ни package
# manager, ни libc (бинарник и так статический из-за CGO_ENABLED=0), поэтому
# в образе физически нечем эксплуатировать типичные CVE рантайма.
# nonroot-вариант уже создаёт непривилегированного пользователя без своей ОС-обвязки.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/collector /usr/local/bin/collector

USER nonroot:nonroot
EXPOSE 2055/udp 8080/tcp
ENTRYPOINT ["/usr/local/bin/collector"]
