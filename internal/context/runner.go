package context

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/emaland/ions/internal/expression"
)

// RunnerContext builds the "runner" context object.
func RunnerContext() expression.Value {
	osName := mapOS(runtime.GOOS)
	archName := mapArch(runtime.GOARCH)

	tempDir := os.TempDir()
	workspace, _ := os.Getwd()

	toolCache := ""
	home, err := os.UserHomeDir()
	if err == nil {
		toolCache = filepath.Join(home, ".ions", "tool-cache")
	}

	fields := map[string]expression.Value{
		"os":         expression.String(osName),
		"arch":       expression.String(archName),
		"name":       expression.String("ions-local"),
		"temp":       expression.String(tempDir),
		"tool_cache": expression.String(toolCache),
		"workspace":  expression.String(workspace),
	}

	return expression.Object(fields)
}

// mapOS maps Go's runtime.GOOS to the GitHub Actions runner OS names.
func mapOS(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	default:
		return goos
	}
}

// mapArch maps Go's runtime.GOARCH to the GitHub Actions runner architecture names.
func mapArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "X64"
	case "arm64":
		return "ARM64"
	default:
		return goarch
	}
}
