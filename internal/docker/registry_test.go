package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegistryFromImage(t *testing.T) {
	tests := []struct {
		image    string
		expected string
	}{
		{"postgres:15", "docker.io"},
		{"library/postgres:15", "docker.io"},
		{"ubuntu", "docker.io"},
		{"ghcr.io/owner/repo:latest", "ghcr.io"},
		{"ghcr.io/owner/repo", "ghcr.io"},
		{"gcr.io/project/image:v1", "gcr.io"},
		{"123456789.dkr.ecr.us-east-1.amazonaws.com/my-repo:latest", "123456789.dkr.ecr.us-east-1.amazonaws.com"},
		{"registry.example.com/image", "registry.example.com"},
		{"registry.example.com:5000/image:tag", "registry.example.com:5000"},
		{"localhost/myimage", "localhost"},
		{"localhost:5000/myimage:v1", "localhost:5000"},
		{"myuser/myimage:latest", "docker.io"},
	}

	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			got := registryFromImage(tt.image)
			assert.Equal(t, tt.expected, got)
		})
	}
}
