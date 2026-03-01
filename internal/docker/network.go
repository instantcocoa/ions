package docker

import (
	"context"
	"fmt"
	"strings"
)

// CreateNetwork creates a Docker bridge network for a job.
// If a network with the same name already exists (from a previous failed run),
// it removes any connected containers and the network first.
func (m *Manager) CreateNetwork(ctx context.Context, jobID string) (string, error) {
	name := fmt.Sprintf("ions-%s", jobID)
	_, err := dockerCmd(ctx, "network", "create", name)
	if err != nil && strings.Contains(err.Error(), "already exists") {
		// Clean up leftover network from previous run.
		// First remove any containers still attached.
		out, _ := dockerCmd(ctx, "network", "inspect", name, "--format", "{{range .Containers}}{{.Name}} {{end}}")
		for _, cname := range strings.Fields(out) {
			_, _ = dockerCmd(ctx, "rm", "-f", cname)
		}
		_, _ = dockerCmd(ctx, "network", "rm", name)
		_, err = dockerCmd(ctx, "network", "create", name)
	}
	if err != nil {
		return "", fmt.Errorf("create network %s: %w", name, err)
	}
	return name, nil
}

// RemoveNetwork removes a Docker network.
func (m *Manager) RemoveNetwork(ctx context.Context, networkName string) error {
	_, err := dockerCmd(ctx, "network", "rm", networkName)
	if err != nil {
		return fmt.Errorf("remove network %s: %w", networkName, err)
	}
	return nil
}
