package analyze

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// sizePattern разбирает значения вида "10MB", "1.5GiB", "512K", "1024".
var sizePattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([a-zA-Z]*)$`)

// sizeUnits — поддерживаемые единицы измерения размера.
var sizeUnits = map[string]int64{
	"":    1,
	"B":   1,
	"K":   1000,
	"KB":  1000,
	"KIB": 1024,
	"M":   1000 * 1000,
	"MB":  1000 * 1000,
	"MIB": 1024 * 1024,
	"G":   1000 * 1000 * 1000,
	"GB":  1000 * 1000 * 1000,
	"GIB": 1024 * 1024 * 1024,
}

// ParseSize переводит человекочитаемый размер ("10MB", "1.5GiB", "1024") в байты.
func ParseSize(value string) (int64, error) {
	matches := sizePattern.FindStringSubmatch(strings.TrimSpace(value))
	if matches == nil {
		return 0, fmt.Errorf("некорректный размер %q (пример: 1024, 512KB, 10MB, 1.5GB)", value)
	}
	number, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, fmt.Errorf("некорректный размер %q: %w", value, err)
	}
	multiplier, ok := sizeUnits[strings.ToUpper(matches[2])]
	if !ok {
		return 0, fmt.Errorf(
			"неизвестная единица измерения в %q (поддерживаются B, KB, KiB, MB, MiB, GB, GiB)", value)
	}
	return int64(number * float64(multiplier)), nil
}

// HumanSize форматирует размер в удобочитаемый вид, например "412.3 MB".
func HumanSize(bytes int64) string {
	const (
		unit     = 1000
		decimals = "%.1f %s"
	)
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	value := float64(bytes)
	index := -1
	for value >= unit && index < len(units)-1 {
		value /= unit
		index++
	}
	return fmt.Sprintf(decimals, value, units[index])
}
