package render

import "strings"

// scanHint — подсказка по типовой причине сбоя Trivy: правило срабатывает,
// если текст ошибки содержит любой из ключевых фрагментов.
type scanHint struct {
	keywords []string
	hint     string
}

// scanHints перечислены в порядке проверки: от частых причин к редким.
var scanHints = []scanHint{
	{
		keywords: []string{"unauthorized", "authentication required", "not authorized"},
		hint: "реестр требует авторизации: задайте TRIVY_USERNAME и TRIVY_PASSWORD в окружении " +
			"(cio пробрасывает их в контейнер) или запустите локальный бинарь trivy после docker login",
	},
	{
		keywords: []string{"error getting credentials", "docker-credential-"},
		hint: "креды реестра лежат в credsStore (менеджер учётных данных ОС) и недоступны в контейнере: " +
			"задайте TRIVY_USERNAME и TRIVY_PASSWORD",
	},
	{
		keywords: []string{"failed to connect to the docker api", "docker socket", "podman socket"},
		hint: "контейнер Trivy не видит локальный демон: добавьте --trivy-docker-socket, " +
			"чтобы смонтировать сокет, либо сканируйте из реестра (--trivy-image-src remote с кредами)",
	},
	{
		keywords: []string{"not found in tar", "unable to get uncompressed layer"},
		hint: "локальная копия образа неполная — docker save отдаёт архив без слоёв: " +
			"перекачайте образ (docker pull) или сканируйте из реестра (--trivy-image-src remote)",
	},
	{
		keywords: []string{"x509:", "tls:", "certificate"},
		hint: "проблема с сертификатом реестра: добавьте корневой сертификат в контейнер " +
			"или используйте --trivy-arg=--insecure (только для доверенных сетей)",
	},
	{
		keywords: []string{"no such host", "connection refused", "dial tcp", "i/o timeout", "network is unreachable"},
		hint:     "нет доступа к реестру или базе уязвимостей: проверьте DNS, прокси и сетевые правила",
	},
	{
		keywords: []string{"vulnerability db", "db error", "failed to download"},
		hint: "не удалось получить базу уязвимостей: проверьте доступ к ghcr.io; " +
			"кэш базы сохраняется в томе --trivy-cache",
	},
	{
		keywords: []string{"context deadline exceeded", "signal: killed", "timed out", "timeout"},
		hint:     "сканирование не уложилось в лимит времени: увеличьте --trivy-timeout",
	},
}

// hintForScanError подбирает подсказку по тексту ошибки сканера.
func hintForScanError(message string) string {
	lowered := strings.ToLower(message)
	for _, rule := range scanHints {
		for _, keyword := range rule.keywords {
			if strings.Contains(lowered, keyword) {
				return rule.hint
			}
		}
	}
	return ""
}
