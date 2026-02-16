//go:build !darwin

package wrapper

// GetParentWindowID returns empty string on non-Darwin systems
func GetParentWindowID() string {
	return ""
}

// GetCGWindowID returns 0 on non-Darwin systems
func GetCGWindowID() int {
	return 0
}
