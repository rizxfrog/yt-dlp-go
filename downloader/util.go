package downloader

import (
	"os"
	"path/filepath"
)

// createFile creates (or truncates) the file at path, ensuring parent dirs exist.
func createFile(path string) (*os.File, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return os.Create(path)
}
