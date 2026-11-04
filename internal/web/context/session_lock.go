package context

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devemmm/codes/internal/session"
)

func (s *Store) withSessionLock(sessionID string, fn func() error) (result error) {
	if sessionID == "" {
		return fn()
	}
	if err := session.EnsurePrivateDirectory(s.sessionsPath); err != nil {
		return fmt.Errorf("validate session directory: %w", err)
	}
	shard := sha256.Sum256([]byte(sessionID))
	lockPath := filepath.Join(s.sessionsPath, fmt.Sprintf(".session-lock-%02x", shard[0]))
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open session lock: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close session lock: %w", err))
		}
	}()
	if err := file.Chmod(0600); err != nil {
		return fmt.Errorf("restrict session lock permissions: %w", err)
	}
	unlock, err := lockSessionFile(file)
	if err != nil {
		return fmt.Errorf("lock session file: %w", err)
	}
	defer func() {
		if err := unlock(); err != nil {
			result = errors.Join(result, fmt.Errorf("unlock session file: %w", err))
		}
	}()
	return fn()
}
