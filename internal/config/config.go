package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

var binaryName string

func init() {
	binaryName = filepath.Base(os.Args[0])
	// No loadConfig() here: package init runs before any TestMain, so an eager
	// load would trip attnDir()'s go-test backstop. Loading is lazy instead.
}

// BinaryName returns the name of the running binary (e.g., "attn")
func BinaryName() string {
	return binaryName
}

// SetBinaryName overrides the binary name (for testing)
func SetBinaryName(name string) {
	binaryName = name
}

type configFile struct {
	DBPath     string `json:"db_path"`
	SocketPath string `json:"socket_path"`
}

var (
	loadedConfig configFile
	configLoaded bool
	configMu     sync.RWMutex
)

// ensureConfigLoaded lazily loads config.json on first use. Callers that read
// loadedConfig (DBPath, SocketPath) must call this first.
func ensureConfigLoaded() {
	configMu.RLock()
	loaded := configLoaded
	configMu.RUnlock()
	if !loaded {
		loadConfig()
	}
}

// loadConfig loads configuration from file
func loadConfig() {
	configMu.Lock()
	defer configMu.Unlock()

	loadedConfig = configFile{}
	configLoaded = true

	configPath := os.Getenv("ATTN_CONFIG_PATH")
	if configPath == "" {
		configPath = filepath.Join(attnDir(), "config.json")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return // missing config file → defaults
	}

	json.Unmarshal(data, &loadedConfig)
}

// reloadConfig reloads configuration (for testing)
func reloadConfig() {
	loadConfig()
}

// ReloadForTesting reloads configuration from disk, for tests that change
// ATTN_PROFILE or ATTN_CONFIG_PATH between subtests.
func ReloadForTesting() {
	loadConfig()
}

var profileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,15}$`)

// Profile returns the active profile name (from ATTN_PROFILE), or "" for the
// default profile; invalid names return "" (use ValidateProfile to reject them).
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

// DeepLinkScheme returns the macOS URL scheme the running profile's .app is
// registered under (DeepLinkSchemeForProfile applied to the active profile).
func DeepLinkScheme() string {
	return DeepLinkSchemeForProfile(Profile())
}

// normalizeProfileForDerivation lowercases/trims and maps "default" and any
// invalid name to "" — the single rule shared by all per-profile derivations.
func normalizeProfileForDerivation(profile string) string {
	p := strings.ToLower(strings.TrimSpace(profile))
	if p == "" || p == "default" || !profileNamePattern.MatchString(p) {
		return ""
	}
	return p
}

// BundleIdentifierForProfile returns the macOS bundle identifier for a profile
// (com.attn.manager[.<profile>]). Single source of truth: Makefile, Rust build,
// and harness derive from this via `attn profile resolve`.
func BundleIdentifierForProfile(profile string) string {
	p := normalizeProfileForDerivation(profile)
	if p == "" {
		return "com.attn.manager"
	}
	return "com.attn.manager." + p
}

// AppNameForProfile returns the .app bundle folder name (without ".app") for a
// profile: attn, or attn-<profile>. Must match the Tauri productName.
func AppNameForProfile(profile string) string {
	p := normalizeProfileForDerivation(profile)
	if p == "" {
		return "attn"
	}
	return "attn-" + p
}

// AppPathForProfile returns the installed bundle path (~/Applications/<name>.app)
// for a profile.
func AppPathForProfile(profile string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	return filepath.Join(home, "Applications", AppNameForProfile(profile)+".app")
}

// DeepLinkSchemeForProfile returns the macOS URL scheme a profile's .app
// registers (attn, or attn-<profile>): a distinct scheme per bundle so macOS
// never cross-routes a spawn deep link to the wrong app.
func DeepLinkSchemeForProfile(profile string) string {
	p := normalizeProfileForDerivation(profile)
	if p == "" {
		return "attn"
	}
	return "attn-" + p
}

// ValidateProfileName validates a profile name against the same rules
// Profile()/ValidateProfile() apply, without consulting the environment.
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

// NormalizeProfileName validates and returns the canonical profile name; use
// it at every persistence/wire boundary. Lowercase+trim (a mixed-case form
// would split data dirs on the remote), and the literal "default" maps to ""
// (letting it through would build ~/.attn-default while reusing port 9849).
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

// attnDir returns the base directory for attn files — the single chokepoint
// every derived path funnels through, so ATTN_DATA_DIR (highest precedence,
// above profile derivation) and the test backstop live in one place.
func attnDir() string {
	if override := strings.TrimSpace(os.Getenv("ATTN_DATA_DIR")); override != "" {
		return filepath.Clean(override)
	}
	requireExplicitDataDirUnderTest()
	return defaultAttnDir(Profile())
}

// requireExplicitDataDirUnderTest panics if a test binary resolves the data
// dir without ATTN_DATA_DIR set — a presence-only check (immune to symlinks
// and odd HOMEs) backstopping the 2026-07-18 production-DB loss. Fix by
// setting ATTN_DATA_DIR to a temp dir; never redirect HOME — see
// docs/plans/2026-07-18-db-loss-mitigation.md.
func requireExplicitDataDirUnderTest() {
	if testing.Testing() && strings.TrimSpace(os.Getenv("ATTN_DATA_DIR")) == "" {
		panic("config: ATTN_DATA_DIR is not set under go test — tests must never resolve the real data dir. " +
			"Set ATTN_DATA_DIR to a temp dir (os.Setenv in a package TestMain, or t.Setenv per-test). " +
			"Never redirect HOME to work around this: see docs/plans/2026-07-18-db-loss-mitigation.md")
	}
}

// ScopeTestEnvironment sets ATTN_DATA_DIR to dataDir and clears ATTN_DB_PATH,
// ATTN_SOCKET_PATH, ATTN_CONFIG_PATH, and ATTN_PLUGIN_DIR; call it from a
// package TestMain instead of setting ATTN_DATA_DIR directly. Those four
// overrides outrank the attnDir() chokepoint, so an inherited one could still
// route test I/O at the real database — same incident class as
// docs/plans/2026-07-18-db-loss-mitigation.md. Test-only: panics outside
// testing.Testing().
func ScopeTestEnvironment(dataDir string) {
	if !testing.Testing() {
		panic("config.ScopeTestEnvironment is test-only")
	}
	os.Setenv("ATTN_DATA_DIR", dataDir)
	os.Unsetenv("ATTN_DB_PATH")
	os.Unsetenv("ATTN_SOCKET_PATH")
	os.Unsetenv("ATTN_CONFIG_PATH")
	os.Unsetenv("ATTN_PLUGIN_DIR")
}

// defaultAttnDir computes the profile-aware default data dir from the real
// $HOME, ignoring ATTN_DATA_DIR and the test backstop entirely.
func defaultAttnDir(profile string) string {
	home, err := os.UserHomeDir()
	base := "/tmp/.attn"
	if err == nil {
		base = filepath.Join(home, ".attn")
	}
	if profile != "" {
		return base + "-" + profile
	}
	return base
}

// DataDir returns the resolved per-profile data directory.
func DataDir() string {
	return attnDir()
}

// PluginDir returns the installed plugin directory for the active profile.
// Priority: ATTN_PLUGIN_DIR env var > per-profile data directory default.
func PluginDir() string {
	if envPath := strings.TrimSpace(os.Getenv("ATTN_PLUGIN_DIR")); envPath != "" {
		return envPath
	}
	return filepath.Join(attnDir(), "plugins")
}

// AppsDir returns the app artifact store for the active profile: one directory
// per app, one directory per version inside it, named by the version's content
// hash. The shared TypeScript apply typechecks with lives here too — it is
// build machinery, not data, and it belongs beside what it builds.
func AppsDir() string {
	return filepath.Join(attnDir(), "apps")
}

// DataDirForProfile computes the canonical data directory for a profile name
// ("" for default), without reading ATTN_PROFILE. Deliberately bypasses the
// attnDir() chokepoint — no ATTN_DATA_DIR override, no go-test backstop — so
// cross-profile probing works; tests must never write through this path.
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

// SocketPathForProfile returns the default socket path for a profile name.
// Same chokepoint bypass as DataDirForProfile; tests must never write here.
func SocketPathForProfile(profile string) string {
	return filepath.Join(DataDirForProfile(profile), "attn.sock")
}

// DBPath returns the SQLite database path.
// Priority: ATTN_DB_PATH env var > config file > default.
func DBPath() string {
	if envPath := os.Getenv("ATTN_DB_PATH"); envPath != "" {
		return envPath
	}

	ensureConfigLoaded()
	configMu.RLock()
	configPath := loadedConfig.DBPath
	configMu.RUnlock()
	if configPath != "" {
		return configPath
	}

	return filepath.Join(attnDir(), "attn.db")
}

// SocketPath returns the unix socket path.
// Priority: ATTN_SOCKET_PATH env var > config file > default.
func SocketPath() string {
	if envPath := os.Getenv("ATTN_SOCKET_PATH"); envPath != "" {
		return envPath
	}

	ensureConfigLoaded()
	configMu.RLock()
	configPath := loadedConfig.SocketPath
	configMu.RUnlock()
	if configPath != "" {
		return configPath
	}

	return filepath.Join(attnDir(), "attn.sock")
}

// ValidateDaemonIsolation rejects configurations whose runtime root
// (socket/PID/workers) is split from the profile's data dir while still using
// its default DB — an auxiliary daemon on that combination can reap live sessions.
func ValidateDaemonIsolation(socketPath string) error {
	socketDir, err := comparableDaemonIsolationPath(filepath.Dir(strings.TrimSpace(socketPath)))
	if err != nil {
		return fmt.Errorf("resolve daemon socket root: %w", err)
	}
	profileDataDir, err := comparableDaemonIsolationPath(DataDir())
	if err != nil {
		return fmt.Errorf("resolve profile data dir: %w", err)
	}
	if socketDir == profileDataDir {
		return nil
	}

	dbPath, err := comparableDaemonIsolationPath(DBPath())
	if err != nil {
		return fmt.Errorf("resolve daemon DB path: %w", err)
	}
	defaultDBPath, err := comparableDaemonIsolationPath(filepath.Join(profileDataDir, "attn.db"))
	if err != nil {
		return fmt.Errorf("resolve profile DB path: %w", err)
	}
	if dbPath != defaultDBPath {
		return nil
	}

	return fmt.Errorf(
		"refusing to start daemon with socket root %q while DB path still resolves to the %s profile store %q; set ATTN_DB_PATH to an isolated database or use ATTN_PROFILE",
		socketDir,
		ProfileLabel(),
		defaultDBPath,
	)
}

func comparableDaemonIsolationPath(path string) (string, error) {
	return CanonicalRuntimePath(path)
}

// CanonicalRuntimePath returns one absolute representation of a runtime path,
// resolving symlinks through the deepest existing ancestor. Routing checks must
// compare through this, never raw env/config strings (which may be CWD-relative).
func CanonicalRuntimePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)

	existing := absolute
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(existing)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return absolute, nil
		}
		missing = append(missing, filepath.Base(existing))
		existing = parent
	}
}

// StatePath returns the legacy state file path (for migration/cleanup).
// Bypasses the attnDir() chokepoint; tests must never write through this path.
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

// AppSupportDirForProfile returns ~/Library/Application Support/<bundle id> —
// mirrors Tauri's BaseDirectory.AppLocalData resolution on macOS, where the
// frontend's disk-based debug logs land.
func AppSupportDirForProfile(profile string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	return filepath.Join(home, "Library", "Application Support", BundleIdentifierForProfile(profile))
}

// AppSupportDir returns the app-support directory for the active profile.
func AppSupportDir() string {
	return AppSupportDirForProfile(Profile())
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

// WSPortForProfile returns the default WebSocket port for a profile name ("" for
// default), independent of the current process's ATTN_PROFILE / ATTN_WS_PORT.
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

// profileFNV hashes a profile name with FNV-1a (32-bit), shared by every
// per-profile port derivation.
func profileFNV(profile string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(profile))
	return h.Sum32()
}

// derivedProfilePort maps a profile name to a stable port in [20000,29848],
// reserving 29849 for "dev" so future named profiles never collide with it.
func derivedProfilePort(profile string) string {
	port := 20000 + int(profileFNV(profile)%9849)
	return fmt.Sprintf("%d", port)
}

// E2EDaemonPortForProfile returns the e2e-harness throwaway-daemon WS port:
// default → 19849, named profiles hash into [30000,30999] — disjoint from prod
// 9849, dev 29849, the real-profile band [20000,29848], and Vite 1420/1421.
func E2EDaemonPortForProfile(profile string) string {
	p := normalizeProfileForDerivation(profile)
	if p == "" {
		return "19849"
	}
	return fmt.Sprintf("%d", 30000+int(profileFNV(p)%1000))
}

// E2EVitePortForProfile returns the e2e Vite dev-server port: default → 1421,
// named profiles hash into [31000,31999]; strictPort makes collisions fail loudly.
func E2EVitePortForProfile(profile string) string {
	p := normalizeProfileForDerivation(profile)
	if p == "" {
		return "1421"
	}
	return fmt.Sprintf("%d", 31000+int(profileFNV(p)%1000))
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

// BrowserHostToken returns the per-profile secret used to authenticate the
// packaged app as the browser-control host. The Tauri shell creates this file
// with owner-only permissions before it starts or connects to the daemon.
func BrowserHostToken() string {
	if token := strings.TrimSpace(os.Getenv("ATTN_BROWSER_HOST_TOKEN")); token != "" {
		return token
	}
	data, err := os.ReadFile(filepath.Join(attnDir(), "browser-host-token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
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

// DefaultPprofPort is the loopback port used when ATTN_PPROF is enabled without
// an explicit port (e.g. ATTN_PPROF=1).
const DefaultPprofPort = 6060

// PprofAddr reports whether the opt-in diagnostics endpoint (pprof + expvar) is
// enabled and the loopback address to bind. Always 127.0.0.1 — any host in the
// value is ignored on purpose.
//
//	unset / "0" / "off" / "false" / "no" → disabled
//	"1" / "on" / "true" / "yes"          → enabled on DefaultPprofPort
//	"<port>" or ":<port>" or "host:port" → enabled on that port (loopback)
func PprofAddr() (addr string, enabled bool) {
	raw := strings.TrimSpace(os.Getenv("ATTN_PPROF"))
	if raw == "" {
		return "", false
	}
	switch strings.ToLower(raw) {
	case "0", "off", "false", "no":
		return "", false
	case "1", "on", "true", "yes":
		return fmt.Sprintf("127.0.0.1:%d", DefaultPprofPort), true
	}
	// Force loopback so the endpoint can never be exposed off the machine.
	portPart := raw
	if i := strings.LastIndex(portPart, ":"); i >= 0 {
		portPart = portPart[i+1:]
	}
	if p, err := strconv.Atoi(portPart); err == nil && p > 0 && p <= 65535 {
		return fmt.Sprintf("127.0.0.1:%d", p), true
	}
	return "", false
}
