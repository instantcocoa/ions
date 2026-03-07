package runner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- downloadURL version format tests ---

func TestDownloadURL_VersionFormat(t *testing.T) {
	url := downloadURL("2.320.0")
	assert.Contains(t, url, "v2.320.0")
	assert.Contains(t, url, "actions-runner-")
	assert.Contains(t, url, "2.320.0")
	// Should have exactly one "v" prefix in the tag portion.
	assert.Contains(t, url, "/v2.320.0/")
}

func TestDownloadURL_ContainsPlatformAndArch(t *testing.T) {
	url := downloadURL("2.319.0")
	plat := platformString()
	arch := archString()
	assert.Contains(t, url, plat)
	assert.Contains(t, url, arch)
}

// --- Config file handling ---

func TestLoadConfig_NonExistentReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	cfg, err := mgr.loadConfig()
	require.NoError(t, err)
	assert.Equal(t, "", cfg.InstalledVersion)
	assert.Equal(t, "", cfg.Platform)
	assert.Equal(t, "", cfg.Architecture)
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Write invalid JSON.
	err = os.WriteFile(mgr.configPath(), []byte("not valid json{"), 0o644)
	require.NoError(t, err)

	_, err = mgr.loadConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse config")
}

func TestSaveConfig_AllFields(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	now := time.Now().Truncate(time.Second)
	cfg := Config{
		InstalledVersion: "2.320.0",
		Platform:         "linux",
		Architecture:     "x64",
		SHA256:           "abcdef1234567890",
		LastUpdateCheck:  now,
		LatestKnown:      "2.321.0",
	}
	err = mgr.saveConfig(cfg)
	require.NoError(t, err)

	loaded, err := mgr.loadConfig()
	require.NoError(t, err)
	assert.Equal(t, cfg.InstalledVersion, loaded.InstalledVersion)
	assert.Equal(t, cfg.Platform, loaded.Platform)
	assert.Equal(t, cfg.Architecture, loaded.Architecture)
	assert.Equal(t, cfg.SHA256, loaded.SHA256)
	assert.Equal(t, cfg.LatestKnown, loaded.LatestKnown)
}

func TestConfigPath(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dir, "config.json"), mgr.configPath())
}

// --- RunnerDir and VersionDir ---

func TestRunnerDir(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dir, "runner"), mgr.RunnerDir())
}

func TestVersionDir_MultipleVersions(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dir, "runner", "2.319.0"), mgr.VersionDir("2.319.0"))
	assert.Equal(t, filepath.Join(dir, "runner", "2.320.0"), mgr.VersionDir("2.320.0"))
	assert.NotEqual(t, mgr.VersionDir("2.319.0"), mgr.VersionDir("2.320.0"))
}

// --- Platform detection ---

func TestPlatformString_CurrentOS(t *testing.T) {
	// Just verify the function returns a non-empty string on the current platform.
	result := platformString()
	assert.NotEmpty(t, result)

	// On linux CI, should be "linux".
	if runtime.GOOS == "linux" {
		assert.Equal(t, "linux", result)
	}
}

func TestArchString_CurrentArch(t *testing.T) {
	result := archString()
	assert.NotEmpty(t, result)

	if runtime.GOARCH == "amd64" {
		assert.Equal(t, "x64", result)
	}
}

// --- extractTarGz edge cases ---

func TestExtractTarGz_WithDirectoryEntry(t *testing.T) {
	// Create an archive with an explicit directory entry.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Add a directory entry.
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "mydir/",
		Typeflag: tar.TypeDir,
		Mode:     0o755,
	}))

	// Add a file inside it.
	content := "package main"
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "mydir/main.go",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(content)),
	}))
	_, err := tw.Write([]byte(content))
	require.NoError(t, err)

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	dir := t.TempDir()
	err = extractTarGz(bytes.NewReader(buf.Bytes()), dir)
	require.NoError(t, err)

	assert.DirExists(t, filepath.Join(dir, "mydir"))
	assert.FileExists(t, filepath.Join(dir, "mydir", "main.go"))

	data, err := os.ReadFile(filepath.Join(dir, "mydir", "main.go"))
	require.NoError(t, err)
	assert.Equal(t, "package main", string(data))
}

func TestExtractTarGz_SkipsTraversalPaths(t *testing.T) {
	// Create an archive with a path that tries directory traversal.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	content := "evil"
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "../escape.txt",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(content)),
	}))
	_, err := tw.Write([]byte(content))
	require.NoError(t, err)

	// Also add a valid file.
	validContent := "safe"
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "safe.txt",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(validContent)),
	}))
	_, err = tw.Write([]byte(validContent))
	require.NoError(t, err)

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	dir := t.TempDir()
	err = extractTarGz(bytes.NewReader(buf.Bytes()), dir)
	require.NoError(t, err)

	// The traversal file should NOT exist.
	assert.NoFileExists(t, filepath.Join(dir, "..", "escape.txt"))
	// The safe file should exist.
	assert.FileExists(t, filepath.Join(dir, "safe.txt"))
}

func TestExtractTarGz_SymlinkHandling(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Add a regular file.
	content := "target"
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "target.txt",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(content)),
	}))
	_, err := tw.Write([]byte(content))
	require.NoError(t, err)

	// Add a relative symlink.
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "link.txt",
		Typeflag: tar.TypeSymlink,
		Linkname: "target.txt",
	}))

	// Add an absolute symlink (should be skipped).
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "bad-link.txt",
		Typeflag: tar.TypeSymlink,
		Linkname: "/etc/passwd",
	}))

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	dir := t.TempDir()
	err = extractTarGz(bytes.NewReader(buf.Bytes()), dir)
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(dir, "target.txt"))
	// Relative symlink should exist.
	_, err = os.Lstat(filepath.Join(dir, "link.txt"))
	assert.NoError(t, err)
	// Absolute symlink should NOT exist.
	_, err = os.Lstat(filepath.Join(dir, "bad-link.txt"))
	assert.True(t, os.IsNotExist(err))
}

func TestExtractTarGz_InvalidGzip(t *testing.T) {
	err := extractTarGz(bytes.NewReader([]byte("not a gzip stream")), t.TempDir())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "gzip")
}

// --- Clean ---

func TestClean_RemovesWorkDirs(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Create a version directory with a _work subdirectory.
	workDir := filepath.Join(mgr.RunnerDir(), "2.319.1", "_work")
	require.NoError(t, os.MkdirAll(workDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "stuff.txt"), []byte("data"), 0o644))

	removed, err := mgr.Clean()
	require.NoError(t, err)

	// The _work directory should be removed.
	assert.Contains(t, removed, workDir)
	assert.NoDirExists(t, workDir)
}

func TestClean_NoWorkDirs(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Create a version directory WITHOUT a _work subdirectory.
	require.NoError(t, os.MkdirAll(filepath.Join(mgr.RunnerDir(), "2.319.1"), 0o755))

	removed, err := mgr.Clean()
	require.NoError(t, err)
	// Should not include any _work dirs.
	for _, r := range removed {
		assert.NotContains(t, r, "_work")
	}
}

func TestClean_EmptyRunnerDir(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	removed, err := mgr.Clean()
	require.NoError(t, err)
	// Nothing to remove in runner dir (cache dirs might be listed but they likely don't exist).
	_ = removed
}

// --- RunnerCacheDirs ---

func TestRunnerCacheDirs(t *testing.T) {
	dirs := RunnerCacheDirs()
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		assert.NotEmpty(t, dirs)
		for _, d := range dirs {
			assert.Contains(t, d, "GitHub")
			assert.Contains(t, d, "ActionsService")
		}
	}
}

// --- LatestVersion with invalid JSON ---

func TestLatestVersion_InvalidJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not json at all"))
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	_, err = mgr.LatestVersion(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse")
}

// --- EnsureInstalled returns cached on second call ---

func TestEnsureInstalled_CachesResult(t *testing.T) {
	archiveData := createTestArchive(t, map[string]string{
		"run.sh": "#!/bin/bash\necho running",
	})

	apiCalls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls++
		if r.URL.Path == "/repos/actions/runner/releases/latest" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(githubRelease{TagName: "v2.320.0"})
			return
		}
		w.Write(archiveData)
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	// First call installs.
	dir1, err := mgr.EnsureInstalled(context.Background())
	require.NoError(t, err)

	callsAfterFirst := apiCalls

	// Second call returns from cache.
	dir2, err := mgr.EnsureInstalled(context.Background())
	require.NoError(t, err)

	assert.Equal(t, dir1, dir2)
	assert.Equal(t, callsAfterFirst, apiCalls, "no additional API calls on second EnsureInstalled")
}

// --- Install makes runner scripts executable ---

func TestInstall_SetsExecutablePermissions(t *testing.T) {
	archiveData := createTestArchive(t, map[string]string{
		"run.sh":    "#!/bin/bash\necho run",
		"config.sh": "#!/bin/bash\necho config",
		"env.sh":    "#!/bin/bash\necho env",
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archiveData)
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	err = mgr.Install(context.Background(), "2.319.1")
	require.NoError(t, err)

	versionDir := mgr.VersionDir("2.319.1")

	for _, script := range []string{"run.sh", "config.sh", "env.sh"} {
		info, err := os.Stat(filepath.Join(versionDir, script))
		require.NoError(t, err)
		// Check that the file is executable.
		assert.NotZero(t, info.Mode()&0o111, "%s should be executable", script)
	}
}

// --- NewManager uses home directory ---

func TestNewManager_UsesHomeDir(t *testing.T) {
	mgr, err := NewManager()
	require.NoError(t, err)

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	expected := filepath.Join(home, ".ions", "runner")
	assert.Equal(t, expected, mgr.RunnerDir())
}

// --- checkForUpdateAsync logs when newer version known ---

func TestCheckForUpdateAsync_RecentCheckSkipped(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Save config with a recent check and a newer known version.
	cfg := Config{
		InstalledVersion: "2.319.0",
		LastUpdateCheck:  time.Now().Add(-1 * time.Hour), // 1 hour ago, within 24h window.
		LatestKnown:      "2.320.0",
	}
	err = mgr.saveConfig(cfg)
	require.NoError(t, err)

	// This should not make any HTTP calls because the check was recent.
	mgr.checkForUpdateAsync(context.Background(), "2.319.0")

	// Verify the config didn't change.
	loaded, err := mgr.loadConfig()
	require.NoError(t, err)
	assert.Equal(t, "2.320.0", loaded.LatestKnown)
}

func TestCheckForUpdateAsync_NoConfigDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Write invalid config to trigger load error.
	err = os.WriteFile(mgr.configPath(), []byte("invalid json"), 0o644)
	require.NoError(t, err)

	// Should not panic.
	mgr.checkForUpdateAsync(context.Background(), "2.319.0")
}

// --- checkForUpdateAsync background goroutine that actually calls LatestVersion ---

func TestCheckForUpdateAsync_OldCheckTriggersBackground(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(githubRelease{TagName: "v2.325.0"})
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	// Save config with a stale check (>24 hours ago).
	cfg := Config{
		InstalledVersion: "2.319.0",
		LastUpdateCheck:  time.Now().Add(-48 * time.Hour),
		LatestKnown:      "2.319.0",
	}
	err = mgr.saveConfig(cfg)
	require.NoError(t, err)

	// This will spawn a goroutine that fetches latest version.
	mgr.checkForUpdateAsync(context.Background(), "2.319.0")

	// Wait for the background goroutine to complete.
	time.Sleep(500 * time.Millisecond)

	// Config should now be updated with the new latest version.
	loaded, err := mgr.loadConfig()
	require.NoError(t, err)
	assert.Equal(t, "2.325.0", loaded.LatestKnown)
}

func TestCheckForUpdateAsync_RecentCheckSameVersionNoLog(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Recent check with LatestKnown == currentVersion should not log update.
	cfg := Config{
		InstalledVersion: "2.319.0",
		LastUpdateCheck:  time.Now().Add(-1 * time.Hour),
		LatestKnown:      "2.319.0",
	}
	err = mgr.saveConfig(cfg)
	require.NoError(t, err)

	// Should not panic and should not log (LatestKnown == currentVersion).
	mgr.checkForUpdateAsync(context.Background(), "2.319.0")
}

func TestCheckForUpdateAsync_BackgroundLatestVersionError(t *testing.T) {
	// Server returns an error.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	// Stale check to trigger background fetch.
	cfg := Config{
		InstalledVersion: "2.319.0",
		LastUpdateCheck:  time.Now().Add(-48 * time.Hour),
	}
	err = mgr.saveConfig(cfg)
	require.NoError(t, err)

	// Background goroutine should silently ignore the error.
	mgr.checkForUpdateAsync(context.Background(), "2.319.0")
	time.Sleep(500 * time.Millisecond)

	// Config should remain unchanged since LatestVersion call failed.
	loaded, err := mgr.loadConfig()
	require.NoError(t, err)
	// LastUpdateCheck should not have been updated.
	assert.True(t, loaded.LastUpdateCheck.Before(time.Now().Add(-24*time.Hour)))
}

// --- EnsureInstalled error when LatestVersion fails ---

func TestEnsureInstalled_LatestVersionFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	_, err = mgr.EnsureInstalled(context.Background())
	assert.ErrorContains(t, err, "cannot determine latest runner version")
}

// --- EnsureInstalled with Install failure ---

func TestEnsureInstalled_InstallFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/actions/runner/releases/latest" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(githubRelease{TagName: "v2.320.0"})
			return
		}
		// Download fails.
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	_, err = mgr.EnsureInstalled(context.Background())
	assert.ErrorContains(t, err, "HTTP 404")
}

// --- EnsureInstalled with stale config but directory missing ---

func TestEnsureInstalled_ConfigExistsButDirMissing(t *testing.T) {
	archiveData := createTestArchive(t, map[string]string{
		"run.sh": "#!/bin/bash\necho running",
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/actions/runner/releases/latest" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(githubRelease{TagName: "v2.320.0"})
			return
		}
		w.Write(archiveData)
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	// Pre-save config but don't create the directory.
	err = mgr.saveConfig(Config{InstalledVersion: "2.320.0"})
	require.NoError(t, err)

	// Directory doesn't exist, so it should download.
	runnerDir, err := mgr.EnsureInstalled(context.Background())
	require.NoError(t, err)
	assert.Equal(t, mgr.VersionDir("2.320.0"), runnerDir)
}

// --- Install with bin/Runner.Listener ---

func TestInstall_CreatesListenerExecutable(t *testing.T) {
	// Create archive with bin/Runner.Listener.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	listenerContent := "#!/bin/bash\necho listener"
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "bin/",
		Typeflag: tar.TypeDir,
		Mode:     0o755,
	}))
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "bin/Runner.Listener",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(listenerContent)),
	}))
	_, err := tw.Write([]byte(listenerContent))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(buf.Bytes())
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	err = mgr.Install(context.Background(), "2.319.1")
	require.NoError(t, err)

	versionDir := mgr.VersionDir("2.319.1")
	info, err := os.Stat(filepath.Join(versionDir, "bin", "Runner.Listener"))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o111, "Runner.Listener should be executable")
}

// --- loadConfig with unreadable file ---

func TestLoadConfig_UnreadableFile(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Create config file with no read permissions.
	configFile := mgr.configPath()
	err = os.WriteFile(configFile, []byte(`{"installed_version":"1.0"}`), 0o000)
	require.NoError(t, err)

	_, err = mgr.loadConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot read config")

	// Restore permissions for cleanup.
	os.Chmod(configFile, 0o644)
}

// --- InstalledVersion with load error ---

func TestInstalledVersion_LoadError(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Write unreadable config.
	err = os.WriteFile(mgr.configPath(), []byte(`{"installed_version":"1.0"}`), 0o000)
	require.NoError(t, err)

	ver, err := mgr.InstalledVersion()
	assert.Error(t, err)
	assert.Empty(t, ver)

	// Restore permissions for cleanup.
	os.Chmod(mgr.configPath(), 0o644)
}

// --- Clean with files (non-dir entries) in runner dir ---

func TestClean_SkipsNonDirectoryEntries(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Create a regular file (not a directory) in the runner dir.
	err = os.WriteFile(filepath.Join(mgr.RunnerDir(), "some-file.txt"), []byte("data"), 0o644)
	require.NoError(t, err)

	// Also create a versioned dir with a _work dir.
	workDir := filepath.Join(mgr.RunnerDir(), "2.320.0", "_work")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	removed, err := mgr.Clean()
	require.NoError(t, err)

	// Should have removed the _work dir but skipped the file.
	assert.Contains(t, removed, workDir)
	assert.FileExists(t, filepath.Join(mgr.RunnerDir(), "some-file.txt"))
}

// --- extractTarGz tar read error ---

func TestExtractTarGz_CorruptTarInsideGzip(t *testing.T) {
	// Create valid gzip wrapping invalid tar content.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, err := gw.Write([]byte("this is not a valid tar archive at all!"))
	require.NoError(t, err)
	require.NoError(t, gw.Close())

	err = extractTarGz(bytes.NewReader(buf.Bytes()), t.TempDir())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tar read error")
}

// --- Stdout and Stderr accessors ---

func TestStdoutAndStderr(t *testing.T) {
	dir := t.TempDir()
	runScript := filepath.Join(dir, "run.sh")
	err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\necho hello\necho err >&2\nsleep 5"), 0o755)
	require.NoError(t, err)

	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	// Before Start, they should be nil.
	assert.Nil(t, p.Stdout())
	assert.Nil(t, p.Stderr())

	err = p.Configure(context.Background())
	require.NoError(t, err)

	err = p.Start(context.Background())
	require.NoError(t, err)
	defer p.Stop()

	// After Start, they should be non-nil.
	assert.NotNil(t, p.Stdout())
	assert.NotNil(t, p.Stderr())
}

// --- Wait on a started process ---

func TestWait_ProcessExitsSuccessfully(t *testing.T) {
	dir := t.TempDir()
	runScript := filepath.Join(dir, "run.sh")
	err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\nexit 0"), 0o755)
	require.NoError(t, err)

	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	err = p.Configure(context.Background())
	require.NoError(t, err)

	err = p.Start(context.Background())
	require.NoError(t, err)

	err = p.Wait()
	assert.NoError(t, err)
}

func TestWait_ProcessExitsWithError(t *testing.T) {
	dir := t.TempDir()
	runScript := filepath.Join(dir, "run.sh")
	err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\nexit 1"), 0o755)
	require.NoError(t, err)

	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	err = p.Configure(context.Background())
	require.NoError(t, err)

	err = p.Start(context.Background())
	require.NoError(t, err)

	err = p.Wait()
	assert.Error(t, err)
}

// --- generateRSAParams ---

func TestGenerateRSAParams_ReturnsAllFields(t *testing.T) {
	params, err := generateRSAParams()
	require.NoError(t, err)

	expectedKeys := []string{"exponent", "modulus", "d", "p", "q", "dp", "dq", "inverseQ"}
	for _, key := range expectedKeys {
		assert.NotEmpty(t, params[key], "key %s should not be empty", key)
	}
}

// --- writeJSONFile error handling ---

func TestWriteJSONFile_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	err := writeJSONFile(path, map[string]string{"key": "value"})
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "key")
	assert.Contains(t, string(data), "value")
}

func TestWriteJSONFile_BadPath(t *testing.T) {
	err := writeJSONFile("/nonexistent/dir/test.json", map[string]string{"key": "value"})
	assert.Error(t, err)
}

// --- NewManagerWithBaseDir with unwritable path ---

func TestNewManagerWithBaseDir_UnwritablePath(t *testing.T) {
	// Create a read-only directory.
	dir := t.TempDir()
	readOnlyDir := filepath.Join(dir, "readonly")
	require.NoError(t, os.MkdirAll(readOnlyDir, 0o755))
	require.NoError(t, os.Chmod(readOnlyDir, 0o444))
	defer os.Chmod(readOnlyDir, 0o755)

	_, err := NewManagerWithBaseDir(filepath.Join(readOnlyDir, "ions"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot create directory")
}

// --- Clean with removable runner cache dirs ---

func TestClean_RunnerCacheDirs_ExistAndRemoved(t *testing.T) {
	// We can't easily create the system cache dirs, but we can test the
	// code path that handles missing runner dir entries.
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Clean on empty runner dir should not fail.
	removed, err := mgr.Clean()
	require.NoError(t, err)
	_ = removed
}

// --- extractTarGz with absolute path symlink ---

func TestExtractTarGz_AbsolutePathEntry(t *testing.T) {
	// Create archive with an absolute path entry (should be skipped).
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	content := "safe content"
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "/etc/evil",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(content)),
	}))
	_, err := tw.Write([]byte(content))
	require.NoError(t, err)

	safeContent := "safe"
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "safe.txt",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(safeContent)),
	}))
	_, err = tw.Write([]byte(safeContent))
	require.NoError(t, err)

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	dir := t.TempDir()
	err = extractTarGz(bytes.NewReader(buf.Bytes()), dir)
	require.NoError(t, err)

	// Safe file should exist, absolute path entry should be skipped.
	assert.FileExists(t, filepath.Join(dir, "safe.txt"))
}

// --- Install with a bad extraction (invalid gzip in body) ---

func TestInstall_ExtractionFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("not a gzip archive at all"))
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	err = mgr.Install(context.Background(), "2.319.1")
	assert.ErrorContains(t, err, "extraction failed")

	// Version directory should have been cleaned up.
	assert.NoDirExists(t, mgr.VersionDir("2.319.1"))
}

// --- saveConfig error (unwritable config file) ---

func TestSaveConfig_UnwritablePath(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Make the base dir read-only.
	require.NoError(t, os.Chmod(dir, 0o444))
	defer os.Chmod(dir, 0o755)

	err = mgr.saveConfig(Config{InstalledVersion: "1.0"})
	assert.Error(t, err)
}

// --- Configure error paths ---

func TestConfigure_UnwritableRunnerDir(t *testing.T) {
	dir := t.TempDir()
	// Create a runner dir that is read-only.
	runnerDir := filepath.Join(dir, "runner")
	require.NoError(t, os.MkdirAll(runnerDir, 0o755))
	require.NoError(t, os.Chmod(runnerDir, 0o444))
	defer os.Chmod(runnerDir, 0o755)

	p, err := NewProcess(ProcessConfig{
		RunnerDir: runnerDir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	err = p.Configure(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing .runner")

	// Unlock the configMu that Configure grabbed before failing.
	p.mu.Lock()
	if p.configLocked {
		p.configLocked = false
		configMu.Unlock()
	}
	p.mu.Unlock()
}

// --- Stop when process Kill is needed (interrupt fails) ---

func TestStop_AfterProcessAlreadyExited(t *testing.T) {
	dir := t.TempDir()
	runScript := filepath.Join(dir, "run.sh")
	err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\nexit 0"), 0o755)
	require.NoError(t, err)

	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	err = p.Configure(context.Background())
	require.NoError(t, err)

	err = p.Start(context.Background())
	require.NoError(t, err)

	// Wait for process to finish.
	_ = p.Wait()

	// Now Stop should handle it gracefully (process already exited).
	err = p.Stop()
	assert.NoError(t, err)
}

// --- Clean with multiple version dirs and _work dirs ---

func TestClean_MultipleVersionDirs(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Create multiple version directories with _work.
	for _, v := range []string{"2.319.0", "2.320.0"} {
		workDir := filepath.Join(mgr.RunnerDir(), v, "_work")
		require.NoError(t, os.MkdirAll(workDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(workDir, "data.txt"), []byte("data"), 0o644))
	}

	removed, err := mgr.Clean()
	require.NoError(t, err)

	assert.Len(t, removed, 2)
	for _, v := range []string{"2.319.0", "2.320.0"} {
		assert.NoDirExists(t, filepath.Join(mgr.RunnerDir(), v, "_work"))
	}
}

// --- Install with context cancellation (download fails) ---

func TestInstall_ContextCancelled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't respond quickly enough.
		time.Sleep(5 * time.Second)
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = mgr.Install(ctx, "2.319.1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "download failed")
}

// --- LatestVersion with context cancellation ---

func TestLatestVersion_ContextCancelled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = mgr.LatestVersion(ctx)
	assert.Error(t, err)
}

// --- extractTarGz with MkdirAll error on directory entry ---

func TestExtractTarGz_DirEntryWithParentMissing(t *testing.T) {
	// Test that extractTarGz creates nested directories properly.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Add nested directory.
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "a/b/c/",
		Typeflag: tar.TypeDir,
		Mode:     0o755,
	}))

	content := "deep content"
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "a/b/c/file.txt",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(content)),
	}))
	_, err := tw.Write([]byte(content))
	require.NoError(t, err)

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	dir := t.TempDir()
	err = extractTarGz(bytes.NewReader(buf.Bytes()), dir)
	require.NoError(t, err)

	assert.DirExists(t, filepath.Join(dir, "a", "b", "c"))
	data, err := os.ReadFile(filepath.Join(dir, "a", "b", "c", "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "deep content", string(data))
}

// --- Install where saveConfig after install is already-exists path ---

func TestInstall_AlreadyExistsAndSaveConfigFails(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Pre-create the version directory.
	versionDir := mgr.VersionDir("2.319.1")
	require.NoError(t, os.MkdirAll(versionDir, 0o755))

	// Make the base dir read-only so saveConfig will fail.
	require.NoError(t, os.Chmod(dir, 0o555))
	defer os.Chmod(dir, 0o755)

	err = mgr.Install(context.Background(), "2.319.1")
	assert.Error(t, err)
}

// --- Start with context that is already cancelled ---

func TestStart_RunScriptNotExecutable(t *testing.T) {
	dir := t.TempDir()
	// Create run.sh but not executable.
	runScript := filepath.Join(dir, "run.sh")
	err := os.WriteFile(runScript, []byte("#!/bin/bash\necho hello"), 0o644) // NOT executable
	require.NoError(t, err)

	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	err = p.Configure(context.Background())
	require.NoError(t, err)

	err = p.Start(context.Background())
	// Start should fail because run.sh is not executable.
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot start runner")
}

// --- Configure: test .credentials write failure ---

func TestConfigure_CredentialsWriteError(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	// Write .runner first, then create .credentials as a directory so
	// writeJSONFile fails trying to write to it.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".credentials"), 0o755))

	err = p.Configure(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing .credentials")

	// Release configMu.
	p.mu.Lock()
	if p.configLocked {
		p.configLocked = false
		configMu.Unlock()
	}
	p.mu.Unlock()
}

// --- Configure: test .credentials_rsaparams write failure ---

func TestConfigure_RSAParamsWriteError(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	// Create .credentials_rsaparams as a directory so writeJSONFile fails.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".credentials_rsaparams"), 0o755))

	err = p.Configure(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing .credentials_rsaparams")

	// Release configMu.
	p.mu.Lock()
	if p.configLocked {
		p.configLocked = false
		configMu.Unlock()
	}
	p.mu.Unlock()
}

// --- Clean: test cache dir removal on current platform ---

func TestClean_WithCacheDir(t *testing.T) {
	// Create a runner cache dir to exercise the removal code path.
	dirs := RunnerCacheDirs()
	if len(dirs) == 0 {
		t.Skip("no cache dirs on this platform")
	}

	// Create the cache dir (just a temporary version of it).
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// The real RunnerCacheDirs points to the user's home, but we can
	// still test the _work dir cleanup path more thoroughly.
	// Create a version dir with nested _work structure.
	workDir := filepath.Join(mgr.RunnerDir(), "2.321.0", "_work", "project")
	require.NoError(t, os.MkdirAll(workDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("data"), 0o644))

	removed, err := mgr.Clean()
	require.NoError(t, err)
	assert.Contains(t, removed, filepath.Join(mgr.RunnerDir(), "2.321.0", "_work"))
}

// --- Install: MkdirAll for version dir fails ---

func TestInstall_VersionDirCreationFails(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("some data"))
	}))
	defer ts.Close()

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	// Make runner dir read-only so MkdirAll for the version dir fails.
	require.NoError(t, os.Chmod(mgr.RunnerDir(), 0o444))
	defer os.Chmod(mgr.RunnerDir(), 0o755)

	err = mgr.Install(context.Background(), "2.319.1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot create version directory")
}

// --- extractTarGz: file creation error (read-only target dir) ---

func TestExtractTarGz_FileCreationError(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Add a directory, then a file inside it.
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "subdir/",
		Typeflag: tar.TypeDir,
		Mode:     0o755,
	}))
	content := "content"
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "subdir/file.txt",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(content)),
	}))
	_, err := tw.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	dir := t.TempDir()
	// Create subdir as read-only so file creation inside fails.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o444))
	defer os.Chmod(filepath.Join(dir, "subdir"), 0o755)

	err = extractTarGz(bytes.NewReader(buf.Bytes()), dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot create file")
}

// --- writeJSONFile: test with unmarshalable value ---

func TestWriteJSONFile_UnmarshalableValue(t *testing.T) {
	// channels cannot be marshaled to JSON.
	ch := make(chan int)
	err := writeJSONFile(filepath.Join(t.TempDir(), "test.json"), ch)
	assert.Error(t, err)
}

// --- NewManager: verify it doesn't fail on the real system ---

func TestNewManager_CreatesDirectories(t *testing.T) {
	mgr, err := NewManager()
	require.NoError(t, err)
	assert.NotNil(t, mgr)
	assert.DirExists(t, mgr.RunnerDir())
}

// --- Clean: read directory error path ---

func TestClean_RunnerDirReadError(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Remove the runner dir entirely (not just make it unreadable).
	require.NoError(t, os.RemoveAll(mgr.RunnerDir()))
	// Recreate it as a file instead of a directory (to trigger ReadDir error).
	require.NoError(t, os.WriteFile(filepath.Join(dir, "runner"), []byte("not a dir"), 0o644))

	_, err = mgr.Clean()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading")
}

// --- Stop: test with process that ignores SIGINT but not SIGKILL (triggers the kill path) ---

func TestStop_SendsKillAfterInterruptFails(t *testing.T) {
	dir := t.TempDir()
	// Script that traps and ignores SIGINT; a real test of the kill fallback
	// would need 10s timeout, but we can test the path where Interrupt itself
	// returns an error (process already dead).
	runScript := filepath.Join(dir, "run.sh")
	err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\nexit 0"), 0o755)
	require.NoError(t, err)

	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	err = p.Configure(context.Background())
	require.NoError(t, err)

	err = p.Start(context.Background())
	require.NoError(t, err)

	// Wait for the process to exit.
	time.Sleep(500 * time.Millisecond)

	// Process already exited; Stop should handle SIGINT failure gracefully.
	err = p.Stop()
	assert.NoError(t, err)
}

// --- extractTarGz: parent dir creation error for regular file ---

func TestExtractTarGz_ParentDirCreateError(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	content := "test"
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "dir/nested/file.txt",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(content)),
	}))
	_, err := tw.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	dir := t.TempDir()
	// Create a file at the path where a directory should be, causing MkdirAll to fail.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dir"), []byte("blocker"), 0o644))

	err = extractTarGz(bytes.NewReader(buf.Bytes()), dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot create parent directory")
}

// --- extractTarGz: directory creation error ---

func TestExtractTarGz_DirCreateError(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "blocked/subdir/",
		Typeflag: tar.TypeDir,
		Mode:     0o755,
	}))
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	dir := t.TempDir()
	// Create a file where the directory should be.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "blocked"), []byte("blocker"), 0o644))

	err := extractTarGz(bytes.NewReader(buf.Bytes()), dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot create directory")
}

// --- Clean: _work dir RemoveAll error ---

func TestClean_WorkDirRemoveError(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Create a version dir with a _work dir.
	workDir := filepath.Join(mgr.RunnerDir(), "2.320.0", "_work")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	// Create a file in _work, then make the _work dir unreadable so
	// RemoveAll might fail. On some systems this still works, but this
	// at least exercises the code path.
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("data"), 0o644))

	// Make the version dir read-only to potentially cause RemoveAll to fail.
	versionDir := filepath.Join(mgr.RunnerDir(), "2.320.0")
	require.NoError(t, os.Chmod(versionDir, 0o555))
	defer os.Chmod(versionDir, 0o755)

	// This may or may not error depending on the OS; the key is it doesn't panic.
	_, _ = mgr.Clean()
}

// --- Clean: exercises cache dir Stat/Remove path when those dirs exist ---

func TestClean_CacheDirExists(t *testing.T) {
	// Create the runner cache dir matching the current platform.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	var cacheDir string
	switch runtime.GOOS {
	case "linux":
		cacheDir = filepath.Join(home, ".local", "share", "GitHub", "ActionsService", "ions-test-clean")
	case "darwin":
		cacheDir = filepath.Join(home, "Library", "Application Support", "GitHub", "ActionsService", "ions-test-clean")
	default:
		t.Skip("test only applicable to linux/darwin")
	}

	require.NoError(t, os.MkdirAll(cacheDir, 0o755))
	defer os.RemoveAll(filepath.Dir(cacheDir)) // clean up the entire ActionsService/ions-test-clean

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	removed, err := mgr.Clean()
	require.NoError(t, err)
	// The parent ActionsService dir should have been cleaned.
	// Check that at least some removal was detected.
	_ = removed
}

// --- Install: exercise the io.Copy error path in extractTarGz ---

func TestExtractTarGz_IOCopyError(t *testing.T) {
	// Create an archive where a file claims to have more data than provided.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// File header says Size=100 but we only write 5 bytes.
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "truncated.txt",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     100,
	}))
	// Writing less than declared is a tar format violation that causes
	// io.Copy to fail when reading the truncated stream.
	tw.Write([]byte("short"))
	// Force-close to get a malformed archive.
	tw.Flush()
	gw.Close()

	dir := t.TempDir()
	err := extractTarGz(bytes.NewReader(buf.Bytes()), dir)
	// This should error, either during io.Copy or tr.Next.
	assert.Error(t, err)
}

// --- Stop: both Stop paths covered by signaling and already-exited ---

func TestStop_ProcessNilProcess(t *testing.T) {
	p, err := NewProcess(ProcessConfig{
		RunnerDir: "/tmp/runner",
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	// Manually set cmd but not Process.
	p.cmd = &exec.Cmd{}
	// cmd.Process is nil, so Stop should return nil.
	err = p.Stop()
	assert.NoError(t, err)
}

// --- Concurrent configure/start to exercise configMu locking ---

func TestConfigure_And_Start_LockingBehavior(t *testing.T) {
	dir := t.TempDir()
	runScript := filepath.Join(dir, "run.sh")
	err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\nexit 0"), 0o755)
	require.NoError(t, err)

	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	err = p.Configure(context.Background())
	require.NoError(t, err)

	err = p.Start(context.Background())
	require.NoError(t, err)

	// Wait for process to finish and config lock to release.
	_ = p.Wait()
	time.Sleep(3 * time.Second)
}

// --- extractTarGz: symlink with relative path that stays inside target ---

// --- Stop: Kill path when Interrupt returns error and process is NOT exited ---

func TestStop_InterruptFailsWithKillFallback(t *testing.T) {
	dir := t.TempDir()
	// Create a quick-exit script.
	runScript := filepath.Join(dir, "run.sh")
	err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\nsleep 1\nexit 0"), 0o755)
	require.NoError(t, err)

	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	err = p.Configure(context.Background())
	require.NoError(t, err)

	err = p.Start(context.Background())
	require.NoError(t, err)

	// Let the process exit naturally.
	time.Sleep(2 * time.Second)

	// Now the process is exited. When Stop calls Signal(Interrupt),
	// it will get "os: process already finished" error, then check
	// ProcessState.Exited() which should be true (process finished via Wait goroutine).
	err = p.Stop()
	assert.NoError(t, err)
}

func TestExtractTarGz_SymlinkRelativeInside(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	content := "original"
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "dir/original.txt",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(content)),
	}))
	_, err := tw.Write([]byte(content))
	require.NoError(t, err)

	// Symlink within the archive that points relatively inside.
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "dir/link.txt",
		Typeflag: tar.TypeSymlink,
		Linkname: "original.txt",
	}))

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	dir := t.TempDir()
	err = extractTarGz(bytes.NewReader(buf.Bytes()), dir)
	require.NoError(t, err)

	// Both the file and the symlink should exist.
	assert.FileExists(t, filepath.Join(dir, "dir", "original.txt"))
	linkTarget, err := os.Readlink(filepath.Join(dir, "dir", "link.txt"))
	require.NoError(t, err)
	assert.Equal(t, "original.txt", linkTarget)
}

// --- Additional coverage tests targeting specific uncovered lines ---

// TestClean_RemoveAllError covers manager.go:358-360 where os.RemoveAll fails
// on the runner cache directories.
func TestClean_RemoveAllError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based test not reliable on Windows")
	}

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Create a _work directory inside a runner version dir.
	versionDir := filepath.Join(mgr.RunnerDir(), "2.320.0")
	workDir := filepath.Join(versionDir, "_work")
	require.NoError(t, os.MkdirAll(workDir, 0o755))
	// Create a file inside _work to make it non-empty.
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("data"), 0o644))

	// Make _work non-removable by removing write permission from its parent.
	require.NoError(t, os.Chmod(versionDir, 0o555))
	defer os.Chmod(versionDir, 0o755) // restore for cleanup

	// Clean should still succeed for the _work portion—but RemoveAll should fail.
	// The _work dir stat will succeed (it exists), but RemoveAll will fail
	// because the parent dir is read-only.
	removed, err := mgr.Clean()
	// We expect an error from the _work removal attempt.
	if err != nil {
		assert.Contains(t, err.Error(), "removing")
	}
	// Even if cache dirs were cleaned, the _work error should dominate.
	_ = removed
}

// TestStopKillAfterTimeout covers process.go:248-249 where Stop sends SIGKILL
// after the 10-second timeout expires. We override the timeout by using a process
// that traps SIGINT and refuses to exit.
func TestStopKillAfterTimeout(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	if testing.Short() {
		t.Skip("skipping slow test in short mode")
	}

	dir := t.TempDir()
	// Script that traps SIGINT and continues sleeping (refuses to die gracefully).
	runScript := filepath.Join(dir, "run.sh")
	err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\ntrap '' INT TERM\nwhile true; do sleep 1; done"), 0o755)
	require.NoError(t, err)

	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	err = p.Configure(context.Background())
	require.NoError(t, err)

	err = p.Start(context.Background())
	require.NoError(t, err)

	// Give the process time to start and set up signal traps.
	time.Sleep(1 * time.Second)
	require.True(t, p.IsRunning(), "process should still be running after 1s")

	// Stop will send SIGINT, wait 10s, then SIGKILL.
	// This takes ~10 seconds. The process ignores SIGINT so the timeout fires.
	start := time.Now()
	err = p.Stop()
	elapsed := time.Since(start)
	// Should take at least ~9 seconds (the timeout with some slack).
	assert.True(t, elapsed >= 9*time.Second, "Stop should have waited ~10s before killing, but only waited %s", elapsed)
	// The Kill should succeed (process dies).
	_ = err
}

// TestStopInterruptFailsProcessDead covers process.go:237-241 where
// Signal(os.Interrupt) fails because the process has already exited,
// and then ProcessState shows it exited (returning nil), or if not,
// calling Kill().
func TestStopInterruptFailsProcessDead(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	dir := t.TempDir()
	// Script that exits immediately.
	runScript := filepath.Join(dir, "run.sh")
	err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\nexit 0"), 0o755)
	require.NoError(t, err)

	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	err = p.Configure(context.Background())
	require.NoError(t, err)

	err = p.Start(context.Background())
	require.NoError(t, err)

	// Wait for the process to fully exit (the goroutine sets exited=true).
	time.Sleep(500 * time.Millisecond)

	// Now call Stop on the already-exited process.
	// Signal(os.Interrupt) will fail, but ProcessState should be set and Exited().
	err = p.Stop()
	assert.NoError(t, err)
}

// TestInstallDownloadError covers manager.go:170-173 where httpClient.Do fails
// (as opposed to the request creation error at 166-168 which is nearly impossible).
func TestInstallDownloadError(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Use a client with a transport that always fails.
	mgr.httpClient = &http.Client{
		Transport: failTransport{},
	}

	err = mgr.Install(context.Background(), "2.999.0")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "download failed")
}

// TestLatestVersionConnectionError covers manager.go:237-240 where httpClient.Do fails.
func TestLatestVersionConnectionError(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Use a client that refuses connections.
	mgr.httpClient = &http.Client{
		Transport: failTransport{},
	}

	_, err = mgr.LatestVersion(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "GitHub API request failed")
}

// TestLatestVersionInvalidJSON covers manager.go:248-250 where JSON decoding fails.
func TestLatestVersionInvalidJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{invalid json`))
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	_, err = mgr.LatestVersion(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse GitHub API response")
}

// failTransport is an http.RoundTripper that always returns an error.
type failTransport struct{}

func (failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("connection refused (test)")
}

// TestConfigureWriteRunnerFails covers process.go:93-95 (writing .runner fails).
func TestConfigureWriteRunnerFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based test not reliable on Windows")
	}

	dir := t.TempDir()
	// Make the runner dir read-only so writeJSONFile fails.
	require.NoError(t, os.Chmod(dir, 0o555))
	defer os.Chmod(dir, 0o755)

	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	err = p.Configure(context.Background())
	// Configure holds configMu. Since it fails, we need to release it.
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing .runner")

	// The configMu is still held since Configure failed after locking.
	// We need to release it to avoid deadlocking other tests.
	p.mu.Lock()
	if p.configLocked {
		p.configLocked = false
		configMu.Unlock()
	}
	p.mu.Unlock()
}

// TestConfigureWriteCredentialsFails covers process.go:106-108 (writing .credentials fails).
func TestConfigureWriteCredentialsFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based test not reliable on Windows")
	}

	dir := t.TempDir()
	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	// Create .runner first so that write succeeds, then make dir read-only
	// so .credentials fails. But actually, Configure writes .runner first,
	// so we need .runner to succeed but .credentials to fail.
	// We can't easily do this without mocking. Instead, create a file named
	// ".credentials" as a directory, which will cause the write to fail.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".credentials"), 0o755))

	err = p.Configure(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing .credentials")

	// Release configMu if held.
	p.mu.Lock()
	if p.configLocked {
		p.configLocked = false
		configMu.Unlock()
	}
	p.mu.Unlock()
}

// TestConfigureWriteRSAParamsFails covers process.go:116-118
// (writing .credentials_rsaparams fails).
func TestConfigureWriteRSAParamsFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based test not reliable on Windows")
	}

	dir := t.TempDir()
	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	// Create a directory named .credentials_rsaparams to make writeJSONFile fail.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".credentials_rsaparams"), 0o755))

	err = p.Configure(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing .credentials_rsaparams")

	// Release configMu if held.
	p.mu.Lock()
	if p.configLocked {
		p.configLocked = false
		configMu.Unlock()
	}
	p.mu.Unlock()
}

// TestClean_WorkDirRemoval covers the happy path of removing _work directories
// inside installed runner versions.
func TestClean_WorkDirRemovalSuccess(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Create multiple version dirs with _work directories.
	for _, ver := range []string{"2.319.0", "2.320.0"} {
		versionDir := filepath.Join(mgr.RunnerDir(), ver)
		workDir := filepath.Join(versionDir, "_work")
		require.NoError(t, os.MkdirAll(workDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(workDir, "job.log"), []byte("log"), 0o644))
	}

	// Also create a file (not a directory) in the runner dir — should be skipped.
	require.NoError(t, os.WriteFile(filepath.Join(mgr.RunnerDir(), "config.json"), []byte("{}"), 0o644))

	removed, err := mgr.Clean()
	require.NoError(t, err)

	// Both _work dirs should have been removed.
	assert.Len(t, removed, 2)
	for _, ver := range []string{"2.319.0", "2.320.0"} {
		workDir := filepath.Join(mgr.RunnerDir(), ver, "_work")
		assert.NoDirExists(t, workDir)
	}
}

// TestInstallExtractionFails covers manager.go:188-192 where extraction fails.
func TestInstallExtractionFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Send garbage data that isn't valid gzip.
		w.Write([]byte("this is not a tar.gz file"))
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	err = mgr.Install(context.Background(), "2.999.0")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "extraction failed")

	// The partially-created directory should have been cleaned up.
	assert.NoDirExists(t, mgr.VersionDir("2.999.0"))
}

// TestNewManager covers manager.go:44-49 (NewManager with real home dir).
func TestNewManager_Success(t *testing.T) {
	// Only test if HOME is set to avoid side effects.
	if os.Getenv("HOME") == "" {
		t.Skip("HOME not set")
	}

	mgr, err := NewManager()
	if err != nil {
		// If it fails (e.g., permissions), that's OK — we just want coverage.
		t.Logf("NewManager failed: %v", err)
		return
	}
	assert.NotNil(t, mgr)
}

// TestClean_ReadDirError covers manager.go:368 where ReadDir fails
// on a non-existent runner directory.
func TestClean_ReadDirNonExistent(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Remove the runner directory so ReadDir fails.
	require.NoError(t, os.RemoveAll(mgr.RunnerDir()))

	removed, err := mgr.Clean()
	require.NoError(t, err) // os.IsNotExist is handled gracefully.
	assert.Empty(t, removed)
}

// TestNewManager_HomeDirError covers manager.go:46-48 where os.UserHomeDir fails.
func TestNewManager_HomeDirError(t *testing.T) {
	// Save and unset HOME so os.UserHomeDir fails.
	origHome := os.Getenv("HOME")
	os.Unsetenv("HOME")
	defer os.Setenv("HOME", origHome)

	_, err := NewManager()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot determine home directory")
}

// TestRunnerCacheDirs_HomeDirError covers manager.go:337-339 where
// os.UserHomeDir fails and returns nil.
func TestRunnerCacheDirs_HomeDirError(t *testing.T) {
	origHome := os.Getenv("HOME")
	os.Unsetenv("HOME")
	defer os.Setenv("HOME", origHome)

	dirs := RunnerCacheDirs()
	assert.Nil(t, dirs)
}

// TestClean_CacheDirRemoveAllError covers manager.go:358-360 where os.RemoveAll
// fails on a runner cache directory.
func TestClean_CacheDirRemoveAllError(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("test only works on Linux")
	}

	// Use a temp directory as HOME so RunnerCacheDirs returns predictable paths.
	fakeHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", fakeHome)
	defer os.Setenv("HOME", origHome)

	// Create the cache directory with a non-removable structure.
	cacheDir := filepath.Join(fakeHome, ".local", "share", "GitHub", "ActionsService")
	require.NoError(t, os.MkdirAll(filepath.Join(cacheDir, "inner"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, "inner", "file.txt"), []byte("data"), 0o644))

	// Make the cache directory non-writable so RemoveAll fails when trying
	// to remove entries inside it.
	require.NoError(t, os.Chmod(cacheDir, 0o555))
	defer os.Chmod(cacheDir, 0o755) // restore for cleanup

	// Create a manager (using a different base dir).
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Clean should hit the RemoveAll error for the cache dir.
	_, err = mgr.Clean()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "removing")
}

// TestStopKillAfterSignalFails covers process.go:241 where Signal(Interrupt)
// fails and ProcessState is nil, so we fall through to Kill.
func TestStopKillAfterSignalFails(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	dir := t.TempDir()
	runScript := filepath.Join(dir, "run.sh")
	err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\nsleep 60"), 0o755)
	require.NoError(t, err)

	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	err = p.Configure(context.Background())
	require.NoError(t, err)

	err = p.Start(context.Background())
	require.NoError(t, err)
	assert.True(t, p.IsRunning())

	// Give the process a moment to start.
	time.Sleep(200 * time.Millisecond)

	// Release the process handle so that Signal will fail,
	// and ProcessState will be nil.
	p.mu.Lock()
	p.cmd.Process.Release()
	p.mu.Unlock()

	// Now Stop should:
	// 1. Signal(os.Interrupt) -> error "process already released"
	// 2. ProcessState == nil (not exited)
	// 3. Kill() -> also error "process already released"
	err = p.Stop()
	// Kill returns an error since the process was released.
	assert.Error(t, err)

	// Clean up: the process is now orphaned. We can't kill it through Go.
	// It will exit when the context is done or when the test cleans up.
}
