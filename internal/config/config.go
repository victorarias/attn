package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

var binaryName string

func init() {
	binaryName = filepath.Base(os.Args[0])
	loadConfig()
}

// BinaryName returns the name of the running binary (e.g., "attn")
func BinaryName() string {
	return binaryName
}

// SetBinaryName overrides the binary name (for testing)
func SetBinaryName(name string) {
	binaryName = name
}

// Config file structure
type configFile struct {
	DBPath     string `json:"db_path"`
	SocketPath string `json:"socket_path"`
}

var (
	loadedConfig configFile
	configMu     sync.RWMutex
)

// loadConfig loads configuration from file
func loadConfig() {
	configMu.Lock()
	defer configMu.Unlock()

	// Reset to empty
	loadedConfig = configFile{}

	configPath := os.Getenv("ATTN_CONFIG_PATH")
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		configPath = filepath.Join(home, ".attn", "config.json")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return // Config file doesn't exist, use defaults
	}

	json.Unmarshal(data, &loadedConfig)
}

// reloadConfig reloads configuration (for testing)
func reloadConfig() {
	loadConfig()
}

// attnDir returns the base directory for attn files
func attnDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/.attn"
	}
	return filepath.Join(home, ".attn")
}

// DBPath returns the SQLite database path
// Priority: ATTN_DB_PATH env var > config file > default
func DBPath() string {
	// 1. Environment variable (highest priority)
	if envPath := os.Getenv("ATTN_DB_PATH"); envPath != "" {
		return envPath
	}

	// 2. Config file
	configMu.RLock()
	configPath := loadedConfig.DBPath
	configMu.RUnlock()
	if configPath != "" {
		return configPath
	}

	// 3. Default
	return filepath.Join(attnDir(), "attn.db")
}

// SocketPath returns the unix socket path
// Priority: ATTN_SOCKET_PATH env var > config file > default
func SocketPath() string {
	// 1. Environment variable (highest priority)
	if envPath := os.Getenv("ATTN_SOCKET_PATH"); envPath != "" {
		return envPath
	}

	// 2. Config file
	configMu.RLock()
	configPath := loadedConfig.SocketPath
	configMu.RUnlock()
	if configPath != "" {
		return configPath
	}

	// 3. Default
	return filepath.Join(attnDir(), "attn.sock")
}

// StatePath returns the legacy state file path (for migration/cleanup)
func StatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/." + binaryName + "-state.json"
	}
	return filepath.Join(home, "."+binaryName+"-state.json")
}

// LogPath returns the log file path
func LogPath() string {
	return filepath.Join(attnDir(), "daemon.log")
}

// PIDPath returns the PID file path (same directory as socket)
func PIDPath() string {
	socketPath := SocketPath()
	return filepath.Join(filepath.Dir(socketPath), "attn.pid")
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
