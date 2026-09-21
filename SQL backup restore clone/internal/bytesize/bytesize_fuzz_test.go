package bytesize

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// FuzzParseSize проверяет разбор порогов свободного места: значения приходят
// из конфига, поэтому на любом вводе функция обязана вернуть либо ошибку,
// либо неотрицательное число, пригодное для сравнения с местом на диске.
func FuzzParseSize(f *testing.F) {
	seeds := []string{
		"", "   ", "0", "1024", "512K", "10MB", "1.5GiB", "5 GiB", "10XB", "-1",
		"1e6", "999999999999999999999999", "10 MB", "много",
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

// FuzzSizeUnmarshalYAML проверяет, что разбор размера из YAML не паникует
// и не принимает некорректные значения молча.
func FuzzSizeUnmarshalYAML(f *testing.F) {
	seeds := []string{"5GB", "1024", "", "-5MB", "1.5GiB", "много", "0"}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		document := "size: " + value + "\n"
		var holder struct {
			Size Size `yaml:"size"`
		}
		if err := yaml.Unmarshal([]byte(document), &holder); err != nil {
			return
		}
		if holder.Size < 0 {
			t.Errorf("размер %d отрицательный при разборе %q", holder.Size, document)
		}
	})
}
