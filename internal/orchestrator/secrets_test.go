package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSecretMasker_BasicMasking(t *testing.T) {
	masker := NewSecretMasker(map[string]string{
		"TOKEN": "abc123",
	})
	assert.Equal(t, "token is ***", masker.Mask("token is abc123"))
}

func TestSecretMasker_MultipleSecrets(t *testing.T) {
	masker := NewSecretMasker(map[string]string{
		"TOKEN":    "abc123",
		"PASSWORD": "hunter2",
	})
	result := masker.Mask("token=abc123 password=hunter2")
	assert.Equal(t, "token=*** password=***", result)
}

func TestSecretMasker_SecretAppearsMultipleTimes(t *testing.T) {
	masker := NewSecretMasker(map[string]string{
		"KEY": "secret",
	})
	result := masker.Mask("secret and secret again")
	assert.Equal(t, "*** and *** again", result)
}

func TestSecretMasker_EmptyValueNotMasked(t *testing.T) {
	masker := NewSecretMasker(map[string]string{
		"EMPTY": "",
		"TOKEN": "abc123",
	})
	result := masker.Mask("value is abc123")
	assert.Equal(t, "value is ***", result)
}

func TestSecretMasker_AddSecret(t *testing.T) {
	masker := NewSecretMasker(map[string]string{
		"TOKEN": "abc123",
	})
	// "abc123" is already masked, "newval" is not yet
	assert.Equal(t, "*** and newval", masker.Mask("abc123 and newval"))

	masker.AddSecret("newval")
	// Now both are masked
	assert.Equal(t, "*** and ***", masker.Mask("abc123 and newval"))
}

func TestSecretMasker_AddEmptySecret(t *testing.T) {
	masker := NewSecretMasker(map[string]string{})
	masker.AddSecret("")
	// Should not panic or mask everything
	assert.Equal(t, "hello world", masker.Mask("hello world"))
}

func TestSecretMasker_NoSecrets(t *testing.T) {
	masker := NewSecretMasker(map[string]string{})
	assert.Equal(t, "nothing to mask here", masker.Mask("nothing to mask here"))
}

func TestSecretMasker_NilSecrets(t *testing.T) {
	masker := NewSecretMasker(nil)
	assert.Equal(t, "pass through", masker.Mask("pass through"))
}

func TestSecretMasker_MultiLineText(t *testing.T) {
	masker := NewSecretMasker(map[string]string{
		"DB_PASS": "s3cret",
		"API_KEY": "key-xyz",
	})
	input := `line 1: connecting with s3cret
line 2: using api key-xyz
line 3: no secrets here
line 4: s3cret again`
	expected := `line 1: connecting with ***
line 2: using api ***
line 3: no secrets here
line 4: *** again`
	assert.Equal(t, expected, masker.Mask(input))
}

func TestSecretMasker_OverlappingSecrets(t *testing.T) {
	// When one secret is a substring of another, the longer one should be masked first
	masker := NewSecretMasker(map[string]string{
		"SHORT": "abc",
		"LONG":  "abcdef",
	})
	result := masker.Mask("value: abcdef")
	assert.Equal(t, "value: ***", result)
}

func TestSecretMasker_SecretContainsMaskString(t *testing.T) {
	masker := NewSecretMasker(map[string]string{
		"WEIRD": "***secret",
	})
	result := masker.Mask("the value is ***secret")
	assert.Equal(t, "the value is ***", result)
}
