//go:build darwin || linux

package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnsurePrivateDirectoryTightensExistingPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions")
	require.NoError(t, os.Mkdir(path, 0755))
	require.NoError(t, EnsurePrivateDirectory(path))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	require.Equal(t, os.FileMode(0700), info.Mode().Perm())
}

func TestEnsurePrivateDirectoryRejectsSymlink(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "sessions")
	require.NoError(t, os.Symlink(target, link))
	require.Error(t, EnsurePrivateDirectory(link))
}
