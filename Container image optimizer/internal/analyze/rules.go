package analyze

import (
	"fmt"
	"sort"
	"strings"
)

// AnalyzeLayers формирует замечания по истории сборки образа.
func AnalyzeLayers(layers []Layer, opts Options) []Finding {
	findings := make([]Finding, 0, len(layers))
	copyCommands := 0

	for _, layer := range layers {
		command := normalizeCommand(layer.CreatedBy)
		if command == "" || command == "<missing>" {
			continue
		}
		if isCopyCommand(command) {
			copyCommands++
		}
		findings = append(findings, checkLayer(layer, command, opts)...)
	}

	if copyCommands >= multiCopyMinCommands {
		findings = append(findings, Finding{
			Rule:           RuleManyCopyLayers,
			Severity:       SeverityInfo,
			LayerIndex:     -1,
			Message:        fmt.Sprintf("найдено %d команд COPY — каждая создаёт отдельный слой", copyCommands),
			Recommendation: "объедините копирование в один слой или используйте multi-stage сборку, чтобы сократить число слоёв",
		})
	}

	sortFindings(findings)
	return findings
}

// checkLayer проверяет одну команду истории сборки.
func checkLayer(layer Layer, command string, opts Options) []Finding {
	findings := sizeFindings(layer, opts)
	findings = append(findings, packageManagerFindings(layer, command)...)
	findings = append(findings, securityFindings(layer, command)...)
	findings = append(findings, buildFindings(layer, command)...)
	return findings
}

// sizeFindings оценивает размер слоя.
func sizeFindings(layer Layer, opts Options) []Finding {
	switch {
	case opts.HugeLayerSize > 0 && layer.Size >= opts.HugeLayerSize:
		return []Finding{newFinding(RuleHugeLayer, SeverityHigh, layer,
			fmt.Sprintf("слой занимает %s — больше порога %s", layer.SizeHuman, HumanSize(opts.HugeLayerSize)),
			"перенесите сборку артефактов в multi-stage: в финальный образ должны попадать только готовые артефакты")}
	case opts.MinLayerSize > 0 && layer.Size >= opts.MinLayerSize:
		return []Finding{newFinding(RuleLargeLayer, SeverityMedium, layer,
			fmt.Sprintf("крупный слой: %s (порог %s)", layer.SizeHuman, HumanSize(opts.MinLayerSize)),
			"проверьте, что в слой не попадают кэши пакетных менеджеров, исходники и артефакты сборки")}
	default:
		return nil
	}
}

func newFinding(rule string, severity Severity, layer Layer, message, recommendation string) Finding {
	return Finding{
		Rule:           rule,
		Severity:       severity,
		LayerIndex:     layer.Index,
		LayerID:        layer.ShortID,
		Message:        message,
		Recommendation: recommendation,
	}
}

func normalizeCommand(createdBy string) string {
	return strings.Join(strings.Fields(strings.ToLower(createdBy)), " ")
}

func isCopyCommand(command string) bool {
	return strings.HasPrefix(command, "copy ") ||
		strings.HasPrefix(command, "#(nop) copy ") ||
		strings.Contains(command, " copy ")
}

func containsAny(value string, substrings ...string) bool {
	for _, substring := range substrings {
		if strings.Contains(value, substring) {
			return true
		}
	}
	return false
}

func firstMatch(value string, candidates []string) string {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return candidate
		}
	}
	return ""
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		left, right := findings[i], findings[j]
		if left.Severity.Rank() != right.Severity.Rank() {
			return left.Severity.Rank() > right.Severity.Rank()
		}
		if left.LayerIndex != right.LayerIndex {
			return left.LayerIndex < right.LayerIndex
		}
		return left.Rule < right.Rule
	})
}
