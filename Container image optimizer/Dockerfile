# syntax=docker/dockerfile:1

# Сборка: docker build -t cio:local .
# Запуск: docker run --rm --group-add 0 -v /var/run/docker.sock:/var/run/docker.sock cio:local analyze alpine:3.20
#
# Контейнеру нужен доступ к docker.sock: утилита читает образы через Docker Engine API.
# Сокет принадлежит root с правами 660, поэтому доступ даёт группа сокета
# (--group-add 0 в Docker Desktop, gid группы docker в Linux — см. README).
#
# Версия попадает в бинарь и в OCI-метки образа:
#   docker build --build-arg VERSION=0.1.0 --build-arg REVISION=$(git rev-parse HEAD) -t cio:0.1.0 .

ARG GO_IMAGE=golang:1.25-alpine
# Тег Trivy фиксируется: с :latest состав образа не воспроизводится, а уязвимости
# встроенного сканера попадают в релизный образ. Обновления приносит Dependabot.
ARG TRIVY_IMAGE=aquasec/trivy:0.74.0
ARG RUNTIME_IMAGE=alpine:3.21

FROM ${GO_IMAGE} AS builder
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/cio ./cmd/cio

# Trivy из официального образа: сканирование работает и внутри контейнера.
FROM ${TRIVY_IMAGE} AS trivy

FROM ${RUNTIME_IMAGE}
ARG VERSION=dev
ARG REVISION=none
# org.opencontainers.image.source связывает пакет в GHCR с репозиторием: без него
# пакет публикуется «непривязанным» и наследует настройки доступа хуже.
LABEL org.opencontainers.image.title="Container image optimizer (cio)" \
      org.opencontainers.image.description="Анализ размера слоёв, ошибок сборки и уязвимостей Docker-образа" \
      org.opencontainers.image.source="https://github.com/DevOps-spb-ru/DevOps-Engineer-Tools" \
      org.opencontainers.image.documentation="https://github.com/DevOps-spb-ru/DevOps-Engineer-Tools/blob/main/Container%20image%20optimizer/README.md" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.licenses="GPL-3.0"
RUN apk add --no-cache ca-certificates \
    && addgroup -g 65532 -S cio \
    && adduser -u 65532 -S -G cio -h /home/cio cio \
    && mkdir -p /home/cio/.cache/trivy \
    && chown -R cio:cio /home/cio
# Путь кэша базы уязвимостей вынесен в переменную окружения: снаружи он известен
# и монтируется томом (-v cio-trivy-cache-img:/home/cio/.cache/trivy).
ENV HOME=/home/cio \
    TRIVY_CACHE_DIR=/home/cio/.cache/trivy
COPY --from=trivy /usr/local/bin/trivy /usr/local/bin/trivy
COPY --from=builder /out/cio /usr/local/bin/cio
# Контейнер работает от непривилегированного пользователя: root ему не нужен.
# Доступ к сокету демона выдаётся группой сокета (--group-add, см. README).
USER cio
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
      CMD ["/usr/local/bin/cio", "--version"]
ENTRYPOINT ["/usr/local/bin/cio"]
CMD ["--help"]
