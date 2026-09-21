package analyze

import "testing"

func TestAnalyzeLayersSecurityRules(t *testing.T) {
	runRuleCases(t, []ruleCase{
		{
			name:        "скачанный файл остаётся в слое",
			command:     "/bin/sh -c curl -o /tmp/app.tar.gz https://example.com/app.tar.gz",
			size:        100,
			opts:        defaultOptions(),
			wantRules:   []string{RuleDownloadKept},
			unwantRules: []string{RuleCurlPipeShell},
		},
		{
			name:        "выполнение скрипта из сети",
			command:     "/bin/sh -c curl -fsSL https://example.com/install.sh | bash",
			size:        100,
			opts:        defaultOptions(),
			wantRules:   []string{RuleCurlPipeShell},
			unwantRules: []string{RuleSecretInLayer},
		},
		{
			name:        "проверка контрольной суммы не считается запуском скрипта",
			command:     `/bin/sh -c wget -O app.tar.gz https://example.com/app.tar.gz && echo "abc *app.tar.gz" | sha256sum -c -`,
			size:        100,
			opts:        defaultOptions(),
			wantRules:   []string{},
			unwantRules: []string{RuleCurlPipeShell},
		},
		{
			name:        "секрет в переменной окружения",
			command:     `/bin/sh -c #(nop)  ENV DB_PASSWORD=supersecret`,
			size:        0,
			opts:        defaultOptions(),
			wantRules:   []string{RuleSecretInLayer},
			unwantRules: []string{RuleDownloadKept},
		},
		{
			name:        "артефакт сборки в финальном образе",
			command:     "/bin/sh -c go build -o /app/server ./cmd/server",
			size:        100,
			opts:        defaultOptions(),
			wantRules:   []string{RuleBuildToolInImage},
			unwantRules: []string{RuleApkCacheNotCleaned},
		},
		{
			name:        "команда истории недоступна",
			command:     "<missing>",
			size:        50 * 1000 * 1000,
			opts:        defaultOptions(),
			wantRules:   []string{},
			unwantRules: []string{RuleLargeLayer, RuleHugeLayer},
		},
		{
			name:        "пороги размера отключены",
			command:     "/bin/sh -c apk add --no-cache curl",
			size:        500 * 1000 * 1000,
			opts:        Options{},
			wantRules:   []string{},
			unwantRules: []string{RuleLargeLayer, RuleHugeLayer, RuleApkCacheNotCleaned},
		},
	})
}

func TestAnalyzeLayersManyCopyCommands(t *testing.T) {
	layers := EnrichLayers([]HistoryLayer{
		{ID: "1", CreatedBy: "/bin/sh -c #(nop) COPY file:abc in /app/config.yaml", Size: 1024},
		{ID: "2", CreatedBy: "/bin/sh -c #(nop) COPY file:def in /app/app.jar", Size: 2048},
		{ID: "3", CreatedBy: "/bin/sh -c #(nop) COPY file:ghi in /app/entrypoint.sh", Size: 512},
	})

	findings := AnalyzeLayers(layers, defaultOptions())
	if len(findings) != 1 {
		t.Fatalf("получено %d замечаний, ожидалось 1: %v", len(findings), ruleNames(findings))
	}
	if findings[0].Rule != RuleManyCopyLayers {
		t.Fatalf("правило = %q, ожидалось %q", findings[0].Rule, RuleManyCopyLayers)
	}
	if findings[0].LayerIndex != -1 {
		t.Errorf("LayerIndex = %d, ожидалось -1 (замечание касается образа целиком)", findings[0].LayerIndex)
	}
}

func TestAnalyzeLayersSortedBySeverity(t *testing.T) {
	layers := EnrichLayers([]HistoryLayer{
		{ID: "1", CreatedBy: "/bin/sh -c apk add curl", Size: 100},
		{ID: "2", CreatedBy: "/bin/sh -c #(nop) ADD file:abc in / ", Size: 150 * 1000 * 1000},
	})

	findings := AnalyzeLayers(layers, defaultOptions())
	if len(findings) < 2 {
		t.Fatalf("получено %d замечаний, ожидалось минимум 2", len(findings))
	}
	if findings[0].Severity != SeverityHigh {
		t.Errorf("первое замечание имеет уровень %q, ожидался %q", findings[0].Severity, SeverityHigh)
	}
	for index := 1; index < len(findings); index++ {
		if findings[index-1].Severity.Rank() < findings[index].Severity.Rank() {
			t.Fatalf("порядок замечаний нарушен: %v", ruleNames(findings))
		}
	}
}
