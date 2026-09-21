package bytesize

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseSize(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int64
		wantErr bool
	}{
		{name: "байты без единицы", value: "1024", want: 1024},
		{name: "килобайты", value: "10KB", want: 10_000},
		{name: "кибибайты в нижнем регистре", value: "1kib", want: 1024},
		{name: "мегабайты с пробелом", value: "10 MB", want: 10_000_000},
		{name: "дробные гигабайты", value: "1.5GB", want: 1_500_000_000},
		{name: "гибибайты", value: "5GiB", want: 5 * 1024 * 1024 * 1024},
		{name: "пустое значение", value: "", wantErr: true},
		{name: "неизвестная единица", value: "10XB", wantErr: true},
		{name: "текст", value: "много", wantErr: true},
		{name: "двойной суффикс", value: "10MBMB", wantErr: true},
		{name: "отрицательное значение", value: "-1GB", wantErr: true},
		// Переполнение int64: без проверки границы получался отрицательный порог.
		{name: "переполнение int64", value: "999999999999999999999999", wantErr: true},
		{name: "переполнение в гигабайтах", value: "9999999999999GiB", wantErr: true},
		{name: "максимально допустимый размер", value: "9223372036GB", want: 9_223_372_036_000_000_000},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseSize(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ParseSize(%q): ожидалась ошибка, получено %d", test.value, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSize(%q): неожиданная ошибка: %v", test.value, err)
			}
			if got != test.want {
				t.Errorf("ParseSize(%q) = %d, ожидалось %d", test.value, got, test.want)
			}
		})
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{bytes: 0, want: "0 B"},
		{bytes: 999, want: "999 B"},
		{bytes: 1_000, want: "1.0 KB"},
		{bytes: 10_500, want: "10.5 KB"},
		{bytes: 1_000_000, want: "1.0 MB"},
		{bytes: 412_300_000, want: "412.3 MB"},
		{bytes: 5 * 1024 * 1024 * 1024, want: "5.4 GB"},
	}

	for _, test := range tests {
		if got := HumanSize(test.bytes); got != test.want {
			t.Errorf("HumanSize(%d) = %q, ожидалось %q", test.bytes, got, test.want)
		}
	}
}

// TestSizeYAML проверяет, что размер читается из конфига строкой и числом,
// а обратная запись остаётся человекочитаемой.
func TestSizeYAML(t *testing.T) {
	tests := []struct {
		name     string
		document string
		want     int64
		wantErr  bool
	}{
		{name: "строка с гигабайтами", document: "size: 5GB\n", want: 5_000_000_000},
		{name: "строка с гибибайтами", document: "size: 512MiB\n", want: 512 * 1024 * 1024},
		{name: "число байт", document: "size: 1024\n", want: 1024},
		{name: "некорректная строка", document: "size: много\n", wantErr: true},
		{name: "неизвестная единица", document: "size: 5ZB\n", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var holder struct {
				Size Size `yaml:"size"`
			}
			err := yaml.Unmarshal([]byte(test.document), &holder)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ожидалась ошибка разбора %q", test.document)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка разбора %q: %v", test.document, err)
			}
			if holder.Size.Bytes() != test.want {
				t.Errorf("размер = %d, ожидалось %d", holder.Size.Bytes(), test.want)
			}
		})
	}
}

func TestSizeMarshalYAML(t *testing.T) {
	holder := struct {
		Size Size `yaml:"size"`
	}{Size: Size(5_000_000_000)}

	out, err := yaml.Marshal(holder)
	if err != nil {
		t.Fatalf("не удалось сериализовать размер: %v", err)
	}
	if got, want := string(out), "size: 5.0 GB\n"; got != want {
		t.Errorf("сериализация = %q, ожидалось %q", got, want)
	}
}
