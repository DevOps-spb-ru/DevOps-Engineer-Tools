package analyze

import "testing"

func TestParseSeverity(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		want      Severity
		wantSet   bool
		wantError bool
	}{
		{name: "пустое значение означает отсутствие порога", value: "", wantSet: false},
		{name: "пробелы игнорируются", value: "  ", wantSet: false},
		{name: "уровень в нижнем регистре", value: "high", want: SeverityHigh, wantSet: true},
		{name: "уровень в верхнем регистре", value: "CRITICAL", want: SeverityCritical, wantSet: true},
		{name: "неизвестный уровень", value: "urgent", wantError: true},
		{name: "unknown не является порогом", value: "unknown", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, set, err := ParseSeverity(test.value)
			if test.wantError {
				if err == nil {
					t.Fatalf("ParseSeverity(%q): ожидалась ошибка", test.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSeverity(%q): неожиданная ошибка: %v", test.value, err)
			}
			if set != test.wantSet {
				t.Fatalf("ParseSeverity(%q): set = %v, ожидалось %v", test.value, set, test.wantSet)
			}
			if got != test.want {
				t.Errorf("ParseSeverity(%q) = %q, ожидалось %q", test.value, got, test.want)
			}
		})
	}
}

func TestSeverityAtLeast(t *testing.T) {
	tests := []struct {
		severity  Severity
		threshold Severity
		want      bool
	}{
		{severity: SeverityCritical, threshold: SeverityHigh, want: true},
		{severity: SeverityHigh, threshold: SeverityHigh, want: true},
		{severity: SeverityMedium, threshold: SeverityHigh, want: false},
		{severity: SeverityInfo, threshold: SeverityInfo, want: true},
		{severity: SeverityUnknown, threshold: SeverityInfo, want: false},
		{severity: SeverityHigh, threshold: "", want: false},
	}

	for _, test := range tests {
		if got := test.severity.AtLeast(test.threshold); got != test.want {
			t.Errorf("%q.AtLeast(%q) = %v, ожидалось %v",
				test.severity, test.threshold, got, test.want)
		}
	}
}

func TestSeverityOrderStartsFromCritical(t *testing.T) {
	order := SeverityOrder()
	if len(order) == 0 {
		t.Fatal("SeverityOrder вернул пустой список")
	}
	if order[0] != SeverityCritical {
		t.Errorf("первый уровень = %q, ожидался %q", order[0], SeverityCritical)
	}
	for index := 1; index < len(order); index++ {
		if order[index-1].Rank() < order[index].Rank() {
			t.Errorf("порядок уровней нарушен: %q идёт перед %q", order[index-1], order[index])
		}
	}
}
