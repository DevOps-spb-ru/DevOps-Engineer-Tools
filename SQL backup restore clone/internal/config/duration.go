package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration — длительность, которая в YAML читается строкой ("30m", "12h").
// Стандартный time.Duration разбирается yaml.v3 только как число наносекунд:
// для конфига сервиса это неудобно и легко даёт ошибку в 10^9 раз.
type Duration time.Duration

// Duration возвращает значение в виде time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// String реализует fmt.Stringer.
func (d Duration) String() string { return time.Duration(d).String() }

// UnmarshalYAML разбирает длительность из строки: единицу измерения указывать
// обязательно, иначе значение вида "1800" читалось бы как 1800 наносекунд.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	text := strings.TrimSpace(value.Value)
	if text == "" {
		return errors.New("длительность не задана (примеры: 30s, 30m, 12h)")
	}
	parsed, err := time.ParseDuration(text)
	if err != nil {
		return fmt.Errorf("некорректная длительность %q: %w (примеры: 30s, 30m, 12h)", text, err)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalYAML печатает длительность строкой: конфиг и снапшот настроек остаются читаемыми.
func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }
