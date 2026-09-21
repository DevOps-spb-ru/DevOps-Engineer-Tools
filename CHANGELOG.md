# CHANGELOG

Формат — [Keep a Changelog](https://keepachangelog.com/ru/1.1.0/), версионирование — [SemVer](https://semver.org/lang/ru/).

## [Unreleased]

### Add

- версия сборки в самом бинаре: `cio --version` печатает версию, коммит и дату (подставляются через
  `-ldflags` в `Makefile`, CI и релизном workflow), Docker-образ получил OCI-метки
  `org.opencontainers.image.*`;
- релизный workflow `.github/workflows/release.yml`: по тегу `cio-vX.Y.Z` собираются бинари для
  linux/amd64, linux/arm64, darwin/arm64 и windows/amd64, создаётся GitHub Release с `SHA256SUMS`
  и публикуется образ `ghcr.io/devops-spb-ru/cio`;
- в README утилиты — разделы «Зачем это нужно», «Преимущества и отличия» и «Интеграция в CI»
  с примерами для GitHub Actions, GitLab CI и Jenkins;
- в `CONTRIBUTING.md` — раздел о версионировании, чеклисте релиза и публикации образа.

## [0.1.0] - 2026-09-21

### Add

- `Container image optimizer` (`cio`) — CLI для анализа Docker-образов:
  - топ самых больших слоёв с долей от размера образа;
  - 14 правил анализа истории сборки (крупные слои, кэши пакетных менеджеров, секреты, `curl | sh`,
    тулчейн сборки в финальном образе и другие) с уровнем значимости и рекомендацией;
  - сканирование уязвимостей через Trivy: локальный бинарь или контейнер `aquasec/trivy` (docker-fallback),
    отключается флагом `--no-trivy`;
  - форматы отчёта `table` и `json`, запись в файл (`--output`), ненулевой код возврата (`--fail-on`);
  - `Makefile`, `Dockerfile` (multi-stage, Trivy внутри образа), `.golangci.yml`.
- CI (`.github/workflows`): линт, тесты с `-race` и покрытием, сборка бинаря и Docker-образа,
  отдельный workflow со сканированием образа Trivy и загрузкой SARIF в GitHub Security.
- Документация: корневой `README.md`, `CONTRIBUTING.md`, этот `CHANGELOG.md`, шаблоны PR и issue.
- Гигиена репозитория: `.editorconfig`, `.gitattributes`, расширенный `.gitignore`.

### Изменилось

- Trivy: флаги `--trivy-image-src`, `--trivy-docker-socket`, `--trivy-cache`, `--trivy-arg`,
  `--trivy-timeout`; переменные `TRIVY_USERNAME`, `TRIVY_PASSWORD`, `TRIVY_INSECURE` передаются
  в контейнер по имени (значения не попадают в командную строку), а база уязвимостей хранится
  в томе `cio-trivy-cache` и не скачивается при каждом запуске.
- Отчёт больше не обрезает причину сбоя Trivy: печатается вся цепочка ошибок (без кадров трассировки Go),
  подсказка по типовой причине и команда запуска сканера; в JSON добавлено поле `scan.command`.

### Исправлено

- Сканирование приватных реестров больше не упирается в `unable to initialize artifact`: причина
  (нет доступа к локальному демону, `UNAUTHORIZED` от реестра, неполная локальная копия образа)
  видна в отчёте вместе с подсказкой, как её устранить.
- Подсказка для неполной локальной копии образа даёт рабочую команду: `docker rmi` + `docker pull`
  для конкретного образа или `cio analyze --trivy-image-src remote <образ>`; прежний совет
  «перекачайте образ» не помогал, потому что Docker отвечает `Image is up to date`. Для сообщения
  `unable to find the specified image` подсказка больше не сводится к кредам реестра и сначала
  предлагает `--trivy-docker-socket`.

### Известные ограничения

- Слои, для которых BuildKit не отдаёт команду (`<missing>`), проверяются только по размеру.
- Размер слоя берётся из метаданных Docker, а не из распакованного файлового дерева.
- Trivy требует доступа к сети для обновления базы уязвимостей; без сети отчёт по слоям всё равно строится.

[Unreleased]: https://github.com/DevOps-spb-ru/DevOps-Engineer-Tools/compare/cio-v0.1.0...HEAD
[cio-v0.1.0]: https://github.com/DevOps-spb-ru/DevOps-Engineer-Tools/releases/tag/cio-v0.1.0
