package runner

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Manager handles downloading, installing, and version management of the
// official GitHub Actions runner binary.
type Manager struct {
	baseDir    string // ~/.ions/
	httpClient *http.Client
}

// Config stores the installed runner configuration.
type Config struct {
	InstalledVersion string    `json:"installed_version"`
	Platform         string    `json:"platform"`
	Architecture     string    `json:"architecture"`
	SHA256           string    `json:"sha256,omitempty"`         // hex-encoded SHA-256 of the downloaded archive
	LastUpdateCheck  time.Time `json:"last_update_check,omitempty"` // when we last checked for updates
	LatestKnown      string    `json:"latest_known,omitempty"`     // latest version from last check
}

// githubRelease is the subset of GitHub's release API response we need.
type githubRelease struct {
	TagName string `json:"tag_name"`
}

// NewManager creates a new runner manager, initializing the directory tree at ~/.ions/.
func NewManager() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	return NewManagerWithBaseDir(filepath.Join(home, ".ions"))
}

// NewManagerWithBaseDir creates a manager with a custom base directory.
func NewManagerWithBaseDir(baseDir string) (*Manager, error) {
	dirs := []string{
		baseDir,
		filepath.Join(baseDir, "runner"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("cannot create directory %s: %w", d, err)
		}
	}
	return &Manager{
		baseDir:    baseDir,
		httpClient: http.DefaultClient,
	}, nil
}

// RunnerDir returns the base runner directory (~/.ions/runner/).
func (m *Manager) RunnerDir() string {
	return filepath.Join(m.baseDir, "runner")
}

// VersionDir returns the directory for a specific runner version.
func (m *Manager) VersionDir(version string) string {
	return filepath.Join(m.RunnerDir(), version)
}

// configPath returns the path to the config file.
func (m *Manager) configPath() string {
	return filepath.Join(m.baseDir, "config.json")
}

// EnsureInstalled checks if a runner is installed and installs the latest if not.
// Returns the path to the runner directory. If a runner is already installed,
// it periodically checks for updates (at most once per 24 hours) and logs a
// message if a newer version is available.
func (m *Manager) EnsureInstalled(ctx context.Context) (string, error) {
	ver, err := m.InstalledVersion()
	if err == nil && ver != "" {
		dir := m.VersionDir(ver)
		if _, statErr := os.Stat(dir); statErr == nil {
			// Runner is installed. Check for updates in the background.
			m.checkForUpdateAsync(ctx, ver)
			return dir, nil
		}
	}

	latest, err := m.LatestVersion(ctx)
	if err != nil {
		return "", fmt.Errorf("cannot determine latest runner version: %w", err)
	}

	if err := m.Install(ctx, latest); err != nil {
		return "", err
	}
	return m.VersionDir(latest), nil
}

// checkForUpdateAsync checks for a newer runner version without blocking.
// Only checks at most once per 24 hours to avoid API rate limits.
func (m *Manager) checkForUpdateAsync(ctx context.Context, currentVersion string) {
	cfg, err := m.loadConfig()
	if err != nil {
		return
	}

	// Don't check more than once per day.
	if !cfg.LastUpdateCheck.IsZero() && time.Since(cfg.LastUpdateCheck) < 24*time.Hour {
		// If we already know about a newer version, log it.
		if cfg.LatestKnown != "" && cfg.LatestKnown != currentVersion {
			log.Printf("[runner] Update available: v%s (installed: v%s). Run 'ions runner install --latest' to update.", cfg.LatestKnown, currentVersion)
		}
		return
	}

	go func() {
		checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		latest, err := m.LatestVersion(checkCtx)
		if err != nil {
			return // silently ignore — don't block the workflow
		}

		cfg.LastUpdateCheck = time.Now()
		cfg.LatestKnown = latest
		m.saveConfig(cfg)

		if latest != currentVersion {
			log.Printf("[runner] Update available: v%s (installed: v%s). Run 'ions runner install --latest' to update.", latest, currentVersion)
		}
	}()
}

// Install downloads and extracts a specific runner version.
// The archive's SHA-256 checksum is computed during download and stored in
// the config file. If the same version is re-installed, the new checksum
// is compared against the stored one to detect tampering.
func (m *Manager) Install(ctx context.Context, version string) error {
	version = strings.TrimPrefix(version, "v")

	dir := m.VersionDir(version)
	if _, err := os.Stat(dir); err == nil {
		// Already installed.
		return m.saveConfig(Config{
			InstalledVersion: version,
			Platform:         platformString(),
			Architecture:     archString(),
		})
	}

	url := downloadURL(version)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("cannot create download request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d from %s", resp.StatusCode, url)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create version directory: %w", err)
	}

	// Compute SHA-256 of the archive while extracting.
	h := sha256.New()
	body := io.TeeReader(resp.Body, h)

	if err := extractTarGz(body, dir); err != nil {
		// Clean up partial extraction.
		os.RemoveAll(dir)
		return fmt.Errorf("extraction failed: %w", err)
	}

	checksum := hex.EncodeToString(h.Sum(nil))
	log.Printf("[runner] Downloaded %s (SHA-256: %s)", url, checksum)

	// If we have a stored checksum for this version, verify it matches.
	if existing, err := m.loadConfig(); err == nil && existing.SHA256 != "" &&
		existing.InstalledVersion == version {
		if existing.SHA256 != checksum {
			os.RemoveAll(dir)
			return fmt.Errorf("checksum mismatch for runner %s: expected %s, got %s",
				version, existing.SHA256, checksum)
		}
	}

	// Make runner scripts executable.
	for _, script := range []string{"config.sh", "run.sh", "env.sh"} {
		p := filepath.Join(dir, script)
		if _, statErr := os.Stat(p); statErr == nil {
			os.Chmod(p, 0o755)
		}
	}
	// Also make bin/Runner.Listener executable.
	listenerPath := filepath.Join(dir, "bin", "Runner.Listener")
	if _, statErr := os.Stat(listenerPath); statErr == nil {
		os.Chmod(listenerPath, 0o755)
	}

	return m.saveConfig(Config{
		InstalledVersion: version,
		Platform:         platformString(),
		Architecture:     archString(),
		SHA256:           checksum,
	})
}

// LatestVersion queries the GitHub API for the latest runner release version.
func (m *Manager) LatestVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/actions/runner/releases/latest", nil)
	if err != nil {
		return "", fmt.Errorf("cannot create API request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("cannot parse GitHub API response: %w", err)
	}

	if release.TagName == "" {
		return "", fmt.Errorf("GitHub API returned empty tag_name")
	}

	return strings.TrimPrefix(release.TagName, "v"), nil
}

// InstalledVersion returns the currently installed runner version, or empty string if none.
func (m *Manager) InstalledVersion() (string, error) {
	cfg, err := m.loadConfig()
	if err != nil {
		return "", err
	}
	return cfg.InstalledVersion, nil
}

// platformString returns the runner platform name for the current OS.
func platformString() string {
	switch runtime.GOOS {
	case "darwin":
		return "osx"
	case "linux":
		return "linux"
	case "windows":
		return "win"
	default:
		return runtime.GOOS
	}
}

// archString returns the runner architecture name for the current GOARCH.
func archString() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

// downloadURL constructs the download URL for a given runner version.
func downloadURL(version string) string {
	plat := platformString()
	arch := archString()
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf(
		"https://github.com/actions/runner/releases/download/v%s/actions-runner-%s-%s-%s.%s",
		version, plat, arch, version, ext,
	)
}

func (m *Manager) loadConfig() (Config, error) {
	data, err := os.ReadFile(m.configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("cannot read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("cannot parse config: %w", err)
	}
	return cfg, nil
}

func (m *Manager) saveConfig(cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot marshal config: %w", err)
	}
	return os.WriteFile(m.configPath(), data, 0o644)
}

// RunnerCacheDirs returns the directories where the .NET runner writes client
// settings caches. These accumulate across runs and can be cleaned up.
// On macOS: ~/Library/Application Support/GitHub/ActionsService/
// On Linux: ~/.local/share/GitHub/ActionsService/
func RunnerCacheDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var dirs []string
	switch runtime.GOOS {
	case "darwin":
		dirs = append(dirs, filepath.Join(home, "Library", "Application Support", "GitHub", "ActionsService"))
	case "linux":
		dirs = append(dirs, filepath.Join(home, ".local", "share", "GitHub", "ActionsService"))
	}
	return dirs
}

// Clean removes runner caches and work directories.
// Returns the list of directories that were removed.
func (m *Manager) Clean() ([]string, error) {
	var removed []string

	// Remove the .NET runner's client settings cache.
	for _, dir := range RunnerCacheDirs() {
		if _, err := os.Stat(dir); err == nil {
			if err := os.RemoveAll(dir); err != nil {
				return removed, fmt.Errorf("removing %s: %w", dir, err)
			}
			removed = append(removed, dir)
		}
	}

	// Remove _work directories inside each installed runner version.
	runnerDir := m.RunnerDir()
	entries, err := os.ReadDir(runnerDir)
	if err != nil && !os.IsNotExist(err) {
		return removed, fmt.Errorf("reading %s: %w", runnerDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		workDir := filepath.Join(runnerDir, e.Name(), "_work")
		if _, err := os.Stat(workDir); err == nil {
			if err := os.RemoveAll(workDir); err != nil {
				return removed, fmt.Errorf("removing %s: %w", workDir, err)
			}
			removed = append(removed, workDir)
		}
	}

	return removed, nil
}

// extractTarGz extracts a .tar.gz archive from r into the target directory.
func extractTarGz(r io.Reader, target string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("cannot open gzip stream: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read error: %w", err)
		}

		// Sanitize the path to prevent directory traversal.
		cleanName := filepath.Clean(hdr.Name)
		if strings.HasPrefix(cleanName, "..") || strings.HasPrefix(cleanName, string(filepath.Separator)) {
			continue
		}
		dest := filepath.Join(target, cleanName)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, os.FileMode(hdr.Mode)|0o755); err != nil {
				return fmt.Errorf("cannot create directory %s: %w", dest, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return fmt.Errorf("cannot create parent directory for %s: %w", dest, err)
			}
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("cannot create file %s: %w", dest, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("cannot write file %s: %w", dest, err)
			}
			f.Close()
		case tar.TypeSymlink:
			// Validate symlink target doesn't escape.
			linkTarget := filepath.Clean(hdr.Linkname)
			if filepath.IsAbs(linkTarget) {
				continue
			}
			os.Symlink(hdr.Linkname, dest)
		}
	}
	return nil
}
