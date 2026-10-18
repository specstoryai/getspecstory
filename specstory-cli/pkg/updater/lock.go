package updater

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The lock is beside the executable, so alternate HOME values still serialize
// the same installation. OS locks are released even if the worker is killed.
func lockInstallation(executable string) (*os.File, error) {
	path := filepath.Join(filepath.Dir(executable), "."+filepath.Base(executable)+".update.lock")
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("update lock is not a regular file: %s", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = tryLock(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
