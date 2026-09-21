package analyze

import "strings"

// packageManagerFindings проверяет команды пакетных менеджеров: кэши, индексы, флаги.
func packageManagerFindings(layer Layer, command string) []Finding {
	findings := make([]Finding, 0, 2)

	if containsAny(command, "apt-get install", "apt install") {
		if !containsAny(command, "/var/lib/apt/lists", "apt-get clean", "apt clean") {
			findings = append(findings, newFinding(RuleAptListsNotCleaned, SeverityMedium, layer,
				"после apt-get install индекс пакетов не удалён",
				"добавьте в ту же команду RUN: rm -rf /var/lib/apt/lists/*"))
		}
		if !strings.Contains(command, "--no-install-recommends") {
			findings = append(findings, newFinding(RuleAptNoRecommends, SeverityLow, layer,
				"apt-get install вызван без --no-install-recommends",
				"добавьте --no-install-recommends, чтобы не тянуть лишние пакеты и уменьшить образ"))
		}
	}

	if containsAny(command, "apt-get upgrade", "apt-get dist-upgrade", "apt upgrade", "apt full-upgrade") {
		findings = append(findings, newFinding(RuleAptUpgrade, SeverityMedium, layer,
			"обновление пакетов внутри образа (apt upgrade) делает сборку невоспроизводимой",
			"обновляйте базовый образ вместо apt upgrade: это даёт повторяемые сборки и меньше слоёв"))
	}

	if containsAny(command, "apk add") && !containsAny(command, "--no-cache", "/var/cache/apk") {
		findings = append(findings, newFinding(RuleApkCacheNotCleaned, SeverityLow, layer,
			"apk add вызван без --no-cache — индекс пакетов остаётся в слое",
			"используйте apk add --no-cache или удаляйте /var/cache/apk/* в той же команде RUN"))
	}

	if containsAny(command, "yum install", "dnf install", "microdnf install") &&
		!containsAny(command, "clean all", "/var/cache/yum", "/var/cache/dnf") {
		findings = append(findings, newFinding(RuleYumCacheNotCleaned, SeverityLow, layer,
			"yum/dnf install выполнен без очистки кэша пакетов",
			"добавьте yum clean all (или rm -rf /var/cache/yum) в ту же команду RUN"))
	}

	if containsAny(command, "pip install", "pip3 install") &&
		!containsAny(command, "--no-cache-dir", "rm -rf /root/.cache") {
		findings = append(findings, newFinding(RulePipCacheNotCleaned, SeverityLow, layer,
			"pip install выполнен без --no-cache-dir — кэш колёс остаётся в слое",
			"добавьте --no-cache-dir к pip install"))
	}

	if containsAny(command, "npm install", "npm ci") &&
		!containsAny(command, "npm cache clean", "--no-cache") {
		findings = append(findings, newFinding(RuleNpmCacheNotCleaned, SeverityInfo, layer,
			"npm install не очищает кэш пакетов",
			"удаляйте кэш в той же команде RUN (npm cache clean --force) либо собирайте зависимости в отдельном stage"))
	}

	return findings
}
