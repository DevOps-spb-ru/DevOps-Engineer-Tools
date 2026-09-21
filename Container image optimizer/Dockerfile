# syntax=docker/dockerfile:1

# Сборка: docker build -t cio:local .
# Запуск: docker run --rm -v /var/run/docker.sock:/var/run/docker.sock cio:local analyze alpine:3.20
#
# Контейнеру нужен доступ к docker.sock: утилита читает образы через Docker Engine API.

ARG GO_IMAGE=golang:1.25-alpine
ARG TRIVY_IMAGE=aquasec/trivy:latest
ARG RUNTIME_IMAGE=alpine:3.21

FROM ${GO_IMAGE} AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/cio ./cmd/cio

# Trivy из официального образа: сканирование работает и внутри контейнера.
FROM ${TRIVY_IMAGE} AS trivy

FROM ${RUNTIME_IMAGE}
RUN apk add --no-cache ca-certificates
COPY --from=trivy /usr/local/bin/trivy /usr/local/bin/trivy
COPY --from=builder /out/cio /usr/local/bin/cio
ENTRYPOINT ["/usr/local/bin/cio"]
CMD ["--help"]
