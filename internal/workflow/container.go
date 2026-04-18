package workflow

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Container can be a string (image name) or full object.
type Container struct {
	Image       string            `yaml:"image"`
	Credentials *Credentials      `yaml:"credentials,omitempty"`
	Env         EnvMap            `yaml:"env,omitempty"`
	Ports       []string          `yaml:"ports,omitempty"`
	Volumes     []string          `yaml:"volumes,omitempty"`
	Options     string            `yaml:"options,omitempty"`
	Entrypoint  string            `yaml:"entrypoint,omitempty"`
	Args        string            `yaml:"args,omitempty"`
}

// NormalizeImageRef strips the `docker://` URI scheme that GitHub Actions
// accepts in `container:`, `services:`, and step `uses:` fields. The real
// Docker CLI does not understand the `docker://` prefix and will interpret
// it as a registry hostname, so we strip it before any pull/login call.
func NormalizeImageRef(image string) string {
	const scheme = "docker://"
	if len(image) >= len(scheme) && image[:len(scheme)] == scheme {
		return image[len(scheme):]
	}
	return image
}

// Credentials holds authentication for a container registry.
type Credentials struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// UnmarshalYAML handles both string (image name only) and mapping forms.
func (c *Container) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		c.Image = NormalizeImageRef(value.Value)
		return nil
	case yaml.MappingNode:
		// Use an alias type to avoid infinite recursion
		type containerAlias Container
		var alias containerAlias
		if err := value.Decode(&alias); err != nil {
			return fmt.Errorf("Container: failed to decode mapping: %w", err)
		}
		*c = Container(alias)
		c.Image = NormalizeImageRef(c.Image)
		return nil
	default:
		return fmt.Errorf("Container: expected string or mapping, got %v", value.Kind)
	}
}
