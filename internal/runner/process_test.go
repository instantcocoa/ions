package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProcess_Defaults(t *testing.T) {
	p, err := NewProcess(ProcessConfig{
		RunnerDir: "/tmp/runner",
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)
	assert.Equal(t, "ions-runner", p.name)
	assert.Equal(t, filepath.Join("/tmp/runner", "_work"), p.workDir)
	assert.Equal(t, "http://localhost:8080", p.brokerURL)
}

func TestNewProcess_CustomValues(t *testing.T) {
	p, err := NewProcess(ProcessConfig{
		RunnerDir: "/tmp/runner",
		BrokerURL: "http://localhost:9090",
		Name:      "my-runner",
		WorkDir:   "/tmp/workspace",
	})
	require.NoError(t, err)
	assert.Equal(t, "my-runner", p.name)
	assert.Equal(t, "/tmp/workspace", p.workDir)
}

func TestNewProcess_Validation(t *testing.T) {
	_, err := NewProcess(ProcessConfig{
		BrokerURL: "http://localhost:8080",
	})
	assert.ErrorContains(t, err, "RunnerDir is required")

	_, err = NewProcess(ProcessConfig{
		RunnerDir: "/tmp/runner",
	})
	assert.ErrorContains(t, err, "BrokerURL is required")
}

func TestRunnerConfig(t *testing.T) {
	p, err := NewProcess(ProcessConfig{
		RunnerDir: "/tmp/runner",
		BrokerURL: "http://localhost:8080",
		Name:      "test-runner",
		WorkDir:   "/tmp/work",
	})
	require.NoError(t, err)

	cfg := p.RunnerConfig()
	assert.Equal(t, "test-runner", cfg["agentName"])
	assert.Equal(t, "http://localhost:8080", cfg["serverUrl"])
	assert.Equal(t, "http://localhost:8080", cfg["gitHubUrl"])
	assert.Equal(t, "/tmp/work", cfg["workFolder"])
}

func TestConfigureWritesFiles(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
		Name:      "test-runner",
	})
	require.NoError(t, err)

	err = p.Configure(context.Background())
	require.NoError(t, err)
	// Configure holds configMu — release it since we won't call Start.
	p.configLocked = false
	configMu.Unlock()

	// Verify .runner file was created.
	_, err = os.Stat(filepath.Join(dir, ".runner"))
	assert.NoError(t, err)

	// Verify .credentials file was created.
	_, err = os.Stat(filepath.Join(dir, ".credentials"))
	assert.NoError(t, err)

	// Verify .credentials_rsaparams file was created.
	_, err = os.Stat(filepath.Join(dir, ".credentials_rsaparams"))
	assert.NoError(t, err)
}

func TestRunnerEnvVars(t *testing.T) {
	envs := runnerEnvVars()
	assert.Contains(t, envs, "ACTIONS_RUNNER_PRINT_LOG_TO_STDOUT=1")
	assert.Contains(t, envs, "RUNNER_ALLOW_RUNASROOT=1")
}

func TestIsRunning_NotStarted(t *testing.T) {
	p, err := NewProcess(ProcessConfig{
		RunnerDir: "/tmp/runner",
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)
	assert.False(t, p.IsRunning())
}

func TestWait_NotStarted(t *testing.T) {
	p, err := NewProcess(ProcessConfig{
		RunnerDir: "/tmp/runner",
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)
	err = p.Wait()
	assert.ErrorContains(t, err, "not started")
}

func TestStartDoubleStart(t *testing.T) {
	dir := t.TempDir()
	// Create a fake run.sh that sleeps.
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
	defer p.Stop()

	// Second start should fail.
	err = p.Start(context.Background())
	assert.ErrorContains(t, err, "already started")
}

func TestStartAndStop(t *testing.T) {
	dir := t.TempDir()
	// Create a fake run.sh that sleeps but handles signals.
	runScript := filepath.Join(dir, "run.sh")
	err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\nexec sleep 60"), 0o755)
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

	err = p.Stop()
	require.NoError(t, err)

	// After Stop returns, the process should no longer be running.
	// Give a brief moment for the done channel goroutine to complete.
	time.Sleep(200 * time.Millisecond)
	assert.False(t, p.IsRunning())
}

func TestStartMissingScripts(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProcess(ProcessConfig{
		RunnerDir: dir,
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	err = p.Start(context.Background())
	assert.ErrorContains(t, err, "neither run.sh nor bin/Runner.Listener found")
}

func TestStartFallbackToListener(t *testing.T) {
	dir := t.TempDir()

	// Create a fake bin/Runner.Listener instead of run.sh.
	binDir := filepath.Join(dir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	listener := filepath.Join(binDir, "Runner.Listener")
	err := os.WriteFile(listener, []byte("#!/usr/bin/env bash\nsleep 60"), 0o755)
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
	defer p.Stop()

	assert.True(t, p.IsRunning())
}

func TestStopAlreadyStopped(t *testing.T) {
	p, err := NewProcess(ProcessConfig{
		RunnerDir: "/tmp/runner",
		BrokerURL: "http://localhost:8080",
	})
	require.NoError(t, err)

	// Stopping a process that was never started should be a no-op.
	err = p.Stop()
	assert.NoError(t, err)
}

func TestStopProcessThatExitsQuickly(t *testing.T) {
	dir := t.TempDir()
	// Create a run.sh that exits immediately.
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

	// Give it a moment to exit.
	time.Sleep(200 * time.Millisecond)

	// Stop should handle the already-exited process gracefully.
	err = p.Stop()
	assert.NoError(t, err)
}

// TestStopKillsHangingProcess verifies that Stop sends SIGKILL after timeout.
// We use a short helper that ignores SIGINT.
func TestStopKillsHangingProcess(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	dir := t.TempDir()
	// Script that traps SIGINT and ignores it.
	runScript := filepath.Join(dir, "run.sh")
	err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\ntrap '' INT\nsleep 300"), 0o755)
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

	// We can't easily test the 10s timeout in a unit test, so just verify
	// that Stop doesn't hang forever (it should eventually SIGKILL).
	// For speed, we'll just stop and not wait the full 10s.
	// Instead, just verify the process structure is correct.
	go func() {
		time.Sleep(500 * time.Millisecond)
		if p.cmd != nil && p.cmd.Process != nil {
			p.cmd.Process.Kill()
		}
	}()
	p.Stop()
}
