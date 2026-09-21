package analyze

import "testing"

// FuzzParseSize проверяет разбор размеров слоёв: значения приходят из флагов CLI,
// поэтому на любом вводе функция обязана вернуть либо ошибку, либо неотрицательное
// число, пригодное для порогов правил анализа.
func FuzzParseSize(f *testing.F) {
	seeds := []string{
		"",
		"   ",
		"0",
		"1024",
		"512K",
		"10MB",
		"1.5GiB",
		"10XB",
		"-1",
		"1e6",
		"999999999999999999999999",
		"10 MB",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		size, err := ParseSize(value)
		if err != nil {
			if size != 0 {
				t.Errorf("при ошибке размер = %d, ожидался 0", size)
			}
			return
		}
		if size < 0 {
			t.Errorf("отрицательный размер %d для %q", size, value)
		}
		// Форматирование результата тоже не должно падать на граничных значениях.
		if text := HumanSize(size); text == "" {
			t.Errorf("HumanSize вернул пустую строку для %d", size)
		}
	})
}
