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
	runnerDir   string // effective root — points at instanceDir
	sharedDir   string // underlying install (symlink target)
	instanceDir string // per-process dir, cleaned up on Stop
	workDir     string
	brokerURL   string
	name        string

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
// It validates required fields and applies defaults. Crucially, each
// Process gets its own *instance directory* cloned from the shared runner
// install — `.runner`, `.credentials`, and `_diag/` live in the instance,
// while the big read-only subtrees (`bin/`, `externals/`, `*.sh`) are
// symlinked back to the install. This lets multiple Processes (within a
// single ions run AND across parallel ions-run invocations) execute
// without racing on config files.
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

	instanceDir, err := materializeInstance(cfg.RunnerDir)
	if err != nil {
		return nil, fmt.Errorf("runner instance: %w", err)
	}

	workDir := cfg.WorkDir
	if workDir == "" {
		workDir = filepath.Join(instanceDir, "_work")
	}

	return &Process{
		runnerDir:   instanceDir,
		sharedDir:   cfg.RunnerDir,
		instanceDir: instanceDir,
		workDir:     workDir,
		brokerURL:   cfg.BrokerURL,
		name:        cfg.Name,
		extraEnv:    cfg.ExtraEnv,
	}, nil
}

// materializeInstance creates a fresh per-process runner directory that
// mirrors the shared install. The runner treats runnerDir as its
// RootFolder; everything it writes (.runner, .credentials, _diag/,
// _work/) lands inside the instance and is cleaned up on Stop.
//
// Subdirectories are recreated; their *files* are hardlinked to the
// shared install. Hardlinks (rather than symlinks) matter because .NET's
// Assembly.Location dereferences symlinks — so a symlinked Runner.Listener.dll
// resolves back to the shared path, and the runner would treat the
// shared dir as its RootFolder, defeating the isolation. Hardlinks
// preserve the instance path while sharing the inode, so storage cost
// is just directory-entry overhead (~1 MB for the full tree).
//
// If the shared dir doesn't exist yet (unit tests passing a stub path),
// return an empty instance so the caller can proceed.
func materializeInstance(sharedDir string) (string, error) {
	instancesRoot := filepath.Join(filepath.Dir(sharedDir), "_instances")
	if err := os.MkdirAll(instancesRoot, 0o755); err != nil {
		return "", err
	}
	instanceDir, err := os.MkdirTemp(instancesRoot, "i-")
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(sharedDir); err != nil {
		if os.IsNotExist(err) {
			return instanceDir, nil
		}
		return "", err
	}

	skip := map[string]bool{
		"_diag": true, "_work": true, "_instances": true,
		".runner": true, ".credentials": true, ".credentials_rsaparams": true,
	}
	err = filepath.Walk(sharedDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(sharedDir, path)
		if rel == "." {
			return nil
		}
		if skip[filepath.Base(rel)] && info.IsDir() {
			return filepath.SkipDir
		}
		if skip[filepath.Base(rel)] {
			return nil
		}
		dst := filepath.Join(instanceDir, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, linkErr := os.Readlink(path)
			if linkErr != nil {
				return linkErr
			}
			return os.Symlink(target, dst)
		}
		if err := os.Link(path, dst); err != nil {
			return fmt.Errorf("link %s: %w", rel, err)
		}
		return nil
	})
	if err != nil {
		_ = os.RemoveAll(instanceDir)
		return "", err
	}
	return instanceDir, nil
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
	defer p.cleanupInstance()

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

// cleanupInstance removes the per-process instance directory. Safe to
// call multiple times.
func (p *Process) cleanupInstance() {
	if p.instanceDir == "" {
		return
	}
	_ = os.RemoveAll(p.instanceDir)
	p.instanceDir = ""
}

// Dir returns the effective runner root directory for this Process (the
// per-process instance dir, not the shared install). Config files and
// work dirs live under this path.
func (p *Process) Dir() string { return p.runnerDir }

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
