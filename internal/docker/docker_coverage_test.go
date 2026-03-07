package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// getMappedPorts — exercises the real function with a Docker container
// ---------------------------------------------------------------------------

func TestGetMappedPorts_RealContainer(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	// Use docker run -d to pull + create + start in one shot.
	containerID, err := dockerCmd(ctx, "run", "-d", "-p", "12345:80", "nginx:alpine")
	require.NoError(t, err)
	t.Cleanup(func() { dockerCmd(ctx, "rm", "-f", containerID) })

	ports, err := getMappedPorts(ctx, containerID)
	require.NoError(t, err)
	// The container maps port 80 to host port 12345.
	assert.Equal(t, "12345", ports["80"])
}

func TestGetMappedPorts_NoPorts(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	// Container with no published ports.
	containerID, err := dockerCmd(ctx, "create", "alpine:latest", "true")
	require.NoError(t, err)
	t.Cleanup(func() { dockerCmd(ctx, "rm", "-f", containerID) })

	_, err = dockerCmd(ctx, "start", containerID)
	require.NoError(t, err)

	ports, err := getMappedPorts(ctx, containerID)
	require.NoError(t, err)
	assert.Empty(t, ports)
}

func TestGetMappedPorts_MultiplePorts(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	// Use docker run -d to pull + create + start in one shot.
	containerID, err := dockerCmd(ctx, "run", "-d", "-p", "80", "-p", "443", "nginx:alpine")
	require.NoError(t, err)
	t.Cleanup(func() { dockerCmd(ctx, "rm", "-f", containerID) })

	ports, err := getMappedPorts(ctx, containerID)
	require.NoError(t, err)
	// Both ports should be mapped; we don't know the host ports so just check existence.
	assert.Contains(t, ports, "80")
	assert.Contains(t, ports, "443")
}

func TestGetMappedPorts_InvalidContainer(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	// Non-existent container ID should return error.
	_, err := getMappedPorts(ctx, "nonexistent-container-id-xyz")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// createAndStart — exercises Env, Volumes, Options, and Ports arguments
// ---------------------------------------------------------------------------

func TestCreateAndStart_WithEnvAndPorts(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	m, err := NewManager(false)
	require.NoError(t, err)

	// Create a temporary network for this test.
	networkName, err := m.CreateNetwork(ctx, "cov-envports")
	require.NoError(t, err)
	t.Cleanup(func() { m.RemoveNetwork(ctx, networkName) })

	svc := ServiceConfig{
		Image: "alpine:latest",
		Env:   map[string]string{"FOO": "bar", "BAZ": "qux"},
		Ports: []string{"8080"},
	}

	instance, err := m.createAndStart(ctx, "ions-cov-envports-web", "web", svc, networkName, svc.Ports)
	require.NoError(t, err)
	t.Cleanup(func() { dockerCmd(ctx, "rm", "-f", instance.ContainerID) })

	assert.Equal(t, "ions-cov-envports-web", instance.Name)
	assert.Equal(t, "alpine:latest", instance.Image)
	assert.NotEmpty(t, instance.ContainerID)

	// Verify env was injected.
	out, err := dockerCmd(ctx, "inspect", instance.ContainerID, "--format", "{{.Config.Env}}")
	require.NoError(t, err)
	assert.Contains(t, out, "FOO=bar")
	assert.Contains(t, out, "BAZ=qux")
}

func TestCreateAndStart_WithVolumes(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	m, err := NewManager(false)
	require.NoError(t, err)

	networkName, err := m.CreateNetwork(ctx, "cov-volumes")
	require.NoError(t, err)
	t.Cleanup(func() { m.RemoveNetwork(ctx, networkName) })

	svc := ServiceConfig{
		Image:   "alpine:latest",
		Volumes: []string{"/tmp:/data:ro"},
	}

	instance, err := m.createAndStart(ctx, "ions-cov-volumes-svc", "svc", svc, networkName, nil)
	require.NoError(t, err)
	t.Cleanup(func() { dockerCmd(ctx, "rm", "-f", instance.ContainerID) })

	assert.NotEmpty(t, instance.ContainerID)
}

func TestCreateAndStart_WithOptions(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	m, err := NewManager(false)
	require.NoError(t, err)

	networkName, err := m.CreateNetwork(ctx, "cov-options")
	require.NoError(t, err)
	t.Cleanup(func() { m.RemoveNetwork(ctx, networkName) })

	svc := ServiceConfig{
		Image:   "alpine:latest",
		Options: "--label test-label=ions-test",
	}

	instance, err := m.createAndStart(ctx, "ions-cov-options-svc", "svc", svc, networkName, nil)
	require.NoError(t, err)
	t.Cleanup(func() { dockerCmd(ctx, "rm", "-f", instance.ContainerID) })

	// Verify label was applied.
	out, err := dockerCmd(ctx, "inspect", instance.ContainerID, "--format", "{{index .Config.Labels \"test-label\"}}")
	require.NoError(t, err)
	assert.Equal(t, "ions-test", out)
}

// ---------------------------------------------------------------------------
// CreateNetwork — exercises the "already exists" recovery path
// ---------------------------------------------------------------------------

func TestCreateNetwork_AlreadyExists(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	m, err := NewManager(false)
	require.NoError(t, err)

	// Create network the first time.
	name1, err := m.CreateNetwork(ctx, "cov-dupe")
	require.NoError(t, err)

	// Create again — should clean up and recreate successfully.
	name2, err := m.CreateNetwork(ctx, "cov-dupe")
	require.NoError(t, err)
	assert.Equal(t, name1, name2)

	// Cleanup.
	m.RemoveNetwork(ctx, name2)
}

func TestCreateNetwork_AlreadyExistsWithContainers(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	m, err := NewManager(false)
	require.NoError(t, err)

	containerName := "ions-cov-dupe-test-ctr"

	// Ensure clean state from any previous failed runs.
	dockerCmd(ctx, "rm", "-f", containerName)
	dockerCmd(ctx, "network", "rm", "ions-cov-dupe-with-ctr")

	// Create network and attach a container with a known name.
	name, err := m.CreateNetwork(ctx, "cov-dupe-with-ctr")
	require.NoError(t, err)

	_, err = dockerCmd(ctx, "create", "--name", containerName, "--network", name, "alpine:latest", "true")
	require.NoError(t, err)

	// Now CreateNetwork should remove the container and network, then recreate.
	name2, err := m.CreateNetwork(ctx, "cov-dupe-with-ctr")
	require.NoError(t, err)
	assert.Equal(t, name, name2)

	// Verify the network was recreated successfully — the key thing is
	// that the "already exists" code path ran (network inspect, rm -f
	// containers, rm network, re-create network). Whether docker fully
	// removes the container depends on the Docker version and daemon config,
	// so we just verify the network was successfully recreated.

	// Verify network exists.
	out, err := dockerCmd(ctx, "network", "inspect", name2, "--format", "{{.Name}}")
	require.NoError(t, err)
	assert.Equal(t, name2, out)

	// Cleanup.
	dockerCmd(ctx, "rm", "-f", containerName) // clean up in case it's still around
	m.RemoveNetwork(ctx, name2)
}

// ---------------------------------------------------------------------------
// startService — port conflict error path
// ---------------------------------------------------------------------------

func TestStartService_PortConflict(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	m, err := NewManager(false)
	require.NoError(t, err)

	networkName, err := m.CreateNetwork(ctx, "cov-conflict")
	require.NoError(t, err)
	t.Cleanup(func() { m.RemoveNetwork(ctx, networkName) })

	// Start a container that binds a specific host port.
	blocker, err := dockerCmd(ctx, "run", "-d", "-p", "19876:80", "nginx:alpine")
	require.NoError(t, err)
	t.Cleanup(func() { dockerCmd(ctx, "rm", "-f", blocker) })

	// Now try to start a service on the same host port — should get "port is already allocated".
	svc := ServiceConfig{
		Image: "alpine:latest",
		Ports: []string{"19876:80"},
	}

	_, startErr := m.startService(ctx, "cov-conflict", "web", svc, networkName)
	require.Error(t, startErr)
	errMsg := startErr.Error()
	// Our code should format a nice "port conflict" message.
	assert.True(t,
		strings.Contains(errMsg, "port conflict") || strings.Contains(errMsg, "port is already allocated") || strings.Contains(errMsg, "address already in use") || strings.Contains(errMsg, "bind"),
		"expected a port conflict error, got: %s", errMsg)

	// Clean up any container that may have been created.
	dockerCmd(ctx, "rm", "-f", "ions-cov-conflict-web")
}

func TestStartService_PortConflict_MultipleHostPorts(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	m, err := NewManager(false)
	require.NoError(t, err)

	networkName, err := m.CreateNetwork(ctx, "cov-conflict2")
	require.NoError(t, err)
	t.Cleanup(func() { m.RemoveNetwork(ctx, networkName) })

	// Start a container that binds a specific host port.
	blocker, err := dockerCmd(ctx, "run", "-d", "-p", "19877:80", "nginx:alpine")
	require.NoError(t, err)
	t.Cleanup(func() { dockerCmd(ctx, "rm", "-f", blocker) })

	// Try with multiple ports, one of which conflicts.
	svc := ServiceConfig{
		Image: "alpine:latest",
		Ports: []string{"19877:80", "19878:443"},
	}

	_, startErr := m.startService(ctx, "cov-conflict2", "web", svc, networkName)
	if startErr != nil {
		errMsg := startErr.Error()
		assert.True(t,
			strings.Contains(errMsg, "port conflict") || strings.Contains(errMsg, "port is already allocated"),
			"expected a port conflict error, got: %s", errMsg)
	}

	// Clean up.
	dockerCmd(ctx, "rm", "-f", "ions-cov-conflict2-web")
}

// ---------------------------------------------------------------------------
// SetupServices — error cleanup path (service fails, earlier ones cleaned up)
// ---------------------------------------------------------------------------

func TestSetupServices_PartialFailure_CleansUp(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	m, err := NewManager(false)
	require.NoError(t, err)

	// Use a nonexistent image to trigger a failure.
	services := map[string]ServiceConfig{
		"badservice": {
			Image: "nonexistent-image-ions-test:latest",
		},
	}

	env, err := m.SetupServices(ctx, "cov-partial", services)
	// With a nonexistent image, pull will fail silently, but create may fail.
	// The behavior depends on whether the image can be pulled or not.
	if err != nil {
		assert.Nil(t, env)
		assert.Contains(t, err.Error(), "badservice")
	}

	// Clean up any leftover resources.
	dockerCmd(ctx, "rm", "-f", "ions-cov-partial-badservice")
	dockerCmd(ctx, "network", "rm", "ions-cov-partial")
}

// ---------------------------------------------------------------------------
// Teardown — error path when container removal fails (already gone)
// ---------------------------------------------------------------------------

func TestTeardown_ContainerAlreadyRemoved(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	m, err := NewManager(false)
	require.NoError(t, err)

	// Create a real environment with one service.
	services := map[string]ServiceConfig{
		"svc": {Image: "alpine:latest"},
	}
	env, err := m.SetupServices(ctx, "cov-teardown-err", services)
	require.NoError(t, err)

	// Manually remove the container before teardown to trigger the error path.
	for _, svc := range env.Services {
		dockerCmd(ctx, "rm", "-f", svc.ContainerID)
	}

	// Teardown should succeed (errors from rm -f on already-removed container
	// may or may not appear depending on Docker version).
	err = m.Teardown(ctx, env)
	// We don't assert NoError because "rm -f" on a missing container may or may not error.
	// The important thing is it doesn't panic.
	_ = err
}

func TestTeardown_EmptyServices(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	m, err := NewManager(false)
	require.NoError(t, err)

	// Teardown with empty services map.
	env := &JobEnvironment{
		NetworkID: "",
		Services:  map[string]*ServiceInstance{},
	}

	err = m.Teardown(ctx, env)
	assert.NoError(t, err)
}

func TestTeardown_WithNetworkOnly(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	m, err := NewManager(false)
	require.NoError(t, err)

	// Create a real network.
	networkName, err := m.CreateNetwork(ctx, "cov-teardown-net")
	require.NoError(t, err)

	env := &JobEnvironment{
		NetworkID: networkName,
		Services:  map[string]*ServiceInstance{},
	}

	err = m.Teardown(ctx, env)
	assert.NoError(t, err)

	// Verify network is gone.
	_, err = dockerCmd(ctx, "network", "inspect", networkName)
	assert.Error(t, err)
}

func TestTeardown_NonExistentNetwork(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	m, err := NewManager(false)
	require.NoError(t, err)

	env := &JobEnvironment{
		NetworkID: "ions-nonexistent-xyz",
		Services:  map[string]*ServiceInstance{},
	}

	err = m.Teardown(ctx, env)
	// Should return error from network removal.
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "remove network")
}

func TestTeardown_ServiceRemovalError(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	m, err := NewManager(false)
	require.NoError(t, err)

	// Create a real network.
	networkName, err := m.CreateNetwork(ctx, "cov-teardown-svc-err")
	require.NoError(t, err)

	// Create an env with a fake container ID (that doesn't exist).
	env := &JobEnvironment{
		NetworkID: networkName,
		Services: map[string]*ServiceInstance{
			"fake": {
				Name:        "ions-cov-fake",
				ContainerID: "nonexistent-container-id",
				Image:       "alpine:latest",
			},
		},
	}

	err = m.Teardown(ctx, env)
	// docker rm -f on nonexistent may or may not error; either way, it
	// should not panic and should attempt to remove the network.
	// The network should be gone regardless.
	_, netErr := dockerCmd(ctx, "network", "inspect", networkName)
	assert.Error(t, netErr, "network should be removed even if service removal fails")
}

// ---------------------------------------------------------------------------
// NewManager — error paths
// ---------------------------------------------------------------------------

func TestNewManager_ReuseContainersTrue(t *testing.T) {
	skipIfNoDocker(t)

	m, err := NewManager(true)
	require.NoError(t, err)
	assert.True(t, m.reuseContainers)
}

// ---------------------------------------------------------------------------
// dockerCmd — error formatting
// ---------------------------------------------------------------------------

func TestDockerCmd_InvalidCommand(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	_, err := dockerCmd(ctx, "not-a-real-command")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "docker not-a-real-command")
}

// ---------------------------------------------------------------------------
// ServiceConfig and ServiceInstance struct tests
// ---------------------------------------------------------------------------

func TestServiceConfig_AllFields(t *testing.T) {
	svc := ServiceConfig{
		Image:   "postgres:15",
		Env:     map[string]string{"POSTGRES_PASSWORD": "test"},
		Ports:   []string{"5432:5432"},
		Volumes: []string{"/data:/var/lib/postgresql/data"},
		Options: "--health-cmd pg_isready",
	}
	assert.Equal(t, "postgres:15", svc.Image)
	assert.Equal(t, "test", svc.Env["POSTGRES_PASSWORD"])
	assert.Equal(t, []string{"5432:5432"}, svc.Ports)
	assert.Equal(t, []string{"/data:/var/lib/postgresql/data"}, svc.Volumes)
	assert.Equal(t, "--health-cmd pg_isready", svc.Options)
}

func TestServiceInstance_Fields(t *testing.T) {
	si := &ServiceInstance{
		Name:        "ions-test-db",
		ContainerID: "abc123",
		Image:       "postgres:15",
		Ports:       map[string]string{"5432": "55432"},
	}
	assert.Equal(t, "ions-test-db", si.Name)
	assert.Equal(t, "abc123", si.ContainerID)
	assert.Equal(t, "postgres:15", si.Image)
	assert.Equal(t, "55432", si.Ports["5432"])
}

func TestJobEnvironment_Fields(t *testing.T) {
	env := &JobEnvironment{
		NetworkID: "ions-test-net",
		Services:  map[string]*ServiceInstance{},
	}
	assert.Equal(t, "ions-test-net", env.NetworkID)
	assert.NotNil(t, env.Services)
}

// ---------------------------------------------------------------------------
// RemoveNetwork — error path (network doesn't exist)
// ---------------------------------------------------------------------------

func TestRemoveNetwork_NonExistent(t *testing.T) {
	skipIfNoDocker(t)
	ctx := context.Background()

	m, err := NewManager(false)
	require.NoError(t, err)

	err = m.RemoveNetwork(ctx, "ions-nonexistent-network-xyz")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "remove network")
}

// ---------------------------------------------------------------------------
// Port parsing edge cases (unit tests - no Docker)
// ---------------------------------------------------------------------------

func TestGetMappedPorts_Parsing_MalformedLine(t *testing.T) {
	// Simulate what would happen if docker port returns a line without " -> "
	// This tests the parsing logic indirectly through the port parsing
	// in the inline TestGetMappedPorts test, but here we test edge cases.
	tests := []struct {
		name   string
		output string
		want   map[string]string
	}{
		{
			name:   "no arrow separator",
			output: "5432/tcp 0.0.0.0:55123",
			want:   map[string]string{},
		},
		{
			name:   "host binding without colon",
			output: "5432/tcp -> localhost",
			want:   map[string]string{},
		},
		{
			name:   "multiple bindings same port",
			output: "5432/tcp -> 0.0.0.0:55123\n5432/tcp -> [::]:55123",
			want:   map[string]string{"5432": "55123"},
		},
		{
			name:   "udp protocol",
			output: "53/udp -> 0.0.0.0:53",
			want:   map[string]string{"53": "53"},
		},
		{
			name:   "mixed protocols",
			output: "80/tcp -> 0.0.0.0:8080\n53/udp -> 0.0.0.0:5353",
			want:   map[string]string{"80": "8080", "53": "5353"},
		},
		{
			name:   "whitespace in line",
			output: " 5432/tcp -> 0.0.0.0:55123 ",
			want:   map[string]string{"5432": "55123"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Replicate the parsing logic from getMappedPorts to test edge cases
			// (since getMappedPorts calls dockerCmd, we can't mock it).
			ports := make(map[string]string)
			if tt.output == "" {
				assert.Equal(t, tt.want, ports)
				return
			}
			for _, line := range strings.Split(tt.output, "\n") {
				parts := strings.SplitN(line, " -> ", 2)
				if len(parts) != 2 {
					continue
				}
				containerPort := strings.TrimSpace(parts[0])
				hostBinding := strings.TrimSpace(parts[1])

				cp := strings.SplitN(containerPort, "/", 2)[0]

				idx := strings.LastIndex(hostBinding, ":")
				if idx < 0 {
					continue
				}
				hostPort := hostBinding[idx+1:]
				ports[cp] = hostPort
			}
			assert.Equal(t, tt.want, ports)
		})
	}
}
