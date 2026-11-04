//go:build darwin || linux

package session

import (
	"fmt"
	"os"
	"syscall"
)

func restrictSecretKeyFile(path string) error {
	if err := os.Chmod(path, 0600); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	return validateSecretKeyFilePermissions(path, info)
}

func validateSecretKeyFilePermissions(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || uint32(stat.Uid) != uint32(os.Geteuid()) {
		return fmt.Errorf("secret-key file is not owned by the running user: %s", path)
	}
	if info.Mode().Perm()&0077 != 0 || info.Mode().Perm()&0400 == 0 {
		return fmt.Errorf("secret-key file must have mode 0600: %s", path)
	}
	return nil
}

func syncSecretKeyDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open secret-key directory: %w", err)
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return fmt.Errorf("sync secret-key directory: %w", err)
	}
	if err := dir.Close(); err != nil {
		return fmt.Errorf("close secret-key directory: %w", err)
	}
	return nil
}
