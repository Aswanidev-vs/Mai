package models

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateConfigBytesDetectsNestedUnknownKeys(t *testing.T) {
	warnings := ValidateConfigBytes([]byte("audio:\n  sample_rate: 16000\n  mystery_knob: 7\n"))
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "audio.mystery_knob")
}

func TestShippedConfigsHaveNoUnknownKeys(t *testing.T) {
	for _, path := range []string{"../../config.yaml", "../../config.example.yaml"} {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Empty(t, ValidateConfigBytes(data), "unexpected ignored keys in %s", path)
	}
}
