package trivy

import (
	"fmt"
	"strings"
)

const (
	// DefaultCacheVolume — том, в котором сохраняется база уязвимостей Trivy.
	DefaultCacheVolume = "cio-trivy-cache"

	// DisableCacheVolume — значение флага --trivy-cache, отключающее кэш базы.
	DisableCacheVolume = "none"

	// containerCachePath — путь к кэшу Trivy внутри контейнера.
	containerCachePath = "/root/.cache/trivy"

	// containerSocketPath — путь к сокету Docker внутри контейнера.
	containerSocketPath = "/var/run/docker.sock"

	// windowsSocketPath — сокет Docker Desktop для Windows, доступный контейнеру.
	windowsSocketPath = "//var/run/docker.sock"

	// unixSocketPath — сокет демона в Linux и macOS.
	unixSocketPath = "/var/run/docker.sock"
)

// allowedImageSources — источники образов, поддерживаемые флагом --image-src.
var allowedImageSources = []string{"docker", "containerd", "podman", "remote"}

// SocketPath возвращает путь к сокету демона на хосте, который можно смонтировать
// в контейнер со сканером. Пустая строка означает, что монтировать нечего:
// DOCKER_HOST указывает на удалённый демон (tcp, ssh), а не на локальный сокет.
func SocketPath(dockerHost, goos string) string {
	switch {
	case strings.HasPrefix(dockerHost, "unix://"):
		return strings.TrimPrefix(dockerHost, "unix://")
	case strings.HasPrefix(dockerHost, "npipe://"):
		// Docker Desktop для Windows: сокет доступен по стандартному пути.
		return windowsSocketPath
	case dockerHost != "":
		return ""
	case goos == "windows":
		return windowsSocketPath
	default:
		return unixSocketPath
	}
}

// ValidateImageSrc проверяет значение флага --image-src до запуска сканера:
// опечатка в источнике иначе превратится в невнятную ошибку Trivy.
func ValidateImageSrc(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	for _, item := range strings.Split(value, ",") {
		source := strings.TrimSpace(item)
		if !contains(allowedImageSources, source) {
			return fmt.Errorf("неизвестный источник образов %q (допустимы: %s)",
				source, strings.Join(allowedImageSources, ", "))
		}
	}
	return nil
}

// dockerRunArgs собирает аргументы docker run для запуска сканера в контейнере.
// Сокет демона и кэш базы монтируются томами, а переменные окружения Trivy
// передаются по имени — так креды реестра не попадают в командную строку.
func dockerRunArgs(opts Options, scanner []string) []string {
	args := []string{"run", "--rm"}
	if opts.CacheVolume != "" && opts.CacheVolume != DisableCacheVolume {
		args = append(args, "-v", opts.CacheVolume+":"+containerCachePath)
	}
	if opts.DockerSocket != "" {
		args = append(args, "-v", opts.DockerSocket+":"+containerSocketPath)
	}
	for _, name := range opts.EnvNames {
		if _, ok := opts.LookupEnv(name); ok {
			args = append(args, "-e", name)
		}
	}
	return append(append(args, opts.FallbackImage), scanner...)
}

// contains проверяет наличие значения в списке.
func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
