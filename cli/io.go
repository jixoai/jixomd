package cli

import "os"

// readFile / writeFile are thin wrappers so tests can swap them if needed
// (e.g. to inject a VFS). The default uses the OS filesystem.
func readFile(path string) ([]byte, error)  { return os.ReadFile(path) }
func writeFile(path string, b []byte) error { return os.WriteFile(path, b, 0o644) }
