package store

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
)

// Раскладка каталога бэкапов:
//
//	<storage.dir>/<база>/20260921T020000Z.dump      — архив pg_dump;
//	<storage.dir>/<база>/20260921T020000Z.json      — метаданные рядом;
//	<storage.dir>/<база>/20260921T020000Z.dump.part — незавершённая запись.
const (
	// ArchiveExt — расширение архива: по нему хранилище и doctor отличают
	// бэкапы от посторонних файлов.
	ArchiveExt = ".dump"
	// MetadataExt — расширение файла метаданных.
	MetadataExt = ".json"
	// PartialExt — суффикс незавершённой записи: файл появляется под этим именем
	// и получает имя архива только после успешного дампа.
	PartialExt = ".part"
	// TimestampLayout — метка времени в имени файла: UTC, ISO 8601 без разделителей.
	// Бэкап должен опознаваться по имени даже если файл метаданных потерян.
	TimestampLayout = "20060102T150405Z"
	// metadataVersion — версия схемы файла метаданных. Несовместимое изменение
	// увеличивает её, а файл другой версии трактуется как «метаданных нет».
	metadataVersion = 1
)

// Status — состояние бэкапа, выведенное из содержимого каталога.
type Status string

const (
	// StatusComplete — архив и метаданные на месте: бэкап пригоден к восстановлению.
	StatusComplete Status = "complete"
	// StatusUnknown — архив есть, а метаданных нет или они не разобраны:
	// файл появился не из сервиса либо был повреждён.
	StatusUnknown Status = "unknown"
)

// Backup — бэкап в каталоге сервиса.
type Backup struct {
	// ID — идентификатор вида «<база>/<имя>»: его принимают restore и clone,
	// он же печатается в списке бэкапов.
	ID string
	// DB — имя базы, к которой относится бэкап.
	DB string
	// Name — имя бэкапа без расширения: 20260921T020000Z или
	// 20260921T020000Z-pre-restore.
	Name string
	// Tag — метка бэкапа (pre-restore и подобные), пустая у обычного бэкапа.
	Tag string
	// CreatedAt — время создания из метаданных, а при их отсутствии — из имени.
	CreatedAt time.Time
	// Path — путь к архиву.
	Path string
	// MetadataPath — путь к файлу метаданных (пустой, если его нет).
	MetadataPath string
	// SizeBytes — размер архива.
	SizeBytes int64
	// SHA256 — контрольная сумма архива из метаданных.
	SHA256 string
	// Note — примечание к бэкапу.
	Note string
	// Format, Compression, ServerVersion, DatabaseSizeBytes — параметры дампа.
	Format            string
	Compression       int
	ServerVersion     string
	DatabaseSizeBytes int64
	// Duration — сколько заняло снятие дампа.
	Duration time.Duration
	// Status — состояние бэкапа.
	Status Status
	// JobID — задача журнала, в рамках которой снят бэкап.
	JobID string
}

// Files возвращает файлы бэкапа: они удаляются вместе (архив и метаданные).
func (b Backup) Files() []string {
	files := make([]string, 0, 2)
	if b.Path != "" {
		files = append(files, b.Path)
	}
	if b.MetadataPath != "" {
		files = append(files, b.MetadataPath)
	}
	return files
}

// ArchiveName — разобранное имя файла архива.
type ArchiveName struct {
	// Time — метка времени создания из имени файла.
	Time time.Time
	// Tag — метка бэкапа, пустая у обычного бэкапа.
	Tag string
}

// FormatArchiveName собирает имя файла архива: «20260921T020000Z.dump» или
// «20260921T020000Z-pre-restore.dump». Метка проверяется тем же правилом, что и
// в argv утилит: через неё имя попадает в путь к файлу.
func FormatArchiveName(createdAt time.Time, tag string) (string, error) {
	name := createdAt.UTC().Format(TimestampLayout)
	if tag != "" {
		if err := pg.ValidateTag(tag); err != nil {
			return "", err
		}
		name += "-" + tag
	}
	return name + ArchiveExt, nil
}

// ParseArchiveName разбирает имя файла архива. Второе значение — признак успеха:
// имя приходит из каталога, где рядом лежат посторонние файлы, поэтому мусор
// должен разбираться как «не бэкап», а не приводить к ошибке.
//
// Функция работает с внешними данными: на любом вводе она обязана либо вернуть
// корректное имя, либо признак отказа (см. FuzzParseArchiveName).
func ParseArchiveName(fileName string) (ArchiveName, bool) {
	trimmed := strings.TrimSuffix(fileName, ArchiveExt)
	if trimmed == fileName || trimmed == "" {
		return ArchiveName{}, false
	}
	stamp := trimmed
	tag := ""
	if before, after, found := strings.Cut(trimmed, "-"); found {
		stamp, tag = before, after
		// Дефис без метки после него — это не бэкап сервиса: имя вида
		// «20260921T020000Z-.dump» короче и на имя не похоже.
		if tag == "" {
			return ArchiveName{}, false
		}
	}
	created, err := time.Parse(TimestampLayout, stamp)
	if err != nil {
		return ArchiveName{}, false
	}
	// Нулевое время (0001-01-01) означает «время неизвестно»: политика хранения
	// такие бэкапы не удаляет, поэтому разбирать имя с такой меткой нельзя.
	if created.IsZero() {
		return ArchiveName{}, false
	}
	if tag != "" && pg.ValidateTag(tag) != nil {
		return ArchiveName{}, false
	}
	return ArchiveName{Time: created, Tag: tag}, true
}

// metadata — содержимое файла метаданных. Структура отделена от Backup: формат
// файла — контракт для будущих версий и для разбора вручную, а поля отчёта
// (пути, состояние) в файле не хранятся.
type metadata struct {
	Version              int    `json:"version"`
	Database             string `json:"database"`
	Name                 string `json:"name"`
	Tag                  string `json:"tag,omitempty"`
	Note                 string `json:"note,omitempty"`
	CreatedAt            string `json:"created_at"`
	SizeBytes            int64  `json:"size_bytes"`
	SHA256               string `json:"sha256"`
	Format               string `json:"format,omitempty"`
	Compression          int    `json:"compression,omitempty"`
	ServerVersion        string `json:"server_version,omitempty"`
	DatabaseSizeBytes    int64  `json:"database_size_bytes,omitempty"`
	DurationMilliseconds int64  `json:"duration_ms,omitempty"`
	JobID                string `json:"job_id,omitempty"`
}

// metadataLimit — предел размера читаемого файла метаданных: файл пишет сам
// сервис, и мегабайта хватает с запасом, а чтение повреждённого файла-гиганта
// не должно съедать память.
const metadataLimit = 1 << 20

// readMetadata читает файл метаданных бэкапа. Отсутствие и повреждение файла —
// не ошибка хранилища: бэкап помечается как StatusUnknown, а решение, что с ним
// делать, принимает вызывающий код.
func (s *Store) readMetadata(path string) (metadata, bool) {
	file, err := s.fs.Open(path)
	if err != nil {
		return metadata{}, false
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, metadataLimit))
	if err != nil {
		return metadata{}, false
	}
	parsed := metadata{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return metadata{}, false
	}
	// Файл другой версии схемы или без контрольной суммы бесполезен: по нему
	// нельзя ни проверить архив, ни показать параметры дампа.
	if parsed.Version != metadataVersion || parsed.SHA256 == "" || parsed.Name == "" {
		return metadata{}, false
	}
	return parsed, true
}

// describe собирает сведения о бэкапе из имени файла и метаданных рядом.
func (s *Store) describe(database, dir, fileName string) Backup {
	backup := Backup{
		DB:     database,
		Name:   strings.TrimSuffix(fileName, ArchiveExt),
		Path:   filepath.Join(dir, fileName),
		Status: StatusUnknown,
	}
	backup.ID = database + "/" + backup.Name
	if parsed, ok := ParseArchiveName(fileName); ok {
		backup.CreatedAt = parsed.Time
		backup.Tag = parsed.Tag
	}
	if info, err := s.fs.Stat(backup.Path); err == nil {
		backup.SizeBytes = info.Size()
	}
	parsed, ok := s.readMetadata(filepath.Join(dir, backup.Name+MetadataExt))
	if !ok {
		return backup
	}
	backup.MetadataPath = filepath.Join(dir, backup.Name+MetadataExt)
	backup.SHA256 = parsed.SHA256
	backup.Note = parsed.Note
	backup.Format = parsed.Format
	backup.Compression = parsed.Compression
	backup.ServerVersion = parsed.ServerVersion
	backup.DatabaseSizeBytes = parsed.DatabaseSizeBytes
	backup.JobID = parsed.JobID
	backup.Duration = time.Duration(parsed.DurationMilliseconds) * time.Millisecond
	backup.Status = StatusComplete
	if created, err := time.Parse(time.RFC3339, parsed.CreatedAt); err == nil {
		backup.CreatedAt = created
	}
	return backup
}

// formatMetadata собирает файл метаданных по бэкапу.
func formatMetadata(backup Backup) ([]byte, error) {
	raw, err := json.MarshalIndent(metadata{
		Version:              metadataVersion,
		Database:             backup.DB,
		Name:                 backup.Name,
		Tag:                  backup.Tag,
		Note:                 backup.Note,
		CreatedAt:            backup.CreatedAt.UTC().Format(time.RFC3339),
		SizeBytes:            backup.SizeBytes,
		SHA256:               backup.SHA256,
		Format:               backup.Format,
		Compression:          backup.Compression,
		ServerVersion:        backup.ServerVersion,
		DatabaseSizeBytes:    backup.DatabaseSizeBytes,
		DurationMilliseconds: backup.Duration.Milliseconds(),
		JobID:                backup.JobID,
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("не удалось собрать метаданные бэкапа %s: %w", backup.ID, err)
	}
	return append(raw, '\n'), nil
}
