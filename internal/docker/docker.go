package docker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Manager manages Docker containers for ions jobs.
type Manager struct {
	reuseContainers bool
	dockerConfig    *DockerConfig
}

// NewManager creates a Docker manager after verifying Docker is available.
func NewManager(reuseContainers bool) (*Manager, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, fmt.Errorf("docker not found in PATH: %w", err)
	}
	cmd := exec.Command("docker", "info")
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker daemon not running: %w", err)
	}
	dockerCfg, _ := LoadDockerConfig()
	return &Manager{reuseContainers: reuseContainers, dockerConfig: dockerCfg}, nil
}

// JobEnvironment holds the Docker resources for a job.
type JobEnvironment struct {
	NetworkID string
	Services  map[string]*ServiceInstance
}

// ServiceInstance tracks a running service container.
type ServiceInstance struct {
	Name        string
	ContainerID string
	Image       string
	Ports       map[string]string // container port -> host port
}

// dockerCmd runs a docker command and returns combined output.
func dockerCmd(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w\n%s", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
