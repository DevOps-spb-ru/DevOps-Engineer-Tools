package analyze

import (
	"strings"
	"testing"
)

// ruleCase — параметры одного сценария проверки правил.
type ruleCase struct {
	name        string
	command     string
	size        int64
	opts        Options
	wantRules   []string
	unwantRules []string
}

// defaultOptions — пороги по умолчанию для тестов правил.
func defaultOptions() Options {
	return Options{Top: DefaultTopLayers, MinLayerSize: DefaultMinLayerSize, HugeLayerSize: DefaultHugeLayerSize}
}

func layerWithCommand(command string, size int64) []Layer {
	return EnrichLayers([]HistoryLayer{{
		ID:        "sha256:" + strings.Repeat("a", 64),
		CreatedBy: command,
		Size:      size,
	}})
}

func ruleNames(findings []Finding) []string {
	names := make([]string, 0, len(findings))
	for _, finding := range findings {
		names = append(names, finding.Rule)
	}
	return names
}

func runRuleCases(t *testing.T, tests []ruleCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings := AnalyzeLayers(layerWithCommand(test.command, test.size), test.opts)
			present := map[string]bool{}
			for _, finding := range findings {
				present[finding.Rule] = true
			}
			for _, rule := range test.wantRules {
				if !present[rule] {
					t.Errorf("ожидалось правило %q, получено %v", rule, ruleNames(findings))
				}
			}
			for _, rule := range test.unwantRules {
				if present[rule] {
					t.Errorf("правило %q не ожидалось, получено %v", rule, ruleNames(findings))
				}
			}
		})
	}
}

func TestAnalyzeLayersRules(t *testing.T) {
	const mediumSize = 30 * 1000 * 1000

	runRuleCases(t, []ruleCase{
		{
			name:        "apt-get install без очистки индекса и без --no-install-recommends",
			command:     "/bin/sh -c apt-get update && apt-get install -y curl",
			size:        mediumSize,
			opts:        defaultOptions(),
			wantRules:   []string{RuleLargeLayer, RuleAptListsNotCleaned, RuleAptNoRecommends},
			unwantRules: []string{RuleAptUpgrade, RuleSecretInLayer},
		},
		{
			name:        "apt-get install с очисткой индекса",
			command:     "/bin/sh -c apt-get update && apt-get install -y --no-install-recommends curl && rm -rf /var/lib/apt/lists/*",
			size:        mediumSize,
			opts:        defaultOptions(),
			wantRules:   []string{RuleLargeLayer},
			unwantRules: []string{RuleAptListsNotCleaned, RuleAptNoRecommends},
		},
		{
			name:        "обновление пакетов внутри образа",
			command:     "/bin/sh -c apt-get upgrade -y",
			size:        100,
			opts:        defaultOptions(),
			wantRules:   []string{RuleAptUpgrade},
			unwantRules: []string{RuleHugeLayer, RuleLargeLayer},
		},
		{
			name:        "очень крупный слой",
			command:     "/bin/sh -c #(nop) ADD file:abc in / ",
			size:        150 * 1000 * 1000,
			opts:        defaultOptions(),
			wantRules:   []string{RuleHugeLayer},
			unwantRules: []string{RuleLargeLayer},
		},
		{
			name:        "apk без --no-cache",
			command:     "/bin/sh -c apk add curl",
			size:        100,
			opts:        defaultOptions(),
			wantRules:   []string{RuleApkCacheNotCleaned},
			unwantRules: []string{RuleAptListsNotCleaned},
		},
		{
			name:        "apk с --no-cache",
			command:     "/bin/sh -c apk add --no-cache curl",
			size:        100,
			opts:        defaultOptions(),
			wantRules:   []string{},
			unwantRules: []string{RuleApkCacheNotCleaned, RuleLargeLayer},
		},
		{
			name:        "pip без --no-cache-dir",
			command:     "/bin/sh -c pip install requests",
			size:        100,
			opts:        defaultOptions(),
			wantRules:   []string{RulePipCacheNotCleaned},
			unwantRules: []string{RuleNpmCacheNotCleaned},
		},
		{
			name:        "npm install без очистки кэша",
			command:     "/bin/sh -c npm install --production",
			size:        100,
			opts:        defaultOptions(),
			wantRules:   []string{RuleNpmCacheNotCleaned},
			unwantRules: []string{RulePipCacheNotCleaned},
		},
	})
}
