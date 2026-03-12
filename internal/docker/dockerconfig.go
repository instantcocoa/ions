package docker

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type DockerConfig struct {
	Auths map[string]DockerAuthEntry `json:"auths"`
}

type DockerAuthEntry struct {
	Auth string `json:"auth"`
}

func LoadDockerConfig() (*DockerConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}
	return LoadDockerConfigFrom(filepath.Join(home, ".docker", "config.json"))
}

func LoadDockerConfigFrom(path string) (*DockerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cfg DockerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *DockerConfig) LookupCredentials(image string) *RegistryCredentials {
	if c == nil || len(c.Auths) == 0 {
		return nil
	}
	registry := registryFromImage(image)
	candidates := []string{registry}
	if registry == "docker.io" {
		candidates = append(candidates, "https://index.docker.io/v1/", "index.docker.io")
	} else {
		candidates = append(candidates, "https://"+registry, "https://"+registry+"/v1/")
	}
	for _, candidate := range candidates {
		if entry, ok := c.Auths[candidate]; ok {
			creds := decodeAuth(entry.Auth)
			if creds != nil {
				return creds
			}
		}
	}
	return nil
}

func decodeAuth(encoded string) *RegistryCredentials {
	if encoded == "" {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return nil
	}
	return &RegistryCredentials{Username: parts[0], Password: parts[1]}
}
