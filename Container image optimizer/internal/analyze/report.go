package analyze

import "time"

// HistoryLayer — сырой элемент истории сборки, полученный из Docker API.
type HistoryLayer struct {
	ID        string
	Created   int64
	CreatedBy string
	Comment   string
	Size      int64
}

// ImageMeta — метаданные образа, полученные из Docker API.
type ImageMeta struct {
	ID           string
	RepoTags     []string
	RepoDigests  []string
	Architecture string
	OS           string
	Created      string
	Size         int64
}

// Layer — слой образа с вычисляемыми полями.
type Layer struct {
	Index          int    `json:"index"`
	ID             string `json:"id"`
	ShortID        string `json:"shortId"`
	Created        int64  `json:"created"`
	CreatedBy      string `json:"createdBy"`
	Comment        string `json:"comment,omitempty"`
	Size           int64  `json:"size"`
	SizeHuman      string `json:"sizeHuman"`
	CumulativeSize int64  `json:"cumulativeSize"`
	Empty          bool   `json:"empty"`
}

// ImageInfo — сводка по образу.
type ImageInfo struct {
	Ref            string   `json:"ref"`
	ID             string   `json:"id"`
	Tags           []string `json:"tags,omitempty"`
	RepoDigests    []string `json:"repoDigests,omitempty"`
	Architecture   string   `json:"architecture,omitempty"`
	OS             string   `json:"os,omitempty"`
	Created        string   `json:"created,omitempty"`
	TotalSize      int64    `json:"totalSize"`
	TotalSizeHuman string   `json:"totalSizeHuman"`
	LayerCount     int      `json:"layerCount"`
	NonEmptyLayers int      `json:"nonEmptyLayers"`
}

// Vulnerability — уязвимость, найденная внешним сканером.
type Vulnerability struct {
	ID               string   `json:"id"`
	Package          string   `json:"package,omitempty"`
	InstalledVersion string   `json:"installedVersion,omitempty"`
	FixedVersion     string   `json:"fixedVersion,omitempty"`
	Severity         Severity `json:"severity"`
	Title            string   `json:"title,omitempty"`
}

// ScanSummary — нормализованная сводка внешнего сканера (Trivy).
type ScanSummary struct {
	Source               string          `json:"source"`
	Command              string          `json:"command,omitempty"`
	BySeverity           map[string]int  `json:"bySeverity,omitempty"`
	TotalVulnerabilities int             `json:"totalVulnerabilities"`
	Misconfigurations    int             `json:"misconfigurations"`
	Top                  []Vulnerability `json:"top,omitempty"`
	Error                string          `json:"error,omitempty"`
}

// Finding — замечание по образу с рекомендацией по исправлению.
type Finding struct {
	Rule           string   `json:"rule"`
	Severity       Severity `json:"severity"`
	LayerIndex     int      `json:"layerIndex"`
	LayerID        string   `json:"layerId,omitempty"`
	Message        string   `json:"message"`
	Recommendation string   `json:"recommendation"`
}

// Report — итоговый отчёт по образу.
type Report struct {
	GeneratedAt time.Time    `json:"generatedAt"`
	Image       ImageInfo    `json:"image"`
	Layers      []Layer      `json:"layers"`
	TopLayers   []Layer      `json:"topLayers"`
	Findings    []Finding    `json:"findings"`
	Scan        *ScanSummary `json:"scan,omitempty"`
}

// Options управляет построением отчёта.
type Options struct {
	// Top — сколько самых больших слоёв включить в отчёт.
	Top int
	// MinLayerSize — порог «крупного» слоя в байтах.
	MinLayerSize int64
	// HugeLayerSize — порог «очень крупного» слоя в байтах.
	HugeLayerSize int64
}

// Build собирает отчёт по метаданным образа и истории сборки.
func Build(ref string, meta ImageMeta, history []HistoryLayer, opts Options) *Report {
	report := &Report{
		GeneratedAt: time.Now().UTC(),
		Image: ImageInfo{
			Ref:            ref,
			ID:             meta.ID,
			Tags:           meta.RepoTags,
			RepoDigests:    meta.RepoDigests,
			Architecture:   meta.Architecture,
			OS:             meta.OS,
			Created:        meta.Created,
			TotalSize:      meta.Size,
			TotalSizeHuman: HumanSize(meta.Size),
		},
	}

	report.Layers = EnrichLayers(history)
	report.Image.LayerCount = len(report.Layers)
	for _, layer := range report.Layers {
		if !layer.Empty {
			report.Image.NonEmptyLayers++
		}
	}
	report.TopLayers = TopLayers(report.Layers, opts.Top)
	report.Findings = AnalyzeLayers(report.Layers, opts)
	return report
}

// HighestFindingSeverity возвращает самый критичный уровень среди находок.
// Второе значение — false, если находок нет.
func (r *Report) HighestFindingSeverity() (Severity, bool) {
	var highest Severity
	found := false
	for _, finding := range r.Findings {
		if !found || finding.Severity.Rank() > highest.Rank() {
			highest = finding.Severity
			found = true
		}
	}
	return highest, found
}
