package analyze

import (
	"sort"
	"strings"
)

// Пороги анализа по умолчанию.
const (
	// DefaultMinLayerSize — с этого размера слой считается крупным.
	DefaultMinLayerSize int64 = 10 * 1000 * 1000
	// DefaultHugeLayerSize — с этого размера слой считается очень крупным.
	DefaultHugeLayerSize int64 = 100 * 1000 * 1000
	// DefaultTopLayers — сколько слоёв показывать в топе по умолчанию.
	DefaultTopLayers = 10

	shortIDLength        = 12
	multiCopyMinCommands = 3
)

// Имена правил анализа.
const (
	RuleHugeLayer          = "layer-too-large"
	RuleLargeLayer         = "layer-large"
	RuleAptListsNotCleaned = "apt-lists-not-cleaned"
	RuleAptNoRecommends    = "apt-no-install-recommends"
	RuleAptUpgrade         = "apt-upgrade"
	RuleApkCacheNotCleaned = "apk-cache-not-cleaned"
	RuleYumCacheNotCleaned = "yum-cache-not-cleaned"
	RulePipCacheNotCleaned = "pip-cache-not-cleaned"
	RuleNpmCacheNotCleaned = "npm-cache-not-cleaned"
	RuleDownloadKept       = "downloaded-artifact-kept"
	RuleSecretInLayer      = "secret-in-layer"
	RuleBuildToolInImage   = "build-toolchain-in-final-image"
	RuleCurlPipeShell      = "curl-pipe-shell"
	RuleManyCopyLayers     = "many-copy-layers"
)

// secretKeywords — имена переменных, значения которых нельзя фиксировать в слое.
var secretKeywords = []string{
	"password", "passwd", "token", "secret", "api_key", "apikey", "private_key", "access_key",
}

// buildToolCommands — команды сборки, наличие которых намекает, что сборка идёт в финальном образе.
var buildToolCommands = []string{
	"go build", "go install", "mvn ", "gradle", "cargo build",
	"npm run build", "yarn build", "make ", "gcc ", "g++ ", "javac ", "dotnet build",
}

// EnrichLayers дополняет историю сборки вычисляемыми полями: индексом, коротким ID,
// человекочитаемым размером и накопительным размером.
func EnrichLayers(history []HistoryLayer) []Layer {
	layers := make([]Layer, 0, len(history))
	var cumulative int64
	for index, item := range history {
		cumulative += item.Size
		layers = append(layers, Layer{
			Index:          index,
			ID:             item.ID,
			ShortID:        ShortID(item.ID),
			Created:        item.Created,
			CreatedBy:      item.CreatedBy,
			Comment:        item.Comment,
			Size:           item.Size,
			SizeHuman:      HumanSize(item.Size),
			CumulativeSize: cumulative,
			Empty:          item.Size == 0,
		})
	}
	return layers
}

// ShortID укорачивает идентификатор до 12 символов, как это делает docker.
// Для слоёв без идентификатора (BuildKit отдаёт строку "<missing>") возвращается пустая строка.
func ShortID(id string) string {
	trimmed := strings.TrimPrefix(id, "sha256:")
	if trimmed == "<missing>" {
		return ""
	}
	if len(trimmed) <= shortIDLength {
		return trimmed
	}
	return trimmed[:shortIDLength]
}

// TopLayers возвращает не более n самых больших слоёв (по убыванию размера).
func TopLayers(layers []Layer, n int) []Layer {
	if n <= 0 || len(layers) == 0 {
		return nil
	}
	sorted := make([]Layer, len(layers))
	copy(sorted, layers)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Size > sorted[j].Size })
	if n < len(sorted) {
		sorted = sorted[:n]
	}
	return sorted
}
