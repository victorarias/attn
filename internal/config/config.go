package config

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
		configPath = filepath.Join(attnDir(), "config.json")
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

// ReloadForTesting reloads configuration from disk. Exported for tests that
// manipulate ATTN_PROFILE or ATTN_CONFIG_PATH between subtests.
func ReloadForTesting() {
	loadConfig()
}

var profileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,15}$`)

// Profile returns the active profile name (from ATTN_PROFILE), or "" for the
// default profile. Invalid profile names return "" — callers that need to
// validate should use ValidateProfile.
func Profile() string {
	raw := strings.TrimSpace(os.Getenv("ATTN_PROFILE"))
	if raw == "" {
		return ""
	}
	normalized := strings.ToLower(raw)
	if !profileNamePattern.MatchString(normalized) {
		return ""
	}
	return normalized
}

// ValidateProfile returns an error if ATTN_PROFILE is set to an invalid name.
// Use this from CLI entry points to fail loudly on typos.
func ValidateProfile() error {
	raw := os.Getenv("ATTN_PROFILE")
	if err := ValidateProfileName(raw); err != nil {
		return fmt.Errorf("invalid ATTN_PROFILE=%q: must match ^[a-z0-9][a-z0-9-]{0,15}$", strings.TrimSpace(raw))
	}
	return nil
}

// ProfileLabel returns a human-readable profile name ("default" for empty).
func ProfileLabel() string {
	if p := Profile(); p != "" {
		return p
	}
	return "default"
}

// DeepLinkScheme returns the macOS URL scheme the running profile's .app
// is registered under. Default → "attn", dev → "attn-dev". Must stay in
// sync with the `deep-link.desktop.schemes` entries in:
//   - app/src-tauri/tauri.conf.json         (prod)
//   - app/src-tauri/tauri.dev.conf.json     (dev)
//
// Used by the CLI wrapper so `attn` in a dev-scoped shell opens
// attn-dev.app, not attn.app.
func DeepLinkScheme() string {
	if p := Profile(); p == "dev" {
		return "attn-dev"
	}
	return "attn"
}

// ValidateProfileName validates a profile name against the same rules
// Profile()/ValidateProfile() apply, without consulting the environment.
// Use this when you have a profile name from a non-env source (e.g. a
// CLI argument) and want to reuse the validation logic.
func ValidateProfileName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil
	}
	normalized := strings.ToLower(trimmed)
	if !profileNamePattern.MatchString(normalized) {
		return fmt.Errorf("invalid profile name %q: must match ^[a-z0-9][a-z0-9-]{0,15}$", name)
	}
	return nil
}

// NormalizeProfileName validates and returns the canonical profile name.
// Use this at every persistence/wire boundary.
//
// Two normalization rules:
//
//  1. Lowercase + trim, so the value the remote daemon sees in
//     $ATTN_PROFILE matches the value stored in the local DB — Profile()
//     on the remote lowercases, so writing a mixed-case form here would
//     split data dirs (~/.attn-DEV referenced in scripts vs ~/.attn-dev
//     written by the daemon).
//
//  2. The literal "default" maps to "". WSPortForProfile and
//     DataDirForProfile already treat "default" as the default profile;
//     hub helpers (remoteBinaryName, ATTN_PROFILE export, log/data dir
//     scripts) do not. Letting "default" reach those would build
//     ~/.local/bin/attn-default and ~/.attn-default/ on the remote while
//     reusing port 9849 — colliding with any real default-profile daemon
//     on the same host. Canonicalizing here keeps every downstream code
//     path on a single representation of "the default profile".
func NormalizeProfileName(name string) (string, error) {
	if err := ValidateProfileName(name); err != nil {
		return "", err
	}
	canonical := strings.ToLower(strings.TrimSpace(name))
	if canonical == "default" {
		canonical = ""
	}
	return canonical, nil
}

// attnDir returns the base directory for attn files. Profile-aware:
// default profile → ~/.attn, named profile → ~/.attn-<profile>.
func attnDir() string {
	home, err := os.UserHomeDir()
	base := "/tmp/.attn"
	if err == nil {
		base = filepath.Join(home, ".attn")
	}
	if p := Profile(); p != "" {
		return base + "-" + p
	}
	return base
}

// DataDir returns the resolved per-profile data directory.
func DataDir() string {
	return attnDir()
}

// DataDirForProfile computes the canonical data directory for a given
// profile name (without reading ATTN_PROFILE). Pass "" for the default
// profile. Callers use this to probe whether the *other* profile's
// daemon is running, for friendlier error messages.
func DataDirForProfile(profile string) string {
	home, err := os.UserHomeDir()
	base := "/tmp/.attn"
	if err == nil {
		base = filepath.Join(home, ".attn")
	}
	p := strings.ToLower(strings.TrimSpace(profile))
	if p == "" || p == "default" {
		return base
	}
	if !profileNamePattern.MatchString(p) {
		return base
	}
	return base + "-" + p
}

// SocketPathForProfile returns the default socket path for a given profile
// name, independent of the current process's ATTN_PROFILE. Used for
// cross-profile probing in error messages; does not consult env overrides
// or the config file.
func SocketPathForProfile(profile string) string {
	return filepath.Join(DataDirForProfile(profile), "attn.sock")
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
	suffix := ""
	if p := Profile(); p != "" {
		suffix = "-" + p
	}
	return filepath.Join(home, "."+binaryName+"-state"+suffix+".json")
}

// LogPath returns the log file path
func LogPath() string {
	return filepath.Join(attnDir(), "daemon.log")
}

// WSPort returns the WebSocket/HTTP port.
// Priority: ATTN_WS_PORT env var > per-profile default.
// Default profile → 9849. Named profile "dev" → 29849. Any other named profile
// gets a stable hash-derived port in [20000,29848] (reserving 29849 for "dev";
// the e2e port 19849 sits outside this range).
func WSPort() string {
	port := strings.TrimSpace(os.Getenv("ATTN_WS_PORT"))
	if port != "" {
		return port
	}
	return WSPortForProfile(Profile())
}

// WSPortForProfile returns the default WebSocket port for a given profile name,
// independent of the current process's ATTN_PROFILE / ATTN_WS_PORT. Pass "" for
// the default profile. Used by the hub to compute the right port to talk to a
// profile-scoped remote daemon.
func WSPortForProfile(profile string) string {
	p := strings.ToLower(strings.TrimSpace(profile))
	switch p {
	case "", "default":
		return "9849"
	case "dev":
		return "29849"
	default:
		if !profileNamePattern.MatchString(p) {
			return "9849"
		}
		return derivedProfilePort(p)
	}
}

// derivedProfilePort maps a profile name to a stable port in [20000,29848],
// reserving 29849 for "dev" so future named profiles never collide with it.
func derivedProfilePort(profile string) string {
	h := fnv.New32a()
	h.Write([]byte(profile))
	port := 20000 + int(h.Sum32()%9849)
	return fmt.Sprintf("%d", port)
}

// WSBindAddress returns the interface/address the HTTP server binds to.
func WSBindAddress() string {
	addr := strings.TrimSpace(os.Getenv("ATTN_WS_BIND"))
	if addr == "" {
		return "127.0.0.1"
	}
	return addr
}

// WSAuthToken returns the optional bearer token required for WebSocket access.
func WSAuthToken() string {
	return strings.TrimSpace(os.Getenv("ATTN_WS_AUTH_TOKEN"))
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
