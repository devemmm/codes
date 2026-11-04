//go:build windows

package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestWindowsSecureSessionDirectoryUsesPrivateACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions")
	err := EnsurePrivateDirectory(path)
	if errors.Is(err, ErrSecureSessionsUnsupported) {
		return // The test volume is not NTFS; unsupported filesystems fail closed.
	}
	require.NoError(t, err)
	h, err := openDirectoryNoFollow(path, windows.READ_CONTROL)
	require.NoError(t, err)
	defer windows.CloseHandle(h)
	sid, err := currentUserSID()
	require.NoError(t, err)
	require.NoError(t, validateWindowsACL(h, sid))
	require.NoError(t, rejectReparsePoint(h))
	_, err = os.Stat(path)
	require.NoError(t, err)
}

func TestWindowsSessionDirectoryCorrectsExistingACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions")
	require.NoError(t, os.Mkdir(path, 0755))
	err := EnsurePrivateDirectory(path)
	if errors.Is(err, ErrSecureSessionsUnsupported) {
		t.Skip("test volume is not NTFS")
	}
	require.NoError(t, err)
	h, err := openDirectoryNoFollow(path, windows.READ_CONTROL)
	require.NoError(t, err)
	defer windows.CloseHandle(h)
	sid, err := currentUserSID()
	require.NoError(t, err)
	require.NoError(t, validateWindowsACL(h, sid))
}

func TestWindowsReparsePointRejected(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	require.NoError(t, os.Mkdir(target, 0700))
	link := filepath.Join(root, "sessions-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	err := EnsurePrivateDirectory(link)
	require.Error(t, err)
}

func TestWindowsSigningKeyUsesPrivateACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions", "flash-signing.key")
	key, err := LoadOrCreateSecretKey(path)
	if errors.Is(err, ErrSecureSessionsUnsupported) {
		t.Skip("test volume is not NTFS")
	}
	require.NoError(t, err)
	require.Len(t, key, 32)
	info, err := os.Lstat(path)
	require.NoError(t, err)
	require.NoError(t, validateSecretKeyFilePermissions(path, info))
}
