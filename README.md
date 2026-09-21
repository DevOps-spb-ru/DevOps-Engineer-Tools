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
| Go | 1.25+ | сборка и тесты утилит на Go |
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

## CI

GitHub Actions (`.github/workflows`): линт, тесты, сборка, сборка образа и его сканирование Trivy.
Релиз — по тегу `cio-vX.Y.Z` (`.github/workflows/release.yml`): бинари для linux/amd64, linux/arm64,
darwin/arm64 и windows/amd64, GitHub Release и публикация образа `ghcr.io/devops-spb-ru/cio`.
Версионирование, чеклист релиза и публикация образа — в [CONTRIBUTING.md](CONTRIBUTING.md).

## Лицензия

GPL-3.0 — см. [LICENSE](LICENSE).
