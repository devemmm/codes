package session

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/gorilla/securecookie"
	"github.com/rs/zerolog/log"
)

// GenerateSecretKey generates a new secret key for sessions
// Returns the key and a boolean indicating if the key was generated
func GenerateSecretKey(filePath string) ([]byte, bool) {
	key, err := os.ReadFile(filePath)
	if err == nil {
		return key, false
	}

	key = securecookie.GenerateRandomKey(32)
	if key == nil {
		log.Fatal().Msg("Failed to generate a new key for sessions")
	}

	err = os.WriteFile(filePath, key, 0600)
	if err != nil {
		log.Fatal().Err(err).Msgf("Failed to save the key to %s", filePath)
	}

	return key, true
}

// LoadOrCreateSecretKey loads a 32-byte key or atomically publishes a newly
// generated one. The temporary file is fully written before the hard link
// makes it visible at filePath, and the link operation never replaces an
// existing key.
func LoadOrCreateSecretKey(filePath string) ([]byte, error) {
	dir := filepath.Dir(filePath)
	if err := EnsurePrivateDirectory(dir); err != nil {
		return nil, err
	}
	key, err := readSecretKey(filePath)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	key = securecookie.GenerateRandomKey(32)
	if len(key) != 32 {
		return nil, fmt.Errorf("generate 32-byte secret key")
	}
	if allZero(key) {
		return nil, fmt.Errorf("generated secret key has invalid contents")
	}

	temp, err := os.CreateTemp(dir, ".secret-key-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary secret-key file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if err = temp.Chmod(0600); err == nil {
		err = restrictSecretKeyFile(tempPath)
	}
	if err == nil {
		_, err = temp.Write(key)
	}
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("write temporary secret-key file: %w", err)
	}

	if err = os.Link(tempPath, filePath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return readSecretKey(filePath)
		}
		return nil, fmt.Errorf("publish secret-key file: %w", err)
	}
	if err := syncSecretKeyDirectory(dir); err != nil {
		return nil, err
	}

	// Validate the published file too, so callers never use a key that was
	// changed by another process or an unexpected filesystem condition.
	return readSecretKey(filePath)
}

func readSecretKey(filePath string) ([]byte, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("secret-key file must be a private regular file: %s", filePath)
	}
	if err := validateSecretKeyFilePermissions(filePath, info); err != nil {
		return nil, err
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	openedInfo, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf("secret-key file changed while opening: %s", filePath)
	}

	key, err := io.ReadAll(io.LimitReader(f, 33))
	if err != nil {
		return nil, err
	}
	if len(key) != 32 || allZero(key) {
		return nil, fmt.Errorf("secret-key file must contain a valid 32-byte key: %s", filePath)
	}
	return key, nil
}

func allZero(key []byte) bool {
	for _, b := range key {
		if b != 0 {
			return false
		}
	}
	return true
}
