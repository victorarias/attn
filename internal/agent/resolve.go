package agent

import (
	"os"
	"strings"
)

// resolveExec resolves an executable path by checking:
// 1. Environment variable override
// 2. Configured value (from settings)
// 3. Default fallback
func resolveExec(envVar, configured, fallback string) string {
	if envVar != "" {
		if v := strings.TrimSpace(os.Getenv(envVar)); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(configured); v != "" {
		return v
	}
	return fallback
}
