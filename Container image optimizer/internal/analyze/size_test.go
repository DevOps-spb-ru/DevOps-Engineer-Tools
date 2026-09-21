package analyze

import "testing"

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
		{name: "пустое значение", value: "", wantErr: true},
		{name: "неизвестная единица", value: "10XB", wantErr: true},
		{name: "текст", value: "много", wantErr: true},
		{name: "двойной суффикс", value: "10MBMB", wantErr: true},
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
		{bytes: 2_000_000_000, want: "2.0 GB"},
	}

	for _, test := range tests {
		if got := HumanSize(test.bytes); got != test.want {
			t.Errorf("HumanSize(%d) = %q, ожидалось %q", test.bytes, got, test.want)
		}
	}
}
