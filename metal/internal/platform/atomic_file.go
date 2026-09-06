// Package platform provides host integration primitives for Metal.
package platform

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Write publishes data with an atomic rename and syncs the file and directory.
func Write(path string, data []byte, mode fs.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}

	temporaryFile, err := os.CreateTemp(directory, "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	defer func() {
		_ = temporaryFile.Close()
		_ = os.Remove(temporaryPath)
	}()

	if err := temporaryFile.Chmod(mode); err != nil {
		return fmt.Errorf("set temporary file mode: %w", err)
	}
	if _, err := temporaryFile.Write(data); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temporaryFile.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporaryFile.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish file: %w", err)
	}

	directoryFile, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open parent directory: %w", err)
	}
	defer directoryFile.Close()
	if err := directoryFile.Sync(); err != nil {
		return fmt.Errorf("sync parent directory: %w", err)
	}
	return nil
}
