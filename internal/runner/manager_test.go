package runner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlatformString(t *testing.T) {
	got := platformString()
	switch runtime.GOOS {
	case "darwin":
		assert.Equal(t, "osx", got)
	case "linux":
		assert.Equal(t, "linux", got)
	case "windows":
		assert.Equal(t, "win", got)
	default:
		assert.Equal(t, runtime.GOOS, got)
	}
}

func TestArchString(t *testing.T) {
	got := archString()
	switch runtime.GOARCH {
	case "amd64":
		assert.Equal(t, "x64", got)
	case "arm64":
		assert.Equal(t, "arm64", got)
	default:
		assert.Equal(t, runtime.GOARCH, got)
	}
}

func TestDownloadURL(t *testing.T) {
	url := downloadURL("2.319.1")
	plat := platformString()
	arch := archString()

	expected := "https://github.com/actions/runner/releases/download/v2.319.1/actions-runner-" +
		plat + "-" + arch + "-2.319.1.tar.gz"
	if runtime.GOOS == "windows" {
		expected = "https://github.com/actions/runner/releases/download/v2.319.1/actions-runner-" +
			plat + "-" + arch + "-2.319.1.zip"
	}
	assert.Equal(t, expected, url)
}

func TestNewManagerWithBaseDir(t *testing.T) {
	dir := t.TempDir()
	baseDir := filepath.Join(dir, "ions-test")

	mgr, err := NewManagerWithBaseDir(baseDir)
	require.NoError(t, err)

	// Directories should be created.
	assert.DirExists(t, baseDir)
	assert.DirExists(t, filepath.Join(baseDir, "runner"))
	assert.Equal(t, filepath.Join(baseDir, "runner"), mgr.RunnerDir())
}

func TestVersionDir(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dir, "runner", "2.319.1"), mgr.VersionDir("2.319.1"))
}

func TestConfigReadWrite(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// No config yet — should return empty.
	ver, err := mgr.InstalledVersion()
	require.NoError(t, err)
	assert.Equal(t, "", ver)

	// Save a config.
	cfg := Config{
		InstalledVersion: "2.319.1",
		Platform:         "osx",
		Architecture:     "arm64",
	}
	err = mgr.saveConfig(cfg)
	require.NoError(t, err)

	// Read it back.
	ver, err = mgr.InstalledVersion()
	require.NoError(t, err)
	assert.Equal(t, "2.319.1", ver)

	// Verify the config file is valid JSON.
	data, err := os.ReadFile(mgr.configPath())
	require.NoError(t, err)

	var readCfg Config
	err = json.Unmarshal(data, &readCfg)
	require.NoError(t, err)
	assert.Equal(t, cfg, readCfg)
}

func TestLatestVersion(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/repos/actions/runner/releases/latest", r.URL.Path)
		assert.Equal(t, "application/vnd.github.v3+json", r.Header.Get("Accept"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(githubRelease{TagName: "v2.320.0"})
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Override the HTTP client to use the test server.
	origClient := mgr.httpClient
	mgr.httpClient = ts.Client()
	defer func() { mgr.httpClient = origClient }()

	// We need to intercept the URL. Create a custom transport.
	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{
			base:    http.DefaultTransport,
			baseURL: ts.URL,
		},
	}

	ver, err := mgr.LatestVersion(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "2.320.0", ver)
}

func TestLatestVersionHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{
			base:    http.DefaultTransport,
			baseURL: ts.URL,
		},
	}

	_, err = mgr.LatestVersion(context.Background())
	assert.ErrorContains(t, err, "HTTP 503")
}

func TestLatestVersionEmptyTag(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(githubRelease{TagName: ""})
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{
			base:    http.DefaultTransport,
			baseURL: ts.URL,
		},
	}

	_, err = mgr.LatestVersion(context.Background())
	assert.ErrorContains(t, err, "empty tag_name")
}

func TestInstallWithMockServer(t *testing.T) {
	// Create a minimal tar.gz archive to serve.
	archiveData := createTestArchive(t, map[string]string{
		"run.sh":    "#!/bin/bash\necho running",
		"config.sh": "#!/bin/bash\necho configuring",
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(archiveData)
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{
			base:    http.DefaultTransport,
			baseURL: ts.URL,
		},
	}

	err = mgr.Install(context.Background(), "2.319.1")
	require.NoError(t, err)

	// Check version directory was created.
	versionDir := mgr.VersionDir("2.319.1")
	assert.DirExists(t, versionDir)

	// Check files were extracted.
	assert.FileExists(t, filepath.Join(versionDir, "run.sh"))
	assert.FileExists(t, filepath.Join(versionDir, "config.sh"))

	// Check config was saved.
	ver, err := mgr.InstalledVersion()
	require.NoError(t, err)
	assert.Equal(t, "2.319.1", ver)
}

func TestInstallStripsVPrefix(t *testing.T) {
	archiveData := createTestArchive(t, map[string]string{
		"run.sh": "#!/bin/bash\necho running",
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

	err = mgr.Install(context.Background(), "v2.319.1")
	require.NoError(t, err)

	// Should use "2.319.1" not "v2.319.1" for the directory.
	assert.DirExists(t, mgr.VersionDir("2.319.1"))
}

func TestInstallAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	// Pre-create the version directory.
	versionDir := mgr.VersionDir("2.319.1")
	require.NoError(t, os.MkdirAll(versionDir, 0o755))

	// Install should succeed without downloading.
	err = mgr.Install(context.Background(), "2.319.1")
	require.NoError(t, err)

	ver, err := mgr.InstalledVersion()
	require.NoError(t, err)
	assert.Equal(t, "2.319.1", ver)
}

func TestInstallComputesChecksum(t *testing.T) {
	archiveData := createTestArchive(t, map[string]string{
		"run.sh": "#!/bin/bash\necho running",
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

	// Config should contain a SHA-256 hash.
	cfg, err := mgr.loadConfig()
	require.NoError(t, err)
	assert.Len(t, cfg.SHA256, 64, "SHA-256 should be 64 hex characters")
	assert.NotEmpty(t, cfg.SHA256)
}

func TestInstallChecksumMismatch(t *testing.T) {
	archiveData := createTestArchive(t, map[string]string{
		"run.sh": "#!/bin/bash\necho running",
	})
	differentArchive := createTestArchive(t, map[string]string{
		"run.sh": "#!/bin/bash\necho DIFFERENT",
	})

	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Write(archiveData)
		} else {
			w.Write(differentArchive)
		}
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	// First install.
	err = mgr.Install(context.Background(), "2.319.1")
	require.NoError(t, err)

	firstCfg, err := mgr.loadConfig()
	require.NoError(t, err)
	firstHash := firstCfg.SHA256

	// Remove the version directory to force re-download.
	require.NoError(t, os.RemoveAll(mgr.VersionDir("2.319.1")))

	// Second install with different archive should fail checksum verification.
	err = mgr.Install(context.Background(), "2.319.1")
	assert.ErrorContains(t, err, "checksum mismatch")

	// The stored hash should still be the original.
	cfg, err := mgr.loadConfig()
	require.NoError(t, err)
	assert.Equal(t, firstHash, cfg.SHA256)
}

func TestInstallHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	err = mgr.Install(context.Background(), "99.99.99")
	assert.ErrorContains(t, err, "HTTP 404")
}

func TestEnsureInstalled(t *testing.T) {
	archiveData := createTestArchive(t, map[string]string{
		"run.sh": "#!/bin/bash\necho running",
	})

	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/repos/actions/runner/releases/latest" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(githubRelease{TagName: "v2.320.0"})
			return
		}
		// Download request.
		w.Write(archiveData)
	}))
	defer ts.Close()

	dir := t.TempDir()
	mgr, err := NewManagerWithBaseDir(dir)
	require.NoError(t, err)

	mgr.httpClient = &http.Client{
		Transport: rewriteTransport{base: http.DefaultTransport, baseURL: ts.URL},
	}

	// First call — should install.
	runnerDir, err := mgr.EnsureInstalled(context.Background())
	require.NoError(t, err)
	assert.Equal(t, mgr.VersionDir("2.320.0"), runnerDir)
	assert.Equal(t, 2, calls) // one for latest, one for download

	// Second call — should be cached.
	runnerDir2, err := mgr.EnsureInstalled(context.Background())
	require.NoError(t, err)
	assert.Equal(t, runnerDir, runnerDir2)
	assert.Equal(t, 2, calls) // no additional calls
}

func TestExtractTarGz(t *testing.T) {
	archiveData := createTestArchive(t, map[string]string{
		"file.txt":        "hello world",
		"subdir/file2.go": "package main",
	})

	dir := t.TempDir()
	err := extractTarGz(
		// Read from the archive bytes.
		bytes_reader(archiveData),
		dir,
	)
	require.NoError(t, err)

	// Check files.
	content, err := os.ReadFile(filepath.Join(dir, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(content))

	content, err = os.ReadFile(filepath.Join(dir, "subdir", "file2.go"))
	require.NoError(t, err)
	assert.Equal(t, "package main", string(content))
}

// --- helpers ---

// rewriteTransport rewrites all request URLs to point at the test server.
type rewriteTransport struct {
	base    http.RoundTripper
	baseURL string
}

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = t.baseURL[len("http://"):]
	return t.base.RoundTrip(req)
}

// createTestArchive creates a tar.gz archive containing the given files.
func createTestArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write([]byte(content))
		require.NoError(t, err)
	}

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

func bytes_reader(data []byte) *bytes.Reader {
	return bytes.NewReader(data)
}
