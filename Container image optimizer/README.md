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
| `--trivy-image-src` | порядок внутри Trivy | источники образов через запятую: `docker,containerd,podman,remote` |
| `--trivy-docker-socket` | `false` | смонтировать сокет Docker в контейнер Trivy, чтобы сканировать локальные образы |
| `--trivy-cache` | `cio-trivy-cache` | том с базой уязвимостей Trivy (`none` — не сохранять кэш) |
| `--trivy-arg` | не задан | дополнительный аргумент Trivy, можно повторять |
| `--trivy-timeout` | `5m` | предел времени на один запуск Trivy |
| `--fail-on` | не задан | вернуть код 1 при находках уровня `info\|low\|medium\|high\|critical` и выше |

Примеры:

```bash
cio analyze postgres:15-alpine
cio analyze --format json --output report.json myapp:1.0
cio analyze --no-trivy --fail-on high myapp:1.0
```

## Сканирование приватных и локальных образов

Trivy запускается отдельным процессом и не наследует доступ к демону Docker, поэтому для приватного
реестра и для образа, которого нет в реестре, нужны явные настройки.

| Ситуация | Что делать |
| --- | --- |
| образ есть только локально | `cio analyze --trivy-docker-socket myapp:1.0` — сокет демона монтируется в контейнер |
| приватный реестр, креды уже в `docker login` | локальный бинарь Trivy: `cio analyze --trivy-image-src remote myapp:1.0` |
| приватный реестр, Trivy в контейнере | задать `TRIVY_USERNAME`/`TRIVY_PASSWORD` (они передаются в контейнер по имени) |
| самоподписанный сертификат реестра | `cio analyze --trivy-arg=--insecure myapp:1.0` |
| сканирование долго не укладывается в лимит | `cio analyze --trivy-timeout 15m myapp:1.0` |
| реестр требует авторизации, но кредов нет | `TRIVY_PASSWORD` не задавать, а запустить `trivy` на хосте после `docker login` |

Пример для PowerShell:

```powershell
$env:TRIVY_USERNAME = 'ci-reader'
$env:TRIVY_PASSWORD = Read-Host 'пароль реестра'
cio analyze docker-hub.iitdgroup.ru/finsynapse/back:5.4.1
```

Переменные `TRIVY_USERNAME`, `TRIVY_PASSWORD` и `TRIVY_INSECURE` передаются в контейнер **по имени**:
значения не попадают ни в командную строку, ни в отчёт. База уязвимостей сохраняется в томе
`--trivy-cache` (по умолчанию `cio-trivy-cache`), поэтому повторные запуски не скачивают её заново;
для параллельных запусков используйте `--trivy-cache none`.

Если сканирование не удалось, отчёт печатает причину целиком, подсказку по типовой ошибке и команду
запуска сканера — её можно выполнить вручную и увидеть полный лог:

```
СКАНИРОВАНИЕ TRIVY
  недоступно: trivy (docker:aquasec/trivy:latest): run error: image scan error: ... 4 errors occurred:
              * docker error: failed to connect to the docker API at unix:///var/run/docker.sock ...
              * remote error: GET https://docker-hub.iitdgroup.ru/v2/app/manifests/5.4.1: UNAUTHORIZED
  подсказка:  реестр требует авторизации: задайте TRIVY_USERNAME и TRIVY_PASSWORD ...
  команда:    docker run --rm -v cio-trivy-cache:/root/.cache/trivy aquasec/trivy:latest image ...
```

Кадры трассировки Go из вывода Trivy в отчёт не попадают, а причина сбоя не обрезается: у Trivy
она часто находится в конце цепочки ошибок.

Проверить, что локальная копия образа целая (Trivy читает образы демона через `docker save`):

```powershell
docker save myapp:1.0 -o image.tar
(Get-Item image.tar).Length / 1MB   # должно быть сопоставимо с размером образа
```

Архив в несколько килобайт вместо сотен мегабайт означает, что слои в локальном хранилище неполные:
перекачайте образ (`docker pull myapp:1.0`) или сканируйте из реестра (`--trivy-image-src remote`).

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
  недоступно (в отчёте появится строка «недоступно: ...» с причиной и подсказкой, код возврата при этом
  остаётся 0, если не задан `--fail-on`);
- Trivy читает локальный образ через `docker save`: если локальная копия неполная (архив в несколько
  килобайт вместо размера образа), сканирование падает с `not found in tar` — перекачайте образ
  (`docker pull`) или используйте `--trivy-image-src remote`;
- анализ делается по локальному образу: если образа нет на машине, сначала выполните `docker pull`.

## Лицензия

GPL-3.0 — см. [LICENSE](../LICENSE).
