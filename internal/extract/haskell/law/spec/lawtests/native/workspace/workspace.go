// Package workspace is the one production handler of assayxport.contract's
// Workspace ability, bound in lawspec.edn (native-bindings :handlers), so
// every unit that uses Workspace gets this code and no copy of it
// (S1.j calibration; throwaway workspace code).
package workspace

import "os"

// Files takes away and gives back read permission (lawspec.host has no
// permission operation). Writing files is lawspec.host's FileSystem
// (createDirectories + writeBytes, LawSpec 0.32.0).
type Files struct{}

// NewWorkspace makes the handler.
func NewWorkspace() *Files { return &Files{} }

// Revoke makes path unreadable: mode 000 (for a directory, also not
// listable or enterable).
func (*Files) Revoke(path string) {
	if err := os.Chmod(path, 0); err != nil {
		panic(err)
	}
}

// Restore makes path readable again (0755: a directory must also be
// enterable, and a file reads the same either way).
func (*Files) Restore(path string) {
	if err := os.Chmod(path, 0o755); err != nil {
		panic(err)
	}
}
