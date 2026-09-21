package analyze

import (
	"strings"
	"testing"
)

func TestShortID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{name: "полный digest", id: "sha256:" + strings.Repeat("a", 64), want: strings.Repeat("a", 12)},
		{name: "короткий id", id: "abc123", want: "abc123"},
		{name: "пустой id", id: "", want: ""},
		{name: "слой без идентификатора (BuildKit)", id: "<missing>", want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ShortID(test.id); got != test.want {
				t.Errorf("ShortID(%q) = %q, ожидалось %q", test.id, got, test.want)
			}
		})
	}
}

func TestEnrichLayers(t *testing.T) {
	history := []HistoryLayer{
		{ID: "sha256:" + strings.Repeat("b", 64), Created: 1, CreatedBy: "/bin/sh -c #(nop) ADD file:a in / ", Size: 5_000_000},
		{ID: "sha256:" + strings.Repeat("c", 64), Created: 2, CreatedBy: "/bin/sh -c #(nop)  ENV PATH=/usr/bin", Size: 0},
		{ID: "sha256:" + strings.Repeat("d", 64), Created: 3, CreatedBy: "/bin/sh -c apt-get update", Size: 1_500_000},
	}

	layers := EnrichLayers(history)
	if len(layers) != len(history) {
		t.Fatalf("получено %d слоёв, ожидалось %d", len(layers), len(history))
	}

	if layers[0].Index != 0 || layers[2].Index != 2 {
		t.Errorf("индексы слоёв заданы неверно: %d, %d", layers[0].Index, layers[2].Index)
	}
	if layers[1].Empty != true || layers[0].Empty != false {
		t.Errorf("признак пустого слоя определён неверно")
	}
	if layers[0].CumulativeSize != 5_000_000 || layers[2].CumulativeSize != 6_500_000 {
		t.Errorf("накопительный размер задан неверно: %d, %d",
			layers[0].CumulativeSize, layers[2].CumulativeSize)
	}
	if layers[0].ShortID != strings.Repeat("b", 12) {
		t.Errorf("ShortID слоя = %q", layers[0].ShortID)
	}
	if layers[0].SizeHuman != "5.0 MB" {
		t.Errorf("SizeHuman = %q, ожидалось %q", layers[0].SizeHuman, "5.0 MB")
	}
}

func TestTopLayers(t *testing.T) {
	layers := EnrichLayers([]HistoryLayer{
		{ID: "1", Size: 10},
		{ID: "2", Size: 300},
		{ID: "3", Size: 200},
	})

	top := TopLayers(layers, 2)
	if len(top) != 2 {
		t.Fatalf("получено %d слоёв, ожидалось 2", len(top))
	}
	if top[0].ID != "2" || top[1].ID != "3" {
		t.Errorf("порядок слоёв неверный: %q, %q", top[0].ID, top[1].ID)
	}
	if TopLayers(layers, 0) != nil {
		t.Error("TopLayers(0) должен вернуть nil")
	}
	if TopLayers(nil, 5) != nil {
		t.Error("TopLayers для пустого списка должен вернуть nil")
	}
}

func TestBuild(t *testing.T) {
	meta := ImageMeta{
		ID:           "sha256:" + strings.Repeat("e", 64),
		RepoTags:     []string{"postgres:15-alpine"},
		Architecture: "amd64",
		OS:           "linux",
		Created:      "2026-01-02T03:04:05.000000000Z",
		Size:         412_300_000,
	}
	history := []HistoryLayer{
		{ID: "sha256:" + strings.Repeat("f", 64), CreatedBy: "/bin/sh -c #(nop) ADD file:a in / ", Size: 400_000_000},
		{ID: "sha256:" + strings.Repeat("a", 64), CreatedBy: "/bin/sh -c #(nop)  CMD [\"postgres\"]", Size: 0},
	}

	report := Build("postgres:15-alpine", meta, history, Options{
		Top:           DefaultTopLayers,
		MinLayerSize:  DefaultMinLayerSize,
		HugeLayerSize: DefaultHugeLayerSize,
	})

	if report.Image.Ref != "postgres:15-alpine" {
		t.Errorf("Ref = %q", report.Image.Ref)
	}
	if report.Image.TotalSizeHuman != "412.3 MB" {
		t.Errorf("TotalSizeHuman = %q, ожидалось %q", report.Image.TotalSizeHuman, "412.3 MB")
	}
	if report.Image.LayerCount != 2 || report.Image.NonEmptyLayers != 1 {
		t.Errorf("слои посчитаны неверно: всего %d, непустых %d",
			report.Image.LayerCount, report.Image.NonEmptyLayers)
	}
	if len(report.TopLayers) != 2 || report.TopLayers[0].Size != 400_000_000 {
		t.Errorf("топ слоёв построен неверно: %+v", report.TopLayers)
	}
	if report.GeneratedAt.IsZero() {
		t.Error("GeneratedAt не заполнен")
	}
}

func TestHighestFindingSeverity(t *testing.T) {
	tests := []struct {
		name     string
		findings []Finding
		want     Severity
		wantOK   bool
	}{
		{name: "без находок", want: "", wantOK: false},
		{
			name:     "самая критичная находка",
			findings: []Finding{{Severity: SeverityLow}, {Severity: SeverityHigh}, {Severity: SeverityInfo}},
			want:     SeverityHigh,
			wantOK:   true,
		},
		{
			name:     "единственная находка",
			findings: []Finding{{Severity: SeverityMedium}},
			want:     SeverityMedium,
			wantOK:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := &Report{Findings: test.findings}
			got, ok := report.HighestFindingSeverity()
			if ok != test.wantOK {
				t.Fatalf("ok = %v, ожидалось %v", ok, test.wantOK)
			}
			if got != test.want {
				t.Errorf("severity = %q, ожидалось %q", got, test.want)
			}
		})
	}
}
