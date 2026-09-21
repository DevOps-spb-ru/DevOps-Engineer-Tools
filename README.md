# DevOps-Engineer-Tools

Набор утилит для DevOps-инженеров. Каждый инструмент живёт в отдельном каталоге и имеет собственный
`README.md`, сборочные файлы и тесты.

## Инструменты

| Инструмент | Что делает | Стек | Документация |
| --- | --- | --- | --- |
| Container image optimizer | Анализирует Docker-образы: размер слоёв, устаревшие пакеты и уязвимости (Trivy), рекомендации по уменьшению размера и multi-stage сборке | Go 1.25 + Docker Engine API + Trivy | [README](Container%20image%20optimizer/README.md) |

## Требования

| Компонент | Версия | Зачем |
| --- | --- | --- |
| Go | 1.25.14+ | сборка и тесты утилит на Go (минимальный патч зафиксирован в `go.mod`) |
| Docker Engine | 24+ (проверено на 29.8.0) | доступ к Docker API: образы, история слоёв |
| Trivy | 0.74+ или доступный Docker (образ `aquasec/trivy`) | поиск уязвимостей; при отсутствии бинаря утилита запускает контейнер |

## Быстрый старт

```powershell
cd "Container image optimizer"
go build -o bin/cio.exe ./cmd/cio
./bin/cio.exe analyze postgres:15-alpine
```

В каталоге инструмента есть `Makefile` (для Linux/CI и для сред с установленным `make`):

```bash
cd "Container image optimizer"
make build
make test
make lint
```

Полное описание флагов, форматов отчёта и ограничений — в
[README инструмента](Container%20image%20optimizer/README.md).

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
| `ci.yml` | линт (`golangci-lint` с правилами безопасности), тесты с покрытием, `govulncheck`, сборка бинаря и образа |
| `trivy-scan.yml` | уязвимости, секреты и конфигурацию образа и файловой системы; находки CRITICAL/HIGH роняют прогон, отчёты уходят в GitHub Security |
| `codeql.yml` | статический анализ Go-кода (CodeQL), отчёты — в GitHub Security |

В `.trivyignore` лежат исключения на уязвимости **встроенного в образ сканера** Trivy: каждое
с указанием срока годности (`exp`), после которого «ворота» снова падают и список нужно перепроверить.
Код `cio` и базовый образ проверяются без исключений.

Сторонние экшены закреплены по SHA, обновления приходят через Dependabot (`.github/dependabot.yml`):
модули Go, GitHub Actions и образы из `Dockerfile`.

Релиз — по тегу `cio-vX.Y.Z` (`.github/workflows/release.yml`): бинари для linux/amd64, linux/arm64,
darwin/arm64 и windows/amd64, GitHub Release с `SHA256SUMS`, SBOM в формате CycloneDX и attestation сборки,
образ `ghcr.io/devops-spb-ru/cio` с SBOM, provenance и подписью cosign (keyless).
Версионирование, чеклист релиза и публикация образа — в [CONTRIBUTING.md](CONTRIBUTING.md),
порядок сообщения об уязвимостях — в [SECURITY.md](SECURITY.md).

## Лицензия

GPL-3.0 — см. [LICENSE](LICENSE).
