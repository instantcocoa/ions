package orchestrator

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed dindwrapper.sh
var dindWrapperScript []byte

// dindWrapperPath ensures the wrapper script is present on disk at a
// stable location under the ions state dir, and returns that path. The
// wrapper is embedded in the ions binary so callers don't need to carry
// it around alongside the CLI. Mounting this path read-only as
// /usr/local/bin/docker inside the job container lets DinD scripts
// issue `docker run -v /__w/...` calls that would otherwise fail —
// the wrapper translates in-container `/__w` paths to their host
// equivalents before forwarding to the real docker CLI.
func dindWrapperPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ions", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "dind-wrapper.sh")

	// Always write — content is small and keeps the file in sync with
	// the ions binary it shipped with.
	if err := os.WriteFile(path, dindWrapperScript, 0o755); err != nil {
		return "", fmt.Errorf("writing dind wrapper: %w", err)
	}
	return path, nil
}
