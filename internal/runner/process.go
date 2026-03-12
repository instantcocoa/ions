package runner

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// ProcessConfig holds the configuration for launching a runner process.
type ProcessConfig struct {
	RunnerDir string // path to extracted runner (e.g. ~/.ions/runner/2.319.1/)
	BrokerURL string // http://localhost:{port}
	Name      string // runner name (default: "ions-runner")
	WorkDir   string // working directory for jobs
	ExtraEnv  []string // additional environment variables (KEY=VALUE format)
}

// Process manages a single runner process lifecycle.
type Process struct {
	runnerDir string
	workDir   string
	brokerURL string
	name      string

	cmd          *exec.Cmd
	stdout       io.ReadCloser
	stderr       io.ReadCloser
	done         chan error
	exited       bool
	configLocked bool
	extraEnv        []string
	dockerContainer string
	mu              sync.Mutex
}

// NewProcess creates a new runner process manager from the given config.
// It validates required fields and applies defaults.
func NewProcess(cfg ProcessConfig) (*Process, error) {
	if cfg.RunnerDir == "" {
		return nil, errors.New("RunnerDir is required")
	}
	if cfg.BrokerURL == "" {
		return nil, errors.New("BrokerURL is required")
	}
	if cfg.Name == "" {
		cfg.Name = "ions-runner"
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = filepath.Join(cfg.RunnerDir, "_work")
	}
	return &Process{
		runnerDir: cfg.RunnerDir,
		workDir:   cfg.WorkDir,
		brokerURL: cfg.BrokerURL,
		name:      cfg.Name,
		extraEnv:  cfg.ExtraEnv,
	}, nil
}

// configMu serializes Configure+Start across processes. The runner reads
// config from its root directory (determined by binary location), so when
// multiple runners share the same binary directory, config files must not
// be overwritten before the previous runner has loaded them.
var configMu sync.Mutex

// Configure writes runner config files to the runner directory and acquires
// the config lock. The caller must call Start() promptly — the lock is
// released after the runner has had time to load its config.
func (p *Process) Configure(ctx context.Context) error {
	configMu.Lock()
	p.mu.Lock()
	p.configLocked = true
	p.mu.Unlock()

	// .runner — main config.
	runnerConfig := map[string]any{
		"agentId":    1,
		"agentName":  p.name,
		"poolId":     1,
		"poolName":   "Default",
		"serverUrl":  p.brokerURL,
		"gitHubUrl":  p.brokerURL,
		"workFolder": p.workDir,
	}
	if err := writeJSONFile(filepath.Join(p.runnerDir, ".runner"), runnerConfig); err != nil {
		p.configLocked = false
		configMu.Unlock()
		return fmt.Errorf("writing .runner: %w", err)
	}

	// .credentials
	credentials := map[string]any{
		"scheme": "OAuth",
		"data": map[string]string{
			"clientId":         "00000000-0000-0000-0000-000000000000",
			"authorizationUrl": p.brokerURL + "/_apis/oauth2/token",
			"oAuthEndpointUrl": p.brokerURL + "/_apis/oauth2/token",
		},
	}
	if err := writeJSONFile(filepath.Join(p.runnerDir, ".credentials"), credentials); err != nil {
		p.configLocked = false
		configMu.Unlock()
		return fmt.Errorf("writing .credentials: %w", err)
	}

	// .credentials_rsaparams
	rsaParams, err := generateRSAParams()
	if err != nil {
		p.configLocked = false
		configMu.Unlock()
		return fmt.Errorf("generating RSA key: %w", err)
	}
	if err := writeJSONFile(filepath.Join(p.runnerDir, ".credentials_rsaparams"), rsaParams); err != nil {
		p.configLocked = false
		configMu.Unlock()
		return fmt.Errorf("writing .credentials_rsaparams: %w", err)
	}

	// Ensure work directory exists.
	if err := os.MkdirAll(p.workDir, 0o755); err != nil {
		p.configLocked = false
		configMu.Unlock()
		return err
	}
	return nil
}

// ReleaseConfigLock releases the config lock if it's held by this process.
// Call this if Configure() was called but Start() will not be called.
func (p *Process) ReleaseConfigLock() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.configLocked {
		p.configLocked = false
		configMu.Unlock()
	}
}

// RunnerConfig returns the runner config that would be written to .runner.
// Useful for testing without actually writing files.
func (p *Process) RunnerConfig() map[string]any {
	return map[string]any{
		"agentId":    1,
		"agentName":  p.name,
		"poolId":     1,
		"poolName":   "Default",
		"serverUrl":  p.brokerURL,
		"gitHubUrl":  p.brokerURL,
		"workFolder": p.workDir,
	}
}

// Start launches the runner process.
// Must be called after Configure(). Releases the config lock after the
// runner has had time to read its config files.
func (p *Process) Start(ctx context.Context) error {
	useDocker := needsDockerFn()

	// Release the config lock after the runner has loaded config.
	// Docker mode needs more time (docker.io install + runner startup).
	defer func() {
		go func() {
			delay := 2 * time.Second
			if useDocker {
				delay = 30 * time.Second
			}
			time.Sleep(delay)
			p.mu.Lock()
			if p.configLocked {
				p.configLocked = false
				configMu.Unlock()
			}
			p.mu.Unlock()
		}()
	}()

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd != nil {
		return errors.New("runner process already started")
	}

	var cmd *exec.Cmd
	if useDocker {
		cmd = p.buildDockerCommand(ctx)
		p.dockerContainer = sanitizeContainerName("ions-runner-" + p.name)
	} else {
		cmd = p.buildNativeCommand(ctx)
		if cmd == nil {
			return fmt.Errorf("neither bin/Runner.Listener nor run.sh found in %s", p.runnerDir)
		}
		cmd.Env = append(os.Environ(), runnerEnvVars()...)
		cmd.Env = append(cmd.Env, p.extraEnv...)
	}
	cmd.Dir = p.runnerDir
	// Start in a new process group so we can kill all child processes at once.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("cannot create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("cannot create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start runner: %w", err)
	}

	p.cmd = cmd
	p.stdout = stdout
	p.stderr = stderr
	p.done = make(chan error, 1)
	p.exited = false

	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		p.exited = true
		p.mu.Unlock()
		p.done <- err
	}()

	return nil
}

// Wait blocks until the runner process exits.
func (p *Process) Wait() error {
	p.mu.Lock()
	done := p.done
	p.mu.Unlock()

	if done == nil {
		return errors.New("runner process not started")
	}
	return <-done
}

// Stop gracefully shuts down the runner process and all its children.
// Sends SIGINT to the process group first, waits up to 5 seconds, then SIGKILL.
func (p *Process) Stop() error {
	p.mu.Lock()
	cmd := p.cmd
	done := p.done
	p.mu.Unlock()

	containerName := p.dockerContainer

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	if containerName != "" {
		_ = exec.Command("docker", "stop", "-t", "5", containerName).Run()
		_ = exec.Command("docker", "rm", "-f", containerName).Run()
		return nil
	}

	pid := cmd.Process.Pid

	// Send SIGINT to the process group (negative PID).
	if err := syscall.Kill(-pid, syscall.SIGINT); err != nil {
		// Process may already be dead.
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return nil
		}
	}

	// Wait up to 5 seconds for graceful shutdown.
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		// Kill the entire process group.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		// Also kill via Go API as fallback.
		_ = cmd.Process.Kill()
		return nil
	}
}

// IsRunning returns true if the runner process is currently running.
func (p *Process) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	return !p.exited
}

// Stdout returns the stdout reader for the runner process.
// Only valid after Start() is called.
func (p *Process) Stdout() io.ReadCloser {
	return p.stdout
}

// Stderr returns the stderr reader for the runner process.
// Only valid after Start() is called.
func (p *Process) Stderr() io.ReadCloser {
	return p.stderr
}

// runnerEnvVars returns the environment variables to set on runner processes.
func runnerEnvVars() []string {
	return []string{
		"ACTIONS_RUNNER_PRINT_LOG_TO_STDOUT=1",
		"RUNNER_ALLOW_RUNASROOT=1",
		"DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1",
	}
}

// writeJSONFile writes a value as formatted JSON to a file.
func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// generateRSAParams generates an RSA key pair and returns it in the format
// expected by the runner's RSAParametersSerializable (.NET format).
// Fields are base64-encoded byte arrays matching System.Security.Cryptography.RSAParameters.
func generateRSAParams() (map[string]string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	b64 := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

	// Convert E (int) to big-endian bytes.
	e := key.PublicKey.E
	var eBytes []byte
	for e > 0 {
		eBytes = append([]byte{byte(e & 0xff)}, eBytes...)
		e >>= 8
	}

	return map[string]string{
		"exponent": b64(eBytes),
		"modulus":  b64(key.PublicKey.N.Bytes()),
		"d":        b64(key.D.Bytes()),
		"p":        b64(key.Primes[0].Bytes()),
		"q":        b64(key.Primes[1].Bytes()),
		"dp":       b64(key.Precomputed.Dp.Bytes()),
		"dq":       b64(key.Precomputed.Dq.Bytes()),
		"inverseQ": b64(key.Precomputed.Qinv.Bytes()),
	}, nil
}

// sanitizeContainerName replaces characters not allowed in Docker container
// names with hyphens. Docker allows [a-zA-Z0-9][a-zA-Z0-9_.-].
func sanitizeContainerName(name string) string {
	var b []byte
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-' {
			b = append(b, c)
		} else {
			b = append(b, '-')
		}
	}
	return string(b)
}

// needsDockerFn returns true when the runner binary can't execute natively
// (e.g. NixOS where dynamically linked ELF binaries from other distros fail).
// Package-level var so tests can override.
var needsDockerFn = func() bool {
	_, err := os.Stat("/etc/NIXOS")
	return err == nil
}

// buildNativeCommand creates the exec.Cmd for running the runner directly.
func (p *Process) buildNativeCommand(ctx context.Context) *exec.Cmd {
	runBin := filepath.Join(p.runnerDir, "bin", "Runner.Listener")
	if _, err := os.Stat(runBin); err == nil {
		return exec.CommandContext(ctx, runBin, "run")
	}
	runScript := filepath.Join(p.runnerDir, "run.sh")
	if _, err := os.Stat(runScript); err == nil {
		return exec.CommandContext(ctx, runScript)
	}
	return nil
}

// buildDockerCommand creates the exec.Cmd that runs the runner inside a
// Docker container. Used on hosts where the runner binary can't execute
// natively (e.g. NixOS). The container gets:
//   - runner dir mounted at the same path (writable for _diag/)
//   - work dir mounted at the same path
//   - Docker socket for docker-in-docker
//   - host networking so the runner can reach the broker on localhost
//
// The container image must have Docker CLI installed so the runner can
// manage job containers (docker-in-docker via the mounted socket).
func (p *Process) buildDockerCommand(ctx context.Context) *exec.Cmd {
	containerName := sanitizeContainerName("ions-runner-" + p.name)

	// Remove any leftover container from a previous run that wasn't cleaned up
	// (e.g., if the process was killed). Ignore errors — it's fine if it doesn't exist.
	_ = exec.Command("docker", "rm", "-f", containerName).Run()

	listenerBin := filepath.Join(p.runnerDir, "bin", "Runner.Listener")

	// Install Docker CLI at container start, then exec the runner.
	// This avoids needing a custom image — plain ubuntu:24.04 works.
	bootScript := fmt.Sprintf(
		`mkdir -p %s/_diag && `+
			`apt-get update -qq && apt-get install -y -qq docker.io >/dev/null 2>&1 && exec %s run`,
		p.runnerDir,
		listenerBin,
	)

	args := []string{
		"run", "--rm",
		"--name", containerName,
		"--network", "host",
		"-v", p.runnerDir + ":" + p.runnerDir,
		"-v", p.workDir + ":" + p.workDir,
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-w", p.runnerDir,
	}

	for _, env := range runnerEnvVars() {
		args = append(args, "-e", env)
	}
	for _, env := range p.extraEnv {
		args = append(args, "-e", env)
	}

	args = append(args,
		"ubuntu:24.04",
		"bash", "-c", bootScript,
	)

	return exec.CommandContext(ctx, "docker", args...)
}
