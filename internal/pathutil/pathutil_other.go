//go:build !darwin

package pathutil

// EnsureGUIPath is a no-op on non-macOS platforms.
// The PATH environment is typically correct on Linux and Windows.
func EnsureGUIPath() error {
	return nil
}
