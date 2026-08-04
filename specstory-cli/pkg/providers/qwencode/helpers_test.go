package qwencode

import (
	"os"
	"testing"
)

// writeFileForTest writes content to path, failing the test on error.
func writeFileForTest(t *testing.T, path string, content string) error {
	t.Helper()
	return os.WriteFile(path, []byte(content), 0o644)
}

// mkdirAllForTest creates a directory tree, failing the test on error.
func mkdirAllForTest(t *testing.T, path string) error {
	t.Helper()
	return os.MkdirAll(path, 0o755)
}
