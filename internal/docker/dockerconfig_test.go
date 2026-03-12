package docker

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDockerConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	err := os.WriteFile(configPath, []byte(`{
		"auths": {
			"https://index.docker.io/v1/": {"auth": "`+base64.StdEncoding.EncodeToString([]byte("myuser:mypass"))+`"},
			"ghcr.io": {"auth": "`+base64.StdEncoding.EncodeToString([]byte("ghuser:ghtoken"))+`"}
		}
	}`), 0644)
	require.NoError(t, err)
	cfg, err := LoadDockerConfigFrom(configPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Len(t, cfg.Auths, 2)
}

func TestLoadDockerConfig_NotFound(t *testing.T) {
	cfg, err := LoadDockerConfigFrom("/nonexistent/config.json")
	assert.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestLookupCredentials_DockerHub(t *testing.T) {
	cfg := &DockerConfig{Auths: map[string]DockerAuthEntry{
		"https://index.docker.io/v1/": {Auth: base64.StdEncoding.EncodeToString([]byte("myuser:mypass"))},
	}}
	creds := cfg.LookupCredentials("postgres:15")
	require.NotNil(t, creds)
	assert.Equal(t, "myuser", creds.Username)
	assert.Equal(t, "mypass", creds.Password)
}

func TestLookupCredentials_GHCR(t *testing.T) {
	cfg := &DockerConfig{Auths: map[string]DockerAuthEntry{
		"ghcr.io": {Auth: base64.StdEncoding.EncodeToString([]byte("ghuser:ghtoken"))},
	}}
	creds := cfg.LookupCredentials("ghcr.io/owner/image:latest")
	require.NotNil(t, creds)
	assert.Equal(t, "ghuser", creds.Username)
}

func TestLookupCredentials_NoMatch(t *testing.T) {
	cfg := &DockerConfig{Auths: map[string]DockerAuthEntry{
		"ghcr.io": {Auth: base64.StdEncoding.EncodeToString([]byte("user:pass"))},
	}}
	assert.Nil(t, cfg.LookupCredentials("ecr.aws/image:latest"))
}

func TestLookupCredentials_NilConfig(t *testing.T) {
	var cfg *DockerConfig
	assert.Nil(t, cfg.LookupCredentials("postgres:15"))
}

func TestDecodeAuth(t *testing.T) {
	tests := []struct {
		name    string
		encoded string
		wantNil bool
		user    string
		pass    string
	}{
		{"valid", base64.StdEncoding.EncodeToString([]byte("user:pass")), false, "user", "pass"},
		{"password with colon", base64.StdEncoding.EncodeToString([]byte("user:pass:with:colons")), false, "user", "pass:with:colons"},
		{"empty", "", true, "", ""},
		{"no colon", base64.StdEncoding.EncodeToString([]byte("nocolon")), true, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := decodeAuth(tt.encoded)
			if tt.wantNil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, tt.user, result.Username)
				assert.Equal(t, tt.pass, result.Password)
			}
		})
	}
}
