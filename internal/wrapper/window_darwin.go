//go:build darwin

package wrapper

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// GetParentWindowID returns the iTerm2 session ID if available
// Format: "w0t0p0:UUID" which encodes window/tab/pane indices
func GetParentWindowID() string {
	return os.Getenv("ITERM_SESSION_ID")
}

// GetCGWindowID returns the macOS CGWindowID for the terminal window containing this session.
// Uses AppleScript to ask iTerm for the window ID of the session matching our UUID.
// Returns 0 if not available.
func GetCGWindowID() int {
	sessionID := os.Getenv("ITERM_SESSION_ID")
	if sessionID == "" {
		return 0
	}

	// Extract UUID from "w0t0p0:UUID" format
	parts := strings.SplitN(sessionID, ":", 2)
	if len(parts) != 2 {
		return 0
	}
	uuid := parts[1]

	script := `tell application "iTerm2"
    repeat with w in windows
        repeat with t in tabs of w
            repeat with s in sessions of t
                if unique id of s is "` + uuid + `" then
                    return id of w
                end if
            end repeat
        end repeat
    end repeat
    return 0
end tell`

	cmd := exec.Command("osascript", "-e", script)
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	result := strings.TrimSpace(string(output))
	windowID, err := strconv.Atoi(result)
	if err != nil {
		return 0
	}
	return windowID
}
