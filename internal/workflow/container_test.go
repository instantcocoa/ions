package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestContainer_StringImage(t *testing.T) {
	var c Container
	err := yaml.Unmarshal([]byte(`node:20`), &c)
	require.NoError(t, err)
	assert.Equal(t, "node:20", c.Image)
	assert.Nil(t, c.Credentials)
	assert.Nil(t, c.Env)
	assert.Nil(t, c.Ports)
	assert.Nil(t, c.Volumes)
	assert.Empty(t, c.Options)
}

func TestContainer_FullMapping(t *testing.T) {
	input := `
image: postgres:15
credentials:
  username: admin
  password: secret
env:
  POSTGRES_PASSWORD: postgres
  POSTGRES_DB: test
ports:
  - 5432:5432
volumes:
  - /data:/var/lib/postgresql/data
options: --health-cmd pg_isready
`
	var c Container
	err := yaml.Unmarshal([]byte(input), &c)
	require.NoError(t, err)
	assert.Equal(t, "postgres:15", c.Image)
	require.NotNil(t, c.Credentials)
	assert.Equal(t, "admin", c.Credentials.Username)
	assert.Equal(t, "secret", c.Credentials.Password)
	assert.Equal(t, map[string]string{
		"POSTGRES_PASSWORD": "postgres",
		"POSTGRES_DB":       "test",
	}, c.Env)
	assert.Equal(t, []string{"5432:5432"}, c.Ports)
	assert.Equal(t, []string{"/data:/var/lib/postgresql/data"}, c.Volumes)
	assert.Equal(t, "--health-cmd pg_isready", c.Options)
}

func TestContainer_ImageOnly(t *testing.T) {
	input := `
image: redis:7
`
	var c Container
	err := yaml.Unmarshal([]byte(input), &c)
	require.NoError(t, err)
	assert.Equal(t, "redis:7", c.Image)
}

func TestContainer_WithPorts(t *testing.T) {
	input := `
image: redis:7
ports:
  - 6379:6379
  - 6380:6380
`
	var c Container
	err := yaml.Unmarshal([]byte(input), &c)
	require.NoError(t, err)
	assert.Equal(t, "redis:7", c.Image)
	assert.Equal(t, []string{"6379:6379", "6380:6380"}, c.Ports)
}

func TestContainer_WithMultilineOptions(t *testing.T) {
	input := `
image: postgres:15
options: >-
  --health-cmd pg_isready
  --health-interval 10s
  --health-timeout 5s
`
	var c Container
	err := yaml.Unmarshal([]byte(input), &c)
	require.NoError(t, err)
	assert.Equal(t, "postgres:15", c.Image)
	assert.Contains(t, c.Options, "--health-cmd pg_isready")
	assert.Contains(t, c.Options, "--health-interval 10s")
}

func TestContainer_InvalidType(t *testing.T) {
	var c Container
	err := yaml.Unmarshal([]byte(`[a, b]`), &c)
	assert.Error(t, err)
}

func TestContainer_InServiceMap(t *testing.T) {
	input := `
postgres:
  image: postgres:15
  env:
    POSTGRES_PASSWORD: postgres
redis: redis:7
`
	// This tests the map[string]*Container usage pattern
	// We need to unmarshal manually since map values go through UnmarshalYAML
	type wrapper struct {
		Services map[string]*Container `yaml:",inline"`
	}
	// Actually, let's just test the map directly
	var services map[string]*Container
	err := yaml.Unmarshal([]byte(input), &services)
	require.NoError(t, err)

	require.Contains(t, services, "postgres")
	assert.Equal(t, "postgres:15", services["postgres"].Image)
	assert.Equal(t, "postgres", services["postgres"].Env["POSTGRES_PASSWORD"])

	require.Contains(t, services, "redis")
	assert.Equal(t, "redis:7", services["redis"].Image)
}
