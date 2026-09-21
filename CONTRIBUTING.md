# Как участвовать в разработке

## Структура репозитория

| Путь | Что там |
| --- | --- |
| `Container image optimizer/` | утилита `cio` на Go: CLI, правила анализа, Docker-сборка |
| `.github/workflows/` | CI: линт, тесты, сборка, сборка образа, сканирование Trivy |
| `.work/` | локальные вспомогательные скрипты (в Git не попадают) |
| `.clinerules/` | локальные правила и справка для AI-ассистента (в Git не попадают) |

## Требования к окружению

| Компонент | Версия | Зачем |
| --- | --- | --- |
| Go | 1.25+ | сборка и тесты |
| Docker Engine | 24+ | утилите нужен демон: образы читаются через Docker API |
| Python 3 | 3.8+ | только для валидации workflows (`.work/validate-workflows.py`) |
| `make` | опционально | в Windows-средах часто нет — используйте `go` напрямую |

## Локальная проверка перед пушем

Всё сразу (рекомендуется):

```powershell
powershell -ExecutionPolicy Bypass -File .work/build-and-test.ps1
```

По шагам:

```bash
cd "Container image optimizer"
gofmt -l cmd internal   # должно быть пусто
go vet ./...
go test ./... -count=1
go build -trimpath -o bin/cio ./cmd/cio
```

Если `make` установлен, те же проверки доступны как `make vet test fmt-check build`.

Линт: `make lint` (нужен `golangci-lint`) или `.work/lint-docker.ps1` — тот же линтер в контейнере
`golangci/golangci-lint` с версией, которая используется в CI.

Дымовая проверка на живом Docker: `.work/smoke-cio.ps1` (добавьте `-WithTrivy`, чтобы проверить сканирование).

## Definition of Done

1. `gofmt` без замечаний, `go vet ./...` и `go test ./...` проходят.
2. Линт (`golangci-lint run ./...`) без новых замечаний.
3. Новая функциональность покрыта юнит-тестами (`*_test.go` рядом с кодом).
4. Поведение и флаги описаны в `README.md` соответствующего инструмента.
5. Изменения пользовательского поведения отражены в `CHANGELOG.md`.

## Коммиты

- Формат сообщения: `Add:` / `Update:` / `Fix:` / `Docs:` / `CI:` + что именно изменилось.
  Пример: `Add: cmd/cio analyze — отчёт по слоям и замечаниям сборки`.
- Один коммит — одно логическое изменение; правила и код коммитятся раздельно.
- Коммит делается только по явному запросу автора изменений.

## Ветки и pull request

- Ветка от `main`: `feature/<краткое-имя>` или `fix/<краткое-имя>`.
- PR заполняется по шаблону `.github/PULL_REQUEST_TEMPLATE.md`; в описании — что проверено и как.
- PR должен проходить CI: `lint`, `test`, `build`, `docker`.
- Слияние — только после зелёного CI и ревью.

## Версионирование и релизы

Версии нумеруются по [SemVer](https://semver.org/lang/ru/), схема тегов — `<инструмент>-vX.Y.Z`
(например, `cio-v0.1.0`). Репозиторий мультипроектный, поэтому общий тег `v0.1.0` пришлось бы
менять, как только в нём появится вторая утилита. Первая версия `cio` — `0.1.0`: пока флаги CLI
и формат JSON-отчёта могут меняться, версия остаётся в `0.x`.

Публичный контракт инструмента — флаги CLI, формат JSON-отчёта и коды возврата:

| Изменение | Версия |
| --- | --- |
| исправления отчётов, правил, подсказок | patch (`0.1.0` → `0.1.1`) |
| новый флаг, новое правило анализа, новое поле в JSON | minor (`0.1.0` → `0.2.0`) |
| удаление или переименование флага, изменение формата JSON | в `0.x` — minor, начиная с `1.0.0` — major |

Версия попадает в бинарь на этапе сборки (`-ldflags "-X main.version=..."`, см. `Makefile`),
поэтому `cio --version` печатает версию, коммит и дату сборки, а образ получает те же значения
в метках `org.opencontainers.image.*`.

### Чеклист релиза

1. Убедиться, что CI на `main` зелёный (`lint`, `test`, `build`, `docker`).
2. В `CHANGELOG.md` переименовать `## [Unreleased]` в `## [X.Y.Z] - ГГГГ-ММ-ДД`, сразу добавить
   новый пустой `## [Unreleased]` и ссылки сравнения в конце файла.
3. Влить изменения в `main` и поставить аннотированный тег: `git tag -a cio-vX.Y.Z -m "cio X.Y.Z"`.
4. Отправить тег: `git push origin cio-vX.Y.Z`.
5. Workflow `.github/workflows/release.yml` соберёт бинари, создаст GitHub Release
   (`--generate-notes`) и опубликует образ в GitHub Packages.
6. Проверить Release (артефакты и `SHA256SUMS`) и пакет: `Packages` → `cio` — видимость
   (public/private) и наличие тега `ghcr.io/devops-spb-ru/cio:X.Y.Z`.

### Публикация образа (GitHub Packages → Containers)

Имя пакета должно быть в нижнем регистре (`ghcr.io/devops-spb-ru/cio`), связь пакета с репозиторием
задаётся меткой `org.opencontainers.image.source` в `Dockerfile`.

Локальная публикация (нужен PAT со scope `write:packages` или токен `gh auth token`):

```bash
echo "$TOKEN" | docker login ghcr.io -u <user> --password-stdin
docker build --build-arg VERSION=0.1.0 --build-arg REVISION="$(git rev-parse HEAD)" \
  -t ghcr.io/devops-spb-ru/cio:0.1.0 "Container image optimizer"
docker push ghcr.io/devops-spb-ru/cio:0.1.0
```

В CI публикацию делает job `image` релизного workflow через `GITHUB_TOKEN` (permissions
`packages: write`); тег `latest` обновляется вместе с версией. Если пакет ещё не связан
с репозиторием, после первого пуша зайдите в `Packages` → `Package settings` и выполните
`Connect repository`.

## Как добавить новый инструмент

1. Создайте каталог с понятным именем и `README.md` внутри (структура — как у
   [`Container image optimizer`](Container%20image%20optimizer/README.md)).
2. Добавьте сборочные файлы и тесты; сборочная команда и тесты должны быть воспроизводимы локально.
3. Заведите job в `.github/workflows/ci.yml` (линт/тесты/сборка) с `working-directory` на новый каталог.
4. Обновите таблицу инструментов в корневом `README.md` и `CHANGELOG.md`.

## Безопасность

- Секреты, токены и пароли не коммитятся: ни в код, ни в конфиги, ни в документацию.
- В отчётах утилиты секреты маскируются; если нашли обратное — это баг, заводите issue.
