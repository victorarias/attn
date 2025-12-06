package config

import (
	"os"
	"path/filepath"
)

var binaryName string

func init() {
	binaryName = filepath.Base(os.Args[0])
}

// BinaryName returns the name of the running binary (e.g., "cm", "attn")
func BinaryName() string {
	return binaryName
}

// SetBinaryName overrides the binary name (for testing)
func SetBinaryName(name string) {
	binaryName = name
}

// SocketPath returns the unix socket path
func SocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/." + binaryName + ".sock"
	}
	return filepath.Join(home, "."+binaryName+".sock")
}

// StatePath returns the state file path
func StatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/." + binaryName + "-state.json"
	}
	return filepath.Join(home, "."+binaryName+"-state.json")
}

// LogPath returns the log file path
func LogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/" + binaryName + ".log"
	}
	return filepath.Join(home, "."+binaryName, "daemon.log")
}

// Log levels
const (
	LogError = iota
	LogWarn
	LogInfo
	LogDebug
	LogTrace
)

// DebugLevel returns the debug level from DEBUG env var
func DebugLevel() int {
	switch os.Getenv("DEBUG") {
	case "trace":
		return LogTrace
	case "debug":
		return LogDebug
	case "info":
		return LogInfo
	case "warn":
		return LogWarn
	case "1", "true":
		return LogDebug
	default:
		return LogError
	}
}
