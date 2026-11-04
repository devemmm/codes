//go:build darwin || linux

package session

import (
	"fmt"
	"os"
	"syscall"
)

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("create private session directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("session path must be a real directory: %s", path)
	}
	if err := verifySessionDirectoryOwner(info, path); err != nil {
		return fmt.Errorf("session directory is not owned by the running user: %s", path)
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	openedInfo, err := dir.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, openedInfo) {
		return fmt.Errorf("session directory changed while opening: %s", path)
	}
	if info.Mode().Perm()&0077 != 0 {
		if err := dir.Chmod(0700); err != nil {
			return fmt.Errorf("restrict session directory permissions: %w", err)
		}
		info, err = dir.Stat()
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode().Perm()&0077 != 0 || verifySessionDirectoryOwner(info, path) != nil {
			return fmt.Errorf("session directory remains insecure after permission correction: %s", path)
		}
		pathInfo, err := os.Lstat(path)
		if err != nil || !os.SameFile(info, pathInfo) {
			return fmt.Errorf("session directory changed during permission correction: %s", path)
		}
	}
	return nil
}

func verifySessionDirectoryOwner(info os.FileInfo, path string) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || uint32(stat.Uid) != uint32(os.Geteuid()) {
		return fmt.Errorf("session directory owner mismatch: %s", path)
	}
	return nil
}
