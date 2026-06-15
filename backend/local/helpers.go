package local

import (
	"io"
	"os"
)

// readFileBytes reads a file from disk (used by the git backend for working-
// tree content).
func readFileBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// readAll copies r into w.
func readAll(w io.Writer, r io.Reader) (int64, error) {
	return io.Copy(w, r)
}
