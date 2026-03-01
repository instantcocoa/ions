package orchestrator

import (
	"strings"
	"sync"
)

// SecretMasker replaces secret values in text with "***".
type SecretMasker struct {
	mu       sync.Mutex
	secrets  []string
	replacer *strings.Replacer
}

// NewSecretMasker creates a masker from a map of secret name -> value.
// Only non-empty values are masked.
func NewSecretMasker(secrets map[string]string) *SecretMasker {
	m := &SecretMasker{}
	for _, v := range secrets {
		if v != "" {
			m.secrets = append(m.secrets, v)
		}
	}
	m.buildReplacer()
	return m
}

// Mask replaces all secret values in s with "***".
func (m *SecretMasker) Mask(s string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.replacer == nil {
		return s
	}
	return m.replacer.Replace(s)
}

// AddSecret adds a new secret value to mask.
func (m *SecretMasker) AddSecret(value string) {
	if value == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets = append(m.secrets, value)
	m.buildReplacer()
}

// buildReplacer rebuilds the internal replacer from current secrets.
// Must be called with mu held.
func (m *SecretMasker) buildReplacer() {
	if len(m.secrets) == 0 {
		m.replacer = nil
		return
	}
	// Sort by length descending so longer secrets are replaced first,
	// preventing partial matches when one secret is a substring of another.
	sorted := make([]string, len(m.secrets))
	copy(sorted, m.secrets)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && len(sorted[j]) > len(sorted[j-1]); j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}

	pairs := make([]string, 0, len(sorted)*2)
	for _, s := range sorted {
		pairs = append(pairs, s, "***")
	}
	m.replacer = strings.NewReplacer(pairs...)
}
