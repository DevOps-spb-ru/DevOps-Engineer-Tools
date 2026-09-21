# DevOps-Engineer-Tools

Набор утилит для DevOps-инженеров. Каждый инструмент живёт в отдельном каталоге и имеет собственный
`README.md`, сборочные файлы и тесты, поэтому релизы и версии у них независимые (`<инструмент>-vX.Y.Z`).

## Инструменты

| Инструмент | Что делает | Стек | Версия |
| --- | --- | --- | --- |
| [Container image optimizer](Container%20image%20optimizer/README.md) (`cio`) | Анализирует Docker-образы: размер слоёв, устаревшие пакеты и уязвимости (Trivy), рекомендации по уменьшению размера и multi-stage сборке | Go + Docker Engine API + Trivy | 0.2.0 |
| [SQL backup, restore and clone](SQL%20backup%20restore%20clone/README.md) (`sqlbrc`) | Бэкап, восстановление и клонирование баз PostgreSQL: каталог бэкапов с политикой хранения, очередь задач с журналом, проверка готовности сервера (`doctor`) и веб-интерфейс с API (`serve`). Поставка в контейнере и режим `postgres.mode: tcp` — 0.3.0 | Go + утилиты PostgreSQL 15 | 0.3.0 |

История изменений — в `CHANGELOG.md` каждого инструмента: [cio](Container%20image%20optimizer/CHANGELOG.md),
[sqlbrc](SQL%20backup%20restore%20clone/CHANGELOG.md). Формат — Keep a Changelog, нумерация — SemVer.

## Требования

| Компонент | Версия | Кому нужен |
| --- | --- | --- |
| Go | 1.25.14+ | обоим инструментам: сборка и тесты (минимальный патч зафиксирован в `go.mod`) |
| Docker Engine | 24+ (проверено на 29.8.0) | `cio`: образы и история слоёв через Docker API |
| Trivy | 0.74+ или доступный Docker (образ `aquasec/trivy`) | `cio`: поиск уязвимостей; при отсутствии бинаря утилита запускает контейнер |
| PostgreSQL | 15, клиент той же мажорной версии, что сервер | `sqlbrc`: операции выполняются утилитами `pg_dump`, `pg_restore`, `psql`, `pg_isready` |
| Linux + systemd + `sudo` | Debian 12, Ubuntu 22.04+ и подобные | `sqlbrc`: служба живёт на сервере рядом с PostgreSQL (сам бинарь собирается и на Windows) |

## Быстрый старт

`cio` — нужен доступ к демону Docker:

```powershell
cd "Container image optimizer"
go build -o bin/cio.exe ./cmd/cio
./bin/cio.exe analyze postgres:15-alpine
```

`sqlbrc` — проверка сервера с PostgreSQL (на Windows команда честно сообщит, что проверить не удалось):

```powershell
cd "SQL backup restore clone"
go build -o bin/sqlbrc.exe ./cmd/sqlbrc
./bin/sqlbrc.exe doctor --config deploy/config.example.yaml
```

В каталоге каждого инструмента есть `Makefile` (для Linux/CI и для сред с установленным `make`):

```bash
cd "Container image optimizer"   # или "SQL backup restore clone"
make build
make test
make lint
```

Полное описание флагов, конфигурации, форматов отчёта и ограничений — в README инструмента
([`cio`](Container%20image%20optimizer/README.md), [`sqlbrc`](SQL%20backup%20restore%20clone/README.md)).

## Соглашения разработки

- Сообщения коммитов: `Add:` / `Update:` / `Fix:` + короткая суть изменения.
- Definition of Done: `go vet ./...`, `go test ./...`, линт без замечаний, обновлённый `README`.
- Процессы (ветвление, PR, релизы) — в [CONTRIBUTING.md](CONTRIBUTING.md).
- Локальные правила для AI-ассистента лежат в `.clinerules/` — каталог намеренно исключён из Git
  (см. `.gitignore`), поэтому в репозитории его нет.

## CI и безопасность

GitHub Actions (`.github/workflows`):

| Workflow | Что проверяет |
| --- | --- |
| `ci.yml` | для каждого инструмента (`cio` и `sqlbrc`): линт (`golangci-lint` с правилами безопасности), достижимые уязвимости зависимостей (`govulncheck`), тесты (`go vet`, `go test -race`, покрытие в артефакте) и сборка бинаря; отдельный job собирает образ каждого инструмента и проверяет, что он запускается под непривилегированным пользователем (`cio`, `sqlbrc`) |
| `trivy-scan.yml` | уязвимости, секреты и конфигурацию образов обоих инструментов (`cio`, `sqlbrc`) и их файловой системы; находки CRITICAL/HIGH роняют прогон, отчёты уходят в GitHub Security |
| `codeql.yml` | статический анализ Go-кода обоих модулей (`cio`, `sqlbrc`), отчёты — в GitHub Security |

В `.trivyignore` лежат исключения на уязвимости **встроенного в образ сканера** Trivy: каждое
с указанием срока годности (`exp`), после которого «ворота» снова падают и список нужно перепроверить.
Код `cio` и базовый образ проверяются без исключений.

Сторонние экшены закреплены по SHA, обновления приходят через Dependabot (`.github/dependabot.yml`):
модули Go, GitHub Actions и образы из `Dockerfile`.

Схема тегов — `<инструмент>-vX.Y.Z`. `.github/workflows/release.yml` запускается по тегам `cio-vX.Y.Z`
и `sqlbrc-vX.Y.Z`; общая часть — сборка бинарей (linux/amd64, linux/arm64, darwin/arm64,
windows/amd64), `SHA256SUMS`, SBOM в формате CycloneDX, attestation сборки и GitHub Release — вынесена
в вызываемый workflow `.github/workflows/release-binaries.yml`, а публикация образа в GitHub Packages
(сборка для linux/amd64 и linux/arm64, `--sbom`, `--provenance`, подпись cosign в keyless-режиме) —
в `.github/workflows/release-image.yml`: `ghcr.io/devops-spb-ru/cio` и `ghcr.io/devops-spb-ru/sqlbrc`.
Версионирование, чеклист релиза и публикация образа — в [CONTRIBUTING.md](CONTRIBUTING.md),
порядок сообщения об уязвимостях — в [SECURITY.md](SECURITY.md).

## Лицензия

GPL-3.0 — см. [LICENSE](LICENSE).
