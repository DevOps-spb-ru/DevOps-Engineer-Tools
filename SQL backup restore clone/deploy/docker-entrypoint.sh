#!/bin/sh
# Точка входа образа sqlbrc: готовит файл пароля PGPASSFILE и передаёт команду
# сервису.
#
# Зачем: при монтировании файла с хоста права не сохраняются (на Windows-хосте
# файл получает 0777), а PostgreSQL отказывается читать pgpass, доступный группе
# или остальным. Поэтому пароль передаётся переменной окружения SQLBRC_PGPASS
# (строка формата PGPASSFILE: host:port:database:user:password), а файл с правами
# 0600 создаётся здесь — внутри контейнера.
#
# Переменные:
#   SQLBRC_PGPASS      — строка pgpass; если не задана, файл не создаётся
#                        (конфиг может указывать на смонтированный файл);
#   SQLBRC_PGPASSFILE  — путь к файлу (по умолчанию /run/sqlbrc/pgpass,
#                        этот же путь указывается в postgres.password_file).
set -eu

if [ -n "${SQLBRC_PGPASS:-}" ]; then
    pgpass="${SQLBRC_PGPASSFILE:-/run/sqlbrc/pgpass}"
    mkdir -p "$(dirname "$pgpass")"
    # umask 077: файл создаётся сразу с правами 0600, промежуточного состояния
    # «пароль читают все» не возникает.
    (umask 077; printf '%s\n' "$SQLBRC_PGPASS" > "$pgpass")
    chmod 0600 "$pgpass"
    export PGPASSFILE="$pgpass"
fi

exec /usr/local/bin/sqlbrc "$@"
