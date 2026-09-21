# Container image optimizer (`cio`)

## Суть

`cio` анализирует Docker-образ через Docker Engine API и отвечает на три вопроса:

- **где съедается место** — топ слоёв по размеру и их доля в общем размере образа;
- **что не так со сборкой** — типовые ошибки: кэши пакетных менеджеров, отсутствие `--no-install-recommends`,
  секреты в слое, тулчейн сборки в финальном образе, `curl | sh` и другие;
- **что уязвимо** — отчёт Trivy: уязвимости пакетов и мисконфигурации.

## Возможности

- топ-N самых больших слоёв с долей от размера образа;
- набор правил анализа истории сборки (пакет `internal/analyze`) с уровнем значимости и рекомендацией;
- двойной режим Trivy: сначала ищется локальный бинарь, если его нет — запускается контейнер
  `aquasec/trivy:latest` через `docker run`; можно полностью отключить (`--no-trivy`);
- форматы отчёта: `table` (для человека) и `json` (для CI и интеграций);
- код возврата 1 при находках уровня `--fail-on` — удобно встраивать в пайплайны;
- утилита **только читает** данные об образах: ничего не собирает, не удаляет и не изменяет.

## Требования

| Компонент | Версия | Обязателен |
| --- | --- | --- |
| Docker Engine | 24+ (проверено на 29.8.0) | да: через его API берутся метаданные и история слоёв |
| Go | 1.25+ | только для сборки из исходников |
| Trivy | 0.74+ или образ `aquasec/trivy` | нет: без Trivy отчёт по слоям всё равно строится |

## Сборка и запуск

```powershell
# Windows
go build -o bin/cio.exe ./cmd/cio
./bin/cio.exe analyze postgres:15-alpine
```

```bash
# Linux/macOS или среда с make
make build
./bin/cio analyze postgres:15-alpine
```

## Флаги команды `analyze`

| Флаг | По умолчанию | Назначение |
| --- | --- | --- |
| `--format` | `table` | формат отчёта: `table` или `json` |
| `-o`, `--output` | stdout | файл для отчёта |
| `--top` | `10` | сколько самых больших слоёв показать |
| `--min-layer-size` | `10MB` | порог «крупного» слоя |
| `--huge-layer-size` | `100MB` | порог «очень крупного» слоя |
| `--no-trivy` | `false` | не запускать сканирование |
| `--trivy-bin` | поиск в `PATH` | путь к локальному бинарю Trivy |
| `--trivy-image` | `aquasec/trivy:latest` | образ Trivy для запуска через docker |
| `--fail-on` | не задан | вернуть код 1 при находках уровня `info\|low\|medium\|high\|critical` и выше |

Примеры:

```bash
cio analyze postgres:15-alpine
cio analyze --format json --output report.json myapp:1.0
cio analyze --no-trivy --fail-on high myapp:1.0
```

## Пример вывода (фрагмент)

```
ОБРАЗ
  Ссылка:      postgres:15-alpine
  ID:          a1b2c3d4e5f6
  Платформа:   linux/amd64
  Размер:      252.0 MB
  Слоёв:       18 (непустых: 14)

САМЫЕ КРУПНЫЕ СЛОИ (10)
  №  РАЗМЕР    ДОЛЯ   КОМАНДА
  5  120.0 MB  47.6%  /bin/sh -c #(nop) ADD file:... in /

ЗАМЕЧАНИЯ ПО СБОРКЕ (4)
  [HIGH    ] правило layer-too-large, слой 5 (a1b2c3d4e5f6)
  слой занимает 120.0 MB — больше порога 100.0 MB
            рекомендация: перенесите сборку артефактов в multi-stage...

СКАНИРОВАНИЕ TRIVY
  Источник:        docker:aquasec/trivy:latest
  Уязвимостей:     12
  Мисконфигураций: 2
  По уровням:      CRITICAL=1  HIGH=5  MEDIUM=6
```

## Как это работает

1. `dockerclient` — `ImageInspect` (метаданные) и `ImageHistory` (слои и команды сборки).
2. `analyze` — рассчитывает топ слоёв и применяет правила (`rules.go`, `rules_packages.go`, `rules_security.go`).
3. `trivy` — запускает сканер и нормализует JSON-отчёт.
4. `render` — печатает отчёт в `table` или `json`.

## Правила анализа

| Правило | Уровень | Что означает |
| --- | --- | --- |
| `layer-too-large` | high | слой больше `--huge-layer-size` |
| `layer-large` | medium | слой больше `--min-layer-size` |
| `apt-lists-not-cleaned` | medium | индекс apt остался в слое |
| `apt-no-install-recommends` | low | нет флага `--no-install-recommends` |
| `apt-upgrade` | medium | обновление пакетов внутри образа |
| `apk-cache-not-cleaned` | low | `apk add` без `--no-cache` |
| `yum-cache-not-cleaned` | low | нет очистки кэша yum/dnf |
| `pip-cache-not-cleaned` | low | `pip install` без `--no-cache-dir` |
| `npm-cache-not-cleaned` | info | кэш npm остаётся в слое |
| `downloaded-artifact-kept` | info | скачанный файл не удалён в том же слое |
| `curl-pipe-shell` | medium | выполнение скрипта из сети (`curl \| sh`) |
| `secret-in-layer` | high | секрет зафиксирован в `ENV`/`ARG` |
| `build-toolchain-in-final-image` | medium | в образе остались команды сборки |
| `many-copy-layers` | info | много команд `COPY` (много слоёв) |

## Разработка

| Команда | Что делает |
| --- | --- |
| `make build` | собирает `bin/cio` (`bin/cio.exe` в Windows) |
| `make test` | `go test ./...` |
| `make vet` | `go vet ./...` |
| `make fmt` | `gofmt -l -w cmd internal` |
| `make lint` | `golangci-lint run ./...` |
| `make docker` | собирает локальный Docker-образ `cio:local` |

Без `make` те же команды доступны напрямую через `go build/test/vet`. Проверки из корня репозитория —
скрипты `.work/build-and-test.ps1`, `.work/smoke-cio.ps1` (дымовой прогон на живом Docker) и
`.work/lint-docker.ps1` (линтер в контейнере, версия как в CI); подробности — в
[CONTRIBUTING.md](../CONTRIBUTING.md).

## Docker-образ

```bash
docker build -t cio:local .
docker run --rm -v /var/run/docker.sock:/var/run/docker.sock cio:local analyze alpine:3.20
```

Контейнеру нужен доступ к `docker.sock` — утилита читает данные об образах через API демона.

## Ограничения

- история BuildKit может содержать команду `<missing>`: такие слои участвуют в расчёте размера,
  но не проверяются правилами;
- размер слоя берётся из метаданных Docker (без распаковки), поэтому может отличаться от реального
  занимаемого места на диске;
- Trivy обновляет базу уязвимостей из сети: первый запуск может быть долгим, а без сети сканирование
  недоступно (в отчёте появится строка «недоступно: ...», код возврата при этом остаётся 0, если не задан `--fail-on`);
- анализ делается по локальному образу: если образа нет на машине, сначала выполните `docker pull`.

## Лицензия

GPL-3.0 — см. [LICENSE](../LICENSE).
