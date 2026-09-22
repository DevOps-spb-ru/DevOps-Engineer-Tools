# Как участвовать в разработке

## Структура репозитория

| Путь | Что там |
| --- | --- |
| `Container image optimizer/` | утилита `cio` на Go: CLI, правила анализа, Docker-сборка |
| `SQL backup restore clone/` | сервис `sqlbrc` на Go: бэкап, восстановление и клонирование баз PostgreSQL |
| `.github/workflows/` | CI: линт, тесты, сборка, CodeQL, сканирование Trivy и сборка образов обоих инструментов |
| `.work/` | локальные вспомогательные скрипты (в Git не попадают) |
| `.clinerules/` | локальные правила и справка для AI-ассистента (в Git не попадают) |

## Требования к окружению

| Компонент | Версия | Зачем |
| --- | --- | --- |
| Go | 1.26.8+ | сборка и тесты обоих инструментов (патч зафиксирован в `go.mod`) |
| Docker Engine | 24+ | `cio`: образы и история слоёв читаются через Docker API |
| PostgreSQL | 15 | `sqlbrc`: утилиты `pg_dump`, `pg_restore`, `psql`, `pg_isready` |
| Python 3 | 3.8+ | только для валидации workflows (`.work/validate-workflows.py`) |
| `make` | опционально | в Windows-средах часто нет — используйте `go` напрямую |

## Локальная проверка перед пушем

Всё сразу (рекомендуется) — скрипт затронутого инструмента:

```powershell
powershell -ExecutionPolicy Bypass -File .work/build-and-test.ps1         # cio
powershell -ExecutionPolicy Bypass -File .work/build-and-test-sqlbrc.ps1  # sqlbrc
```

По шагам (в каталоге нужного инструмента: `cd "Container image optimizer"` или
`cd "SQL backup restore clone"`):

```bash
gofmt -l cmd internal   # должно быть пусто
go vet ./...
go test ./... -count=1
go build -trimpath -o bin/cio ./cmd/cio             # для cio
go build -trimpath -o bin/sqlbrc.exe ./cmd/sqlbrc   # для sqlbrc (.exe — только в Windows)
```

Если `make` установлен, те же проверки доступны как `make vet test fmt-check build`.

Линт: `make lint` (нужен `golangci-lint`) или `.work/lint-docker.ps1` — тот же линтер в контейнере
`golangci/golangci-lint` с версией, которая используется в CI.

Проверка зависимостей: `go install golang.org/x/vuln/cmd/govulncheck@v1.8.0`, затем `govulncheck ./...`
(достижимые уязвимости роняют CI). Версию держим той же, что в `.github/workflows/ci.yml`: `v1.8.0`
требует Go ≥ 1.26, а в `go.mod` обоих инструментов объявлена 1.26.8.

Дымовая проверка на живом Docker: `.work/smoke-cio.ps1` (добавьте `-WithTrivy`, чтобы проверить сканирование).

Дымовая проверка `sqlbrc`: `.work/smoke-serve.ps1` поднимает `sqlbrc serve` с временным конфигом
и проверяет вход, страницы, токен API и отказ операции без PostgreSQL (нужен PowerShell 7:
скрипт использует `HttpClient`).

Проверка `sqlbrc` без живого PostgreSQL: `bin/sqlbrc doctor --config deploy/config.example.yaml` —
отчёт по проверкам готовности сервера; код возврата 1 означает «есть ошибки», в том числе
«на этой ОС проверить не удалось» (например, на Windows).

Полная проверка поставки `sqlbrc` в контейнере (нужны Docker и compose): `.work/smoke-container.ps1` —
образ собирается, запускается от непривилегированного пользователя и работает по `postgres.mode: tcp`.

## Definition of Done

1. `gofmt` без замечаний, `go vet ./...` и `go test ./...` проходят.
2. Линт (`golangci-lint run ./...`) без новых замечаний: в конфиге включены `gosec`, `noctx`,
   `exhaustive`, `unparam`, `dupl`, `gocognit` — новый код должен им соответствовать.
3. Новая функциональность покрыта юнит-тестами (`*_test.go` рядом с кодом); для разбора внешних
   данных (отчёты сканера, значения флагов) добавляется fuzz-цель (`Fuzz*`).
4. `govulncheck ./...` не находит достижимых уязвимостей (тот же шаг есть в CI).
5. Поведение и флаги описаны в `README.md` соответствующего инструмента.
6. Изменения пользовательского поведения отражены в `CHANGELOG.md` затронутого инструмента.

## Коммиты

- Формат сообщения: `Add:` / `Update:` / `Fix:` / `Docs:` / `CI:` + что именно изменилось.
  Пример: `Add: cmd/cio analyze — отчёт по слоям и замечаниям сборки`.
- Один коммит — одно логическое изменение; правила и код коммитятся раздельно.
- Коммит делается только по явному запросу автора изменений.

## Ветки и pull request

- Ветка от `main`: `feature/<краткое-имя>` или `fix/<краткое-имя>`.
- PR заполняется по шаблону `.github/PULL_REQUEST_TEMPLATE.md`; в описании — что проверено и как.
- PR должен проходить CI: `Линт`, `Уязвимости зависимостей`, `Тесты`, `Сборка` и `Docker-образ`
  (для обоих инструментов).
- Слияние — только после зелёного CI и ревью.

## Версионирование и релизы

Версии нумеруются по [SemVer](https://semver.org/lang/ru/), схема тегов — `<инструмент>-vX.Y.Z`
(например, `cio-v0.1.0`, `sqlbrc-v0.1.0`). Репозиторий мультипроектный, поэтому общий тег `v0.1.0`
пришлось бы менять, как только в нём появится вторая утилита. Первая версия `cio` — `0.1.0`: пока флаги CLI
и формат JSON-отчёта могут меняться, версия остаётся в `0.x`.

Версии у инструментов независимые, поэтому у каждого инструмента свой `CHANGELOG.md` в его каталоге
(`Container image optimizer/CHANGELOG.md`, `SQL backup restore clone/CHANGELOG.md`), а раздел называется
просто по версии (`## [0.2.0]`): файл и так принадлежит одному инструменту. Ссылки сравнения в конце
файла ведут на теги того же инструмента (`cio-vX.Y.Z`), а в корневом `CHANGELOG.md` остаётся только
указатель на эти файлы.

Публичный контракт инструмента — флаги CLI, формат JSON-отчёта и коды возврата:

| Изменение | Версия |
| --- | --- |
| исправления отчётов, правил, подсказок | patch (`0.1.0` → `0.1.1`) |
| новый флаг, новое правило анализа, новое поле в JSON | minor (`0.1.0` → `0.2.0`) |
| удаление или переименование флага, изменение формата JSON | в `0.x` — minor, начиная с `1.0.0` — major |

Версия попадает в бинарь на этапе сборки (`-ldflags "-X main.version=..."`, см. `Makefile`),
поэтому `--version` печатает версию, коммит и дату сборки — у обоих инструментов (цель `version`
в `Makefile`). Образ получает те же значения build-аргументами `--build-arg VERSION`, `REVISION`
и `DATE` (их передаёт `release-image.yml`), а `cio` — ещё и в метках `org.opencontainers.image.*`.
Значения в образе видны в `--version` внутри контейнера: без `DATE` там было бы «сборка unknown».

### Чеклист релиза

1. Убедиться, что CI на `main` зелёный: `Линт`, `Уязвимости зависимостей`, `Тесты` и `Сборка`
   для нужного инструмента, а если у него есть контейнерная поставка — ещё и `Docker-образ`.
2. В `CHANGELOG.md` инструмента (`Container image optimizer/CHANGELOG.md` или
   `SQL backup restore clone/CHANGELOG.md`) переименовать `## [Unreleased]` в `## [X.Y.Z] - ГГГГ-ММ-ДД`
   (например, `## [0.1.0] - 2026-10-01`), сразу добавить новый пустой `## [Unreleased]`
   и ссылки сравнения в конце файла — на теги того же инструмента.
3. Влить изменения в `main` и поставить аннотированный тег:
   `git tag -a <инструмент>-vX.Y.Z -m "<инструмент> X.Y.Z"`.
4. Отправить тег: `git push origin <инструмент>-vX.Y.Z`.
5. Workflow `.github/workflows/release.yml` соберёт бинари инструмента (общие шаги вынесены
   в вызываемые `.github/workflows/release-binaries.yml` и `.github/workflows/release-image.yml`),
   создаст GitHub Release (`--generate-notes`), приложит SBOM и attestation сборки и опубликует образ
   в GitHub Packages (SBOM, provenance, подпись cosign в keyless-режиме). Тег `latest` обновляют
   только стабильные версии: тег с суффиксом (`0.2.0-rc.1`) его не двигает.
6. Проверить Release (артефакты и `SHA256SUMS`) и пакет: `Packages` → `cio` или `sqlbrc` — видимость
   (public/private) и наличие тега `ghcr.io/devops-spb-ru/<инструмент>:X.Y.Z`.
7. Проверить подпись и attestation (нужны `cosign` и `gh`):

   ```bash
   cosign verify ghcr.io/devops-spb-ru/cio:X.Y.Z \
     --certificate-identity-regexp '^https://github.com/DevOps-spb-ru/DevOps-Engineer-Tools/' \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com
   gh attestation verify cio-linux-amd64 --repo DevOps-spb-ru/DevOps-Engineer-Tools
   ```

8. Убедиться, что образ запускается от непривилегированного пользователя:
   `docker image inspect ghcr.io/devops-spb-ru/cio:X.Y.Z --format '{{.Config.User}}'` → `cio`.

Релизный workflow настроен для обоих инструментов: по тегу `cio-vX.Y.Z` или `sqlbrc-vX.Y.Z` собираются
бинари и публикуется образ соответствующего инструмента (`ghcr.io/devops-spb-ru/cio`,
`ghcr.io/devops-spb-ru/sqlbrc`), поэтому шаги 5–8 применяются к обоим.

### Публикация образа (GitHub Packages → Containers)

Имя пакета должно быть в нижнем регистре (`ghcr.io/devops-spb-ru/cio`), связь пакета с репозиторием
задаётся меткой `org.opencontainers.image.source` в `Dockerfile`.

Локальная публикация (нужен PAT со scope `write:packages` или токен `gh auth token`):

```bash
echo "$TOKEN" | docker login ghcr.io -u <user> --password-stdin
docker build --build-arg VERSION=0.1.0 --build-arg REVISION="$(git rev-parse --short HEAD)" \
  --build-arg DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t ghcr.io/devops-spb-ru/cio:0.1.0 "Container image optimizer"
docker push ghcr.io/devops-spb-ru/cio:0.1.0
```

В CI публикацию делает job `image` релизного workflow через `GITHUB_TOKEN` (permissions
`packages: write`); тег `latest` обновляется вместе с версией. Если пакет ещё не связан
с репозиторием, после первого пуша зайдите в `Packages` → `Package settings` и выполните
`Connect repository`.

Образ публикуется у каждого инструмента, для которого есть поставка в контейнере: сейчас это
`ghcr.io/devops-spb-ru/cio` и `ghcr.io/devops-spb-ru/sqlbrc` (контейнерная поставка `sqlbrc`
появилась в 0.3.0). Инструмент без `Dockerfile` в релизном workflow образ не публикует.

## Как добавить новый инструмент

1. Создайте каталог с понятным именем и `README.md` внутри; образцы структуры —
   [`Container image optimizer`](Container%20image%20optimizer/README.md) и
   [`SQL backup restore clone`](SQL%20backup%20restore%20clone/README.md).
2. Добавьте сборочные файлы (`go.mod`, `Makefile`, при контейнерной поставке — `Dockerfile`)
   и тесты; сборочная команда и тесты должны быть воспроизводимы локально.
3. Добавьте каталог в матрицы workflow: `ci.yml` (линт, зависимости, тесты, сборка; образ —
   если он есть), `trivy-scan.yml` (образ и файловая система) и `codeql.yml` (анализ Go).
   Путь к инструменту задаётся в `working-directory`, `context` и `go-version-file`.
4. Заведите релизные job'ы в `release.yml` по образцу существующих: тег `<инструмент>-vX.Y.Z`,
   вызов `release-binaries.yml` (а для контейнерной поставки — ещё и `release-image.yml`)
   с параметрами `tool`, `dir` и `tag_prefix`.
5. Обновите публичные документы: таблицу инструментов и требования в корневом `README.md`,
   строку в корневом `CHANGELOG.md`, строку в `SECURITY.md` (поддерживаемые версии, область,
   «вне области»), ссылку на README инструмента в `.github/ISSUE_TEMPLATE/config.yml` и варианты
   инструмента в формах issue.
6. Заведите `CHANGELOG.md` в каталоге инструмента (раздел `## [Unreleased]`), добавьте модуль
   Go, а при контейнерной поставке — и образы из `Dockerfile` в `.github/dependabot.yml`.

## Безопасность

- Порядок сообщения об уязвимостях, поддерживаемые версии и область ответственности описаны
  сразу для всех инструментов — в [SECURITY.md](SECURITY.md); приватный отчёт открывается
  во вкладке *Security* → *Report a vulnerability*.
- Секреты, токены и пароли не коммитятся: ни в код, ни в конфиги, ни в документацию.
- Сторонние экшены в workflows фиксируются по SHA; обновления предлагает Dependabot.
- В отчётах утилиты секреты маскируются; если нашли обратное — это баг, заводите issue.
- Образы запускаются без прав root (`cio`, `sqlbrc`). Доступ к `docker.sock` нужен только `cio`:
  он выдаётся группой сокета (`--group-add`, см. README инструмента).
