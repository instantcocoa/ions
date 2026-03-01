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
	"time"
)

// ProcessConfig holds the configuration for launching a runner process.
type ProcessConfig struct {
	RunnerDir string // path to extracted runner (e.g. ~/.ions/runner/2.319.1/)
	BrokerURL string // http://localhost:{port}
	Name      string // runner name (default: "ions-runner")
	WorkDir   string // working directory for jobs
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
	configLocked bool // true while we hold configMu
	mu           sync.Mutex
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
	}, nil
}

// configMu serializes writes to the shared runner directory's config files.
// The runner resolves its root from the binary's location, so config files
// must live next to the binary. We hold the lock through Configure+Start
// to ensure each runner process reads its own config before the next one
// overwrites it.
var configMu sync.Mutex

// Configure writes the runner config files to the shared runner directory.
// The caller must call Start() promptly after Configure() — the config lock
// is held until the runner process has started and loaded its config.
func (p *Process) Configure(ctx context.Context) error {
	configMu.Lock()
	p.mu.Lock()
	p.configLocked = true
	p.mu.Unlock()
	// Lock is released in Start() after the runner process has launched.

	// .runner — main config telling the runner where to connect.
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
		return fmt.Errorf("writing .runner: %w", err)
	}

	// .credentials — OAuth credentials pointing to the broker's token endpoint.
	credentials := map[string]any{
		"scheme": "OAuth",
		"data": map[string]string{
			"clientId":         "00000000-0000-0000-0000-000000000000",
			"authorizationUrl": p.brokerURL + "/_apis/oauth2/token",
			"oAuthEndpointUrl": p.brokerURL + "/_apis/oauth2/token",
		},
	}
	if err := writeJSONFile(filepath.Join(p.runnerDir, ".credentials"), credentials); err != nil {
		return fmt.Errorf("writing .credentials: %w", err)
	}

	// .credentials_rsaparams — the runner needs a valid RSA key to sign JWTs
	// for the OAuth token exchange. Generate a real key pair.
	rsaParams, err := generateRSAParams()
	if err != nil {
		return fmt.Errorf("generating RSA key: %w", err)
	}
	if err := writeJSONFile(filepath.Join(p.runnerDir, ".credentials_rsaparams"), rsaParams); err != nil {
		return fmt.Errorf("writing .credentials_rsaparams: %w", err)
	}

	// Ensure work directory exists.
	return os.MkdirAll(p.workDir, 0o755)
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

// Start launches the runner process using run.sh.
// Must be called after Configure(). Releases the config lock after the
// runner has had time to read its config files.
func (p *Process) Start(ctx context.Context) error {
	// Release the config lock after the runner has loaded config files.
	// The runner reads config synchronously during Main() startup, which
	// takes ~200ms. We give it 2 seconds to be safe.
	defer func() {
		go func() {
			time.Sleep(2 * time.Second)
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

	runScript := filepath.Join(p.runnerDir, "run.sh")
	if _, err := os.Stat(runScript); err != nil {
		// Fall back to bin/Runner.Listener.
		runScript = filepath.Join(p.runnerDir, "bin", "Runner.Listener")
		if _, err := os.Stat(runScript); err != nil {
			return fmt.Errorf("neither run.sh nor bin/Runner.Listener found in %s", p.runnerDir)
		}
	}

	cmd := exec.CommandContext(ctx, runScript)
	if filepath.Base(runScript) == "Runner.Listener" {
		cmd.Args = append(cmd.Args, "run")
	}
	cmd.Dir = p.runnerDir
	cmd.Env = append(os.Environ(), runnerEnvVars()...)

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

// Stop gracefully shuts down the runner process.
// Sends SIGINT first, waits up to 10 seconds, then sends SIGKILL.
func (p *Process) Stop() error {
	p.mu.Lock()
	cmd := p.cmd
	done := p.done
	p.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	// Send interrupt signal.
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		// Process may already be dead.
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return nil
		}
		// If interrupt fails, try kill directly.
		return cmd.Process.Kill()
	}

	// Wait up to 10 seconds for graceful shutdown.
	select {
	case <-done:
		return nil
	case <-time.After(10 * time.Second):
		return cmd.Process.Kill()
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
