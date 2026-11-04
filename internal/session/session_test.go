package session

import (
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadOrCreateSecretKeyConcurrentAndPersistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flash-signing.key")
	const callers = 24
	keys := make([][]byte, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			keys[i], errs[i] = LoadOrCreateSecretKey(path)
		}(i)
	}
	wg.Wait()
	for i := range keys {
		require.NoError(t, errs[i])
		require.Len(t, keys[i], 32)
		require.Equal(t, keys[0], keys[i])
	}

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular())
	require.Zero(t, info.Mode().Perm()&0077)
	loaded, err := LoadOrCreateSecretKey(path)
	require.NoError(t, err)
	require.Equal(t, keys[0], loaded)
}

func TestLoadOrCreateSecretKeyRejectsInvalidExistingKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flash-signing.key")
	require.NoError(t, os.WriteFile(path, []byte("short"), 0600))
	_, err := LoadOrCreateSecretKey(path)
	require.Error(t, err)
	require.Equal(t, []byte("short"), mustReadFile(t, path))
}

func TestLoadOrCreateSecretKeyRejectsInsecurePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flash-signing.key")
	require.NoError(t, os.WriteFile(path, []byte("12345678901234567890123456789012"), 0644))
	_, err := LoadOrCreateSecretKey(path)
	require.Error(t, err)
}

func TestLoadOrCreateSecretKeyConcurrentProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared", "flash-signing.key")
	const workers = 5
	outputs := make([]string, workers)
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := range outputs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestLoadOrCreateSecretKeyProcessHelper$")
			cmd.Env = append(os.Environ(), "CODES_KEY_HELPER=1", "CODES_KEY_PATH="+path)
			out, err := cmd.Output()
			outputs[i] = strings.TrimSpace(strings.SplitN(string(out), "PASS", 2)[0])
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	for _, key := range outputs {
		require.Len(t, key, 64)
		require.Equal(t, outputs[0], key)
	}
	key, err := LoadOrCreateSecretKey(path)
	require.NoError(t, err)
	require.Equal(t, outputs[0], hex.EncodeToString(key))
}

func TestLoadOrCreateSecretKeyProcessHelper(t *testing.T) {
	if os.Getenv("CODES_KEY_HELPER") != "1" {
		return
	}
	key, err := LoadOrCreateSecretKey(os.Getenv("CODES_KEY_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = os.Stdout.WriteString(hex.EncodeToString(key))
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}
