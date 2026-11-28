package adapters

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileWriteRejectsShortAndOutsidePaths(t *testing.T) {
	root := t.TempDir()
	tool := &FileWriteTool{AllowedRoots: []string{root}}

	for _, path := range []string{"", "a", "~/", filepath.Join(root, "..", "outside.txt")} {
		params, err := json.Marshal(map[string]any{"path": path, "content": "x"})
		require.NoError(t, err)
		result, err := tool.Execute(context.Background(), params)
		require.NoError(t, err, "a bad path must return an error result, not panic")
		assert.Error(t, result.Error, "path %q", path)
	}
}

func TestFileWriteHonoursConfirmation(t *testing.T) {
	root := t.TempDir()
	tool := &FileWriteTool{
		AllowedRoots: []string{root},
		Confirm:      func(string, string) bool { return false },
	}
	target := filepath.Join(root, "note.txt")
	params, err := json.Marshal(map[string]any{"path": target, "content": "hello"})
	require.NoError(t, err)

	result, err := tool.Execute(context.Background(), params)
	require.NoError(t, err)
	assert.NoError(t, result.Error)
	assert.Contains(t, result.Output, "cancelled")
	_, statErr := os.Stat(target)
	assert.True(t, os.IsNotExist(statErr), "an unconfirmed write must not touch disk")
}

func TestShellToolAllowlist(t *testing.T) {
	tool := &ShellTool{Allowed: []string{"echo"}}
	params, err := json.Marshal(map[string]any{"command": "format c:"})
	require.NoError(t, err)

	result, err := tool.Execute(context.Background(), params)
	require.NoError(t, err)
	assert.Error(t, result.Error)
}
