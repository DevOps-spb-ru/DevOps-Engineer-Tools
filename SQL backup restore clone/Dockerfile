# syntax=docker/dockerfile:1
# Образ сервиса sqlbrc: сборка бинаря и запуск.
#
# В контейнере сервис работает в режиме postgres.mode: tcp: локального сокета
# PostgreSQL и peer-аутентификации в контейнере нет, поэтому подключение идёт по
# сети от имени роли, а пароль передаётся файлом PGPASSFILE (postgres.password_file,
# права 0600). sudo в образ не ставится — он в этом режиме не нужен.
#
# Версии базовых образов закреплены: обновляет их Dependabot (.github/dependabot.yml).
ARG GO_IMAGE=golang:1.26.8-alpine3.23
ARG RUNTIME_IMAGE=alpine:3.22

FROM ${GO_IMAGE} AS build
WORKDIR /src
# Сначала зависимости: слой пересобирается только при изменении go.mod и go.sum.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# VERSION, REVISION и DATE попадают в `sqlbrc --version` — тем же способом их
# подставляет Makefile и релизный workflow.
ARG VERSION=dev
ARG REVISION=none
ARG DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${REVISION} -X main.date=${DATE}" \
        -o /out/sqlbrc ./cmd/sqlbrc

FROM ${RUNTIME_IMAGE}
# Утилиты PostgreSQL нужны самому сервису: он запускает pg_dump, pg_restore, psql
# и pg_isready. Пакет соответствует версии сервера из примера установки (15).
RUN apk add --no-cache postgresql15-client tzdata \
 && addgroup -S sqlbrc \
 && adduser -S -G sqlbrc -h /home/sqlbrc -s /sbin/nologin sqlbrc \
 && install -d -m 0750 -o sqlbrc -g sqlbrc /var/backups/sqlbrc /etc/sqlbrc \
 && install -d -m 0700 -o sqlbrc -g sqlbrc /var/lib/sqlbrc /run/sqlbrc
COPY --from=build /out/sqlbrc /usr/local/bin/sqlbrc
COPY --chmod=0755 deploy/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
# Образцы конфига, файла пароля и токенов лежат в образе: формат видно рядом,
# не заглядывая в документацию.
COPY deploy/config.example.yaml /etc/sqlbrc/config.example.yaml
COPY deploy/config.container.example.yaml /etc/sqlbrc/config.container.example.yaml
COPY deploy/pgpass.example /etc/sqlbrc/pgpass.example
COPY deploy/tokens.example /etc/sqlbrc/tokens.example
ARG VERSION=dev
LABEL org.opencontainers.image.title="sqlbrc" \
      org.opencontainers.image.description="Бэкап, восстановление и клонирование баз PostgreSQL 15 на одном сервере" \
      org.opencontainers.image.source="https://github.com/DevOps-spb-ru/DevOps-Engineer-Tools" \
      org.opencontainers.image.licenses="GPL-3.0-only" \
      org.opencontainers.image.version="${VERSION}"
ENV SQLBRC_CONFIG=/etc/sqlbrc/config.yaml
EXPOSE 8088
# Сервис работает от непривилегированного пользователя: root в образе не нужен.
USER sqlbrc
# Проверка живости — тот же /healthz, что используют systemd и мониторинг.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD ["/usr/bin/wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8088/healthz"]
# Точка входа создаёт файл пароля PGPASSFILE с правами 0600 из переменной
# SQLBRC_PGPASS: смонтированный с хоста файл прав не сохраняет (см. скрипт).
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["serve"]
