package docker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ServiceConfig mirrors the workflow service definition.
type ServiceConfig struct {
	Image       string
	Credentials *RegistryCredentials
	Env         map[string]string
	Ports       []string // "5432:5432" or "5432" format
	Volumes     []string
	Options     string
}

// RegistryCredentials holds authentication for a container registry.
type RegistryCredentials struct {
	Username string
	Password string
}

// SetupServices starts all service containers for a job.
func (m *Manager) SetupServices(ctx context.Context, jobID string, services map[string]ServiceConfig) (*JobEnvironment, error) {
	networkName, err := m.CreateNetwork(ctx, jobID)
	if err != nil {
		return nil, err
	}

	env := &JobEnvironment{
		NetworkID: networkName,
		Services:  make(map[string]*ServiceInstance),
	}

	for name, svc := range services {
		instance, err := m.startService(ctx, jobID, name, svc, networkName)
		if err != nil {
			// Clean up anything we already started.
			_ = m.Teardown(ctx, env)
			return nil, fmt.Errorf("service %s: %w", name, err)
		}
		env.Services[name] = instance
	}

	return env, nil
}

func (m *Manager) startService(ctx context.Context, jobID, name string, svc ServiceConfig, networkName string) (*ServiceInstance, error) {
	// Authenticate with the registry if credentials are provided.
	if svc.Credentials != nil && svc.Credentials.Username != "" {
		if err := dockerLogin(ctx, svc.Image, svc.Credentials); err != nil {
			return nil, fmt.Errorf("registry login: %w", err)
		}
	}

	// Pull image (best-effort — may already be cached).
	_, _ = dockerCmd(ctx, "pull", svc.Image)

	containerName := fmt.Sprintf("ions-%s-%s", jobID, name)

	instance, err := m.createAndStart(ctx, containerName, name, svc, networkName, svc.Ports)
	if err != nil && strings.Contains(err.Error(), "port is already allocated") {
		// Provide a helpful error with the conflicting port(s).
		conflictPorts := make([]string, 0)
		for _, p := range svc.Ports {
			if strings.Contains(p, ":") {
				hostPort := strings.SplitN(p, ":", 2)[0]
				conflictPorts = append(conflictPorts, hostPort)
			}
		}
		return nil, fmt.Errorf("port conflict: host port(s) %s already in use\n  hint: stop the conflicting container(s) or service, e.g.:\n    docker ps --format '{{.Names}}\\t{{.Ports}}' | grep -E '%s'",
			strings.Join(conflictPorts, ", "),
			strings.Join(conflictPorts, "|"))
	}
	if err != nil {
		return nil, err
	}

	return instance, nil
}

func (m *Manager) createAndStart(ctx context.Context, containerName, serviceName string, svc ServiceConfig, networkName string, ports []string) (*ServiceInstance, error) {
	args := []string{"create",
		"--name", containerName,
		"--network", networkName,
		"--network-alias", serviceName,
	}

	for k, v := range svc.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	for _, p := range ports {
		args = append(args, "-p", p)
	}

	for _, v := range svc.Volumes {
		args = append(args, "-v", v)
	}

	if svc.Options != "" {
		args = append(args, strings.Fields(svc.Options)...)
	}

	args = append(args, svc.Image)

	containerID, err := dockerCmd(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("create container: %w", err)
	}

	if _, err := dockerCmd(ctx, "start", containerID); err != nil {
		_, _ = dockerCmd(ctx, "rm", "-f", containerID)
		return nil, fmt.Errorf("start container: %w", err)
	}

	mappedPorts, err := getMappedPorts(ctx, containerID)
	if err != nil {
		mappedPorts = make(map[string]string)
	}

	return &ServiceInstance{
		Name:        containerName,
		ContainerID: containerID,
		Image:       svc.Image,
		Ports:       mappedPorts,
	}, nil
}

// getMappedPorts parses `docker port` output to extract host port mappings.
// Output format: "5432/tcp -> 0.0.0.0:55123"
func getMappedPorts(ctx context.Context, containerID string) (map[string]string, error) {
	out, err := dockerCmd(ctx, "port", containerID)
	if err != nil {
		return nil, err
	}

	ports := make(map[string]string)
	if out == "" {
		return ports, nil
	}

	for _, line := range strings.Split(out, "\n") {
		// Expected: "5432/tcp -> 0.0.0.0:55123"
		parts := strings.SplitN(line, " -> ", 2)
		if len(parts) != 2 {
			continue
		}
		containerPort := strings.TrimSpace(parts[0]) // "5432/tcp"
		hostBinding := strings.TrimSpace(parts[1])   // "0.0.0.0:55123"

		// Extract just the port number from container side.
		cp := strings.SplitN(containerPort, "/", 2)[0]

		// Extract host port from "0.0.0.0:55123" or "[::]:55123".
		idx := strings.LastIndex(hostBinding, ":")
		if idx < 0 {
			continue
		}
		hostPort := hostBinding[idx+1:]

		ports[cp] = hostPort
	}

	return ports, nil
}

// dockerLogin authenticates with a container registry using docker login.
// The password is passed via stdin to avoid it appearing in process listings.
func dockerLogin(ctx context.Context, image string, creds *RegistryCredentials) error {
	registry := registryFromImage(image)
	cmd := exec.CommandContext(ctx, "docker", "login",
		"--username", creds.Username,
		"--password-stdin",
		registry,
	)
	cmd.Stdin = strings.NewReader(creds.Password)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker login to %s: %w\n%s", registry, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// registryFromImage extracts the registry hostname from a Docker image reference.
// For images like "ghcr.io/owner/repo:tag" it returns "ghcr.io".
// For Docker Hub images like "postgres:15" or "library/postgres" it returns
// "docker.io" (the default registry).
func registryFromImage(image string) string {
	// Strip tag/digest.
	ref := image
	if atIdx := strings.Index(ref, "@"); atIdx >= 0 {
		ref = ref[:atIdx]
	}
	if colonIdx := strings.LastIndex(ref, ":"); colonIdx >= 0 {
		// Only strip if it's a tag (not part of a port like registry:5000/image).
		after := ref[colonIdx+1:]
		if !strings.Contains(after, "/") {
			ref = ref[:colonIdx]
		}
	}

	// If there's a slash and the first component looks like a hostname
	// (contains a dot or colon, or is "localhost"), use it as registry.
	if slashIdx := strings.Index(ref, "/"); slashIdx >= 0 {
		firstPart := ref[:slashIdx]
		if strings.Contains(firstPart, ".") || strings.Contains(firstPart, ":") || firstPart == "localhost" {
			return firstPart
		}
	}

	return "docker.io"
}

// Teardown stops and removes all containers and the network.
// When reuseContainers is true, containers and network are kept.
// Teardown is idempotent — already-removed resources are silently ignored.
func (m *Manager) Teardown(ctx context.Context, env *JobEnvironment) error {
	if env == nil {
		return nil
	}

	if m.reuseContainers {
		return nil
	}

	var firstErr error
	for _, svc := range env.Services {
		if _, err := dockerCmd(ctx, "rm", "-f", svc.ContainerID); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("remove container %s: %w", svc.Name, err)
			}
		}
	}

	if env.NetworkID != "" {
		if err := m.RemoveNetwork(ctx, env.NetworkID); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}
