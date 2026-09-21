package render

import (
	"encoding/json"
	"io"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/analyze"
)

// JSON печатает отчёт в машинночитаемом виде (для интеграций и CI).
func JSON(w io.Writer, report *analyze.Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(report)
}
