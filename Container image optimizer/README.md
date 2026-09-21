# Container image optimizer (`cio`)

## Суть

`cio` анализирует Docker-образ через Docker Engine API и отвечает на три вопроса:

- **где съедается место** — топ слоёв по размеру и их доля в общем размере образа;
- **что не так со сборкой** — типовые ошибки: кэши пакетных менеджеров, отсутствие `--no-install-recommends`,
  секреты в слое, тулчейн сборки в финальном образе, `curl | sh` и другие;
- **что уязвимо** — отчёт Trivy: уязвимости пакетов и мисконфигурации.

## Зачем это нужно

Образ растёт от сборки к сборке, а причину видно только по косвенным признакам: логи CI, вывод
`docker system df`, жалобы на долгий `docker pull`. `cio` отвечает на эти вопросы одним отчётом —
по уже собранному образу, без пересборки и без изменений в системе.

| Боль | Что показывает `cio` |
| --- | --- |
| «образ вырос до 1.2 GB, непонятно из-за чего» | топ-N слоёв с долей от размера образа и командой, которой слой создан |
| «в финальный образ попал тулчейн сборки или кэш пакетного менеджера» | правила `build-toolchain-in-final-image`, `*-cache-not-cleaned` с рекомендацией |
| «секрет зафиксирован в истории сборки» | правило `secret-in-layer` (`ENV`/`ARG` в слое) |
| «нужен отчёт об уязвимостях прямо в пайплайне» | Trivy: уязвимости по уровням и мисконфигурации, `--format json` для машин |
| «нужно блокировать изменения, из-за которых образ растёт» | `--fail-on` — код возврата 1 при находках уровня и выше |

Отчёт снимается с готового образа, поэтому запускать его можно и в CI (сразу после `docker build`
или `docker pull`), и на рабочей машине — пересборка для анализа не нужна.

## Возможности

- топ-N самых больших слоёв с долей от размера образа;
- набор правил анализа истории сборки (пакет `internal/analyze`) с уровнем значимости и рекомендацией;
- двойной режим Trivy: сначала ищется локальный бинарь, если его нет — запускается контейнер
  `aquasec/trivy:0.74.0` через `docker run`; можно полностью отключить (`--no-trivy`);
- форматы отчёта: `table` (для человека) и `json` (для CI и интеграций);
- код возврата 1 при находках уровня `--fail-on` — удобно встраивать в пайплайны;
- утилита **только читает** данные об образах: ничего не собирает, не удаляет и не изменяет.

## Преимущества и отличия

`cio` не заменяет существующие инструменты, а собирает их ответы в один отчёт: размер и историю
слоёв из Docker Engine API, замечания по сборке из собственных правил, уязвимости из Trivy.

| Инструмент | Что даёт | Чего не хватает для регулярной работы |
| --- | --- | --- |
| `docker history` | размер и команды слоёв | нет правил и уязвимостей, разбор вручную |
| `dive` | интерактивный просмотр содержимого слоёв | нужен TTY, отчёта для CI нет |
| `trivy image` | уязвимости и мисконфигурации | не объясняет размер образа и ошибки сборки |

Что это даёт на практике:

- один запуск вместо нескольких инструментов и ручного сопоставления выводов;
- `--format json` и код возврата: отчёт кладётся артефактом и может ронять сборку;
- утилита ничего не меняет в системе, поэтому её безопасно запускать на любом хосте с демоном;
- локальный Trivy не обязателен: без бинаря сканирование идёт контейнером, а с `--no-trivy`
  остаются размеры слоёв и замечания по сборке;
- причина сбоя сканера печатается целиком, вместе с подсказкой и воспроизводимой командой;
- приватные реестры и локальные образы поддерживаются без пересборки образа.

## Требования

| Компонент | Версия | Обязателен |
| --- | --- | --- |
| Docker Engine | 24+ (проверено на 29.8.0) | да: через его API берутся метаданные и история слоёв |
| Go | 1.25.14+ | только для сборки из исходников (минимальный патч зафиксирован в `go.mod`) |
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

Версия подставляется в бинарь на этапе сборки (`make build` берёт её из `git describe`, CI — из
коммита, релиз — из тега): `cio --version` печатает версию, коммит и дату сборки. Готовые бинари
и образ `ghcr.io/devops-spb-ru/cio` публикуются по тегу `cio-vX.Y.Z` — схема версий и чеклист
релиза описаны в [CONTRIBUTING.md](../CONTRIBUTING.md), история изменений версий — в
[CHANGELOG.md](CHANGELOG.md).

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
| `--trivy-image` | `aquasec/trivy:0.74.0` | образ Trivy для запуска через docker |
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

## Интеграция в CI

Утилита рассчитана на запуск в пайплайне: отчёт `--format json` кладётся артефактом, а `--fail-on`
возвращает код 1 и роняет шаг, если в образе нашлось что-то серьёзнее порога. Каркас одинаков для
любой системы CI:

1. получить образ в демон Docker (`docker build` или `docker pull`);
2. собрать `cio` (`go build -trimpath -o bin/cio ./cmd/cio`) или запустить контейнер `cio:local`;
3. выполнить `cio analyze --format json --output cio-report.json --fail-on high <образ>`
   и забрать `cio-report.json` артефактом.

| Что настроить | Зачем |
| --- | --- |
| `--fail-on high` (или `medium`) | шаг CI падает при находках уровня и выше |
| `--output cio-report.json` + артефакт | отчёт сохраняется даже упавшего шага (`if: always()` / `when: always`) |
| `--trivy-cache <том>` | база уязвимостей не скачивается при каждом запуске |
| `--trivy-timeout 15m` | первый запуск Trivy на медленном реестре не срывается по лимиту |
| `TRIVY_USERNAME`/`TRIVY_PASSWORD` | доступ к приватному реестру: значения передаются в контейнер по имени и не видны ни в командной строке, ни в отчёте |
| `--no-trivy` | только разбор слоёв и правил: быстрее и не требует сети |

### GitHub Actions

```yaml
name: Image analysis

on: [pull_request]

jobs:
  cio:
    runs-on: ubuntu-latest
    env:
      IMAGE: myapp:ci
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: "Container image optimizer/go.mod"
          cache-dependency-path: "Container image optimizer/go.sum"
      - name: Собрать образ
        run: docker build -t "$IMAGE" .
      - name: Собрать cio
        working-directory: Container image optimizer
        run: go build -trimpath -o bin/cio ./cmd/cio
      - name: Проанализировать образ
        working-directory: Container image optimizer
        run: ./bin/cio analyze --format json --output cio-report.json --trivy-docker-socket --fail-on high "$IMAGE"
      - name: Отчёт в summary
        if: always()
        working-directory: Container image optimizer
        run: ./bin/cio analyze --no-trivy "$IMAGE" >> "$GITHUB_STEP_SUMMARY"
      - uses: actions/upload-artifact@v7
        if: always()
        with:
          name: cio-report
          path: Container image optimizer/cio-report.json
          if-no-files-found: warn
```

- образ, собранный в этом же job, есть только в локальном демоне → `--trivy-docker-socket`
  (сокет монтируется в контейнер Trivy);
- для образа из приватного реестра добавьте `--trivy-image-src remote` и `TRIVY_USERNAME`/`TRIVY_PASSWORD`
  из secrets: креды `docker login` контейнер Trivy не наследует;
- `--fail-on` роняет шаг, поэтому артефакт и summary загружаются с `if: always()`.

### GitLab CI

```yaml
stages: [build, scan]

image-analysis:
  stage: scan
  image: docker:27
  services: [docker:27-dind]
  variables:
    DOCKER_HOST: tcp://docker:2376
    DOCKER_TLS_CERTDIR: "/certs"
    IMAGE: myapp:$CI_COMMIT_SHORT_SHA
    TRIVY_USERNAME: $CI_REGISTRY_USER
    TRIVY_PASSWORD: $CI_REGISTRY_PASSWORD
  script:
    - cd "Container image optimizer"
    - go build -trimpath -o bin/cio ./cmd/cio
    # dind: демон удалённый, сокет монтировать нельзя — Trivy читает образ из реестра
    - ./bin/cio analyze --format json --output cio-report.json --trivy-image-src remote --fail-on high "$IMAGE"
  artifacts:
    when: always
    paths: ["Container image optimizer/cio-report.json"]
    expire_in: 1 week
```

- в `dind` демон удалённый, поэтому его сокет в контейнер Trivy не смонтировать: образ сканируется
  источником `remote` (или job запускается на shell-раннере с локальным демоном);
- `TRIVY_USERNAME`/`TRIVY_PASSWORD` задавайте как masked и protected variables — в отчёт значения
  не попадают, в логе команды их тоже нет;
- база уязвимостей внутри `dind` не переживает job: чтобы не качать её каждый раз, прогрейте кэш
  на shell-раннере или соберите свой образ Trivy с базой;
- если образ уже есть в демоне раннера, замените флаг на `--trivy-docker-socket` и передайте
  обычное имя тега вместо адреса реестра.

### Jenkins

```groovy
pipeline {
  agent { label 'docker' }
  environment {
    IMAGE = 'registry.example.com/team/myapp:1.0'
  }
  stages {
    stage('Build cio') {
      steps {
        sh 'cd "Container image optimizer" && go build -trimpath -o bin/cio ./cmd/cio'
      }
    }
    stage('Analyze image') {
      steps {
        withCredentials([usernamePassword(credentialsId: 'registry-read',
                                          usernameVariable: 'TRIVY_USERNAME',
                                          passwordVariable: 'TRIVY_PASSWORD')]) {
          sh '''cd "Container image optimizer"
                ./bin/cio analyze --format json --output cio-report.json \
                  --trivy-image-src remote --fail-on high "$IMAGE"'''
        }
      }
    }
  }
  post {
    always {
      archiveArtifacts artifacts: 'Container image optimizer/cio-report.json', allowEmptyArchive: true
    }
  }
}
```

- агент запускается на узле с доступом к Docker (`agent { label 'docker' }`); для образа, собранного
  на том же узле, используйте `--trivy-docker-socket`;
- креды реестра передавайте через `withCredentials`: `cio` отдаёт переменные контейнеру Trivy
  по имени, поэтому пароль не попадёт ни в лог шага, ни в отчёт;
- `--fail-on` помечает сборку красной, а `post { always { ... } }` сохраняет отчёт артефактом даже
  у упавшего шага;
- если в Jenkins используется агент-контейнер без смонтированного `docker.sock`, сканируйте образ
  из реестра (`--trivy-image-src remote`) — локального демона в контейнере нет.

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
cio analyze registry.example.com/team/app:5.4.1
```

Переменные `TRIVY_USERNAME`, `TRIVY_PASSWORD` и `TRIVY_INSECURE` передаются в контейнер **по имени**:
значения не попадают ни в командную строку, ни в отчёт. База уязвимостей сохраняется в томе
`--trivy-cache` (по умолчанию `cio-trivy-cache`), поэтому повторные запуски не скачивают её заново;
для параллельных запусков используйте `--trivy-cache none`.

Если сканирование не удалось, отчёт печатает причину целиком, подсказку по типовой ошибке и команду
запуска сканера — её можно выполнить вручную и увидеть полный лог:

```
СКАНИРОВАНИЕ TRIVY
  недоступно: trivy (docker:aquasec/trivy:0.74.0): run error: image scan error: ... 4 errors occurred:
              * docker error: failed to connect to the docker API at unix:///var/run/docker.sock ...
              * remote error: GET https://registry.example.com/v2/app/manifests/5.4.1: UNAUTHORIZED
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

Архив в несколько килобайт вместо сотен мегабайт означает, что слои в локальном хранилище неполные.
`docker pull` такую копию не лечит: Docker считает тег актуальным и отвечает `Image is up to date`,
поэтому копию нужно пересоздать:

```powershell
docker rmi myapp:1.0
docker pull myapp:1.0
```

Альтернатива без удаления — сканировать из реестра: `cio analyze --trivy-image-src remote myapp:1.0`
(креды берутся из `docker login` или из `TRIVY_USERNAME`/`TRIVY_PASSWORD`).

В Docker Desktop с containerd image store (`docker info` → `GraphDriver: null`) тех же слоёв может не быть
и в самом хранилище: тогда и `docker save`, и containerd-источник Trivy падают с `not found in tar` /
`content digest ... not found`, хотя `docker image inspect` показывает все слои, а `docker system df` —
полный размер. Локально такой образ не сканируется: помогает только пересоздание копии или источник `remote`.

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
  Источник:        docker:aquasec/trivy:0.74.0
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
| `make version` | собирает и печатает `--version` (проверка подстановки версии через `-ldflags`) |

Без `make` те же команды доступны напрямую через `go build/test/vet`. Проверки из корня репозитория —
скрипты `.work/build-and-test.ps1`, `.work/smoke-cio.ps1` (дымовой прогон на живом Docker) и
`.work/lint-docker.ps1` (линтер в контейнере, версия как в CI); подробности — в
[CONTRIBUTING.md](../CONTRIBUTING.md).

## Docker-образ

```bash
docker build -t cio:local .
docker run --rm --group-add 0 -v /var/run/docker.sock:/var/run/docker.sock cio:local analyze alpine:3.20
```

Контейнеру нужен доступ к `docker.sock` — утилита читает данные об образах через API демона.
Сокет принадлежит `root` с правами `660`, поэтому образ запускается непривилегированным
пользователем `cio`, а доступ выдаётся группой сокета:

| Система | Как выдать доступ к сокету |
| --- | --- |
| Docker Desktop (Windows, macOS) | `--group-add 0` (сокет принадлежит `root:root`) |
| Linux | `--group-add "$(stat -c '%g' /var/run/docker.sock)"` (обычно группа `docker`) |

Без `--group-add` контейнер запустится, но сканирование не сможет обратиться к демону: в отчёте
будет ошибка доступа к сокету с подсказкой, а размеры слоёв и замечания по сборке останутся.

База уязвимостей Trivy в образе лежит в `/home/cio/.cache/trivy`. Чтобы не скачивать её при
каждом запуске, подключите том — у свежего тома права совпадут с каталогом образа:

```bash
docker run --rm --group-add 0 \
  -v cio-trivy-cache-img:/home/cio/.cache/trivy \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/devops-spb-ru/cio:latest analyze alpine:3.20
```

Готовый образ публикуется в GitHub Packages при релизе (теги `X.Y.Z` и `latest`):

```bash
docker run --rm ghcr.io/devops-spb-ru/cio:0.2.0 --version
docker run --rm --group-add 0 -v /var/run/docker.sock:/var/run/docker.sock ghcr.io/devops-spb-ru/cio:latest analyze alpine:3.20
```

Версия Trivy внутри образа фиксирована и переопределяется на сборке (`--build-arg TRIVY_IMAGE=...`),
поэтому обновление сканера не меняет сборку неожиданно. Версия и ревизия исходников видны и снаружи —
в метках `org.opencontainers.image.version` и `org.opencontainers.image.revision`
(`docker image inspect`), и внутри — в выводе `cio --version`.
Локальная сборка с версией: `docker build --build-arg VERSION=0.2.0 -t cio:0.2.0 .`.

## Ограничения

- история BuildKit может содержать команду `<missing>`: такие слои участвуют в расчёте размера,
  но не проверяются правилами;
- размер слоя берётся из метаданных Docker (без распаковки), поэтому может отличаться от реального
  занимаемого места на диске;
- Trivy обновляет базу уязвимостей из сети: первый запуск может быть долгим, а без сети сканирование
  недоступно (в отчёте появится строка «недоступно: ...» с причиной и подсказкой, код возврата при этом
  остаётся 0, если не задан `--fail-on`);
- Trivy читает локальный образ через `docker save`: если локальная копия неполная (архив в несколько
  килобайт вместо размера образа), сканирование падает с `not found in tar` — пересоздайте копию
  (`docker rmi`, затем `docker pull`) или используйте `--trivy-image-src remote`; один `docker pull`
  копию не пересоздаёт (Docker отвечает `Image is up to date`);
- в Docker Desktop с containerd image store копия может быть неполной и в самом хранилище слоёв:
  containerd-источник Trivy падает так же (`content digest ... not found`), поэтому локальное
  сканирование такого образа невозможно — остаётся источник `remote`;
- анализ делается по локальному образу: если образа нет на машине, сначала выполните `docker pull`;
- образ запускается без прав root (пользователь `cio`), поэтому доступ к `docker.sock` выдаётся
  группой сокета (`--group-add`, см. раздел «Docker-образ»);
- том `cio-trivy-cache`, созданный запуском Trivy-контейнера от `root` (хост-версия `cio`), не подходит
  непривилегированному образу: для контейнера используйте отдельный том (`cio-trivy-cache-img`)
  или запуск с `--user root`.

## Лицензия

GPL-3.0 — см. [LICENSE](../LICENSE).
