package config

import (
	"encoding/json"
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
	// Deliberately does NOT call loadConfig() here: package init() runs
	// before any test's TestMain, so an eager load would hit attnDir()'s
	// go-test backstop before a package ever gets a chance to set
	// ATTN_DATA_DIR. Config loading is instead lazy — see
	// ensureConfigLoaded — triggered by the first call to a function that
	// actually needs it (DBPath, SocketPath), which by then runs inside a
	// test body/TestMain, not package init.
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
	configLoaded bool
	configMu     sync.RWMutex
)

// ensureConfigLoaded performs the first, lazy load of config.json, unless it
// has already been loaded (by this or an explicit reloadConfig() call).
// Callers that read loadedConfig (DBPath, SocketPath) must call this before
// reading it. Lazy rather than init()-time so the first load happens inside
// a test body/TestMain (after ATTN_DATA_DIR is set) rather than at package
// init, which runs before any TestMain and would otherwise trip the
// attnDir() test backstop unconditionally, in every package that merely
// imports config.
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

	// Reset to empty
	loadedConfig = configFile{}
	configLoaded = true

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

// DeepLinkScheme returns the macOS URL scheme the running profile's .app is
// registered under: default → "attn", dev → "attn-dev", agent7 → "attn-agent7".
// It is the per-profile authority (DeepLinkSchemeForProfile) applied to the
// active profile, so the scheme a profile's bundle registers at build time and
// the scheme the CLI opens at runtime can never diverge.
//
// Used by the CLI wrapper so `attn` in a profile-scoped shell opens that
// profile's app, never another profile's.
func DeepLinkScheme() string {
	return DeepLinkSchemeForProfile(Profile())
}

// normalizeProfileForDerivation lowercases/trims a profile name and maps the
// literal "default" and any invalid name to "" so every per-profile derivation
// helper (bundle id, app name, ports) shares exactly one rule.
func normalizeProfileForDerivation(profile string) string {
	p := strings.ToLower(strings.TrimSpace(profile))
	if p == "" || p == "default" || !profileNamePattern.MatchString(p) {
		return ""
	}
	return p
}

// BundleIdentifierForProfile returns the macOS bundle identifier for a profile:
// default → com.attn.manager, dev → com.attn.manager.dev, agent7 →
// com.attn.manager.agent7. Single source of truth — the Makefile, Rust build,
// and real-app harness all derive from this (via `attn profile resolve`) instead
// of re-encoding the mapping.
func BundleIdentifierForProfile(profile string) string {
	p := normalizeProfileForDerivation(profile)
	if p == "" {
		return "com.attn.manager"
	}
	return "com.attn.manager." + p
}

// AppNameForProfile returns the .app bundle folder name (without ".app") for a
// profile: default → attn, dev → attn-dev, agent7 → attn-agent7. Must match the
// Tauri productName the build produces.
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
// registers: default → attn, dev → attn-dev, agent7 → attn-agent7. Each profile
// bundle registers a distinct scheme so macOS never cross-routes a spawn deep
// link to the wrong app. The per-profile build (`make install PROFILE=<name>`)
// bakes this scheme into the bundle, and DeepLinkScheme() reports it at runtime,
// so the registered and opened schemes are derived from this one function.
func DeepLinkSchemeForProfile(profile string) string {
	p := normalizeProfileForDerivation(profile)
	if p == "" {
		return "attn"
	}
	return "attn-" + p
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

// attnDir returns the base directory for attn files. This is the single
// chokepoint every derived path (socket, DB, plugins, logs, PID, workers,
// ...) funnels through, so ATTN_DATA_DIR and the test backstop below only
// need to live in one place.
//
// Precedence:
//  1. ATTN_DATA_DIR env var, if set and non-empty — highest precedence,
//     above ATTN_PROFILE derivation. Returned verbatim (filepath.Clean'd).
//  2. Profile-aware default: ~/.attn, or ~/.attn-<profile> for a named
//     profile.
func attnDir() string {
	if override := strings.TrimSpace(os.Getenv("ATTN_DATA_DIR")); override != "" {
		return filepath.Clean(override)
	}
	requireExplicitDataDirUnderTest()
	return defaultAttnDir(Profile())
}

// requireExplicitDataDirUnderTest panics if called from a test binary
// (testing.Testing()) without ATTN_DATA_DIR set. This is the backstop for
// the 2026-07-18 incident where a daemon test resolved config.DataDir()
// straight to the real ~/.attn and destroyed the production database.
//
// It is a presence check only — no path comparison, no HOME inspection —
// so it can't be fooled by symlinks or unusual HOMEs, and it catches every
// package (including ones that don't exist yet) that forgets to scope its
// data dir.
//
// If you hit this panic: set ATTN_DATA_DIR to a temp dir, either in a
// package TestMain (os.Setenv, so it applies to the whole package) or in an
// individual test via t.Setenv for extra per-test isolation. Never redirect
// HOME to work around this — see docs/plans/2026-07-18-db-loss-mitigation.md.
func requireExplicitDataDirUnderTest() {
	if testing.Testing() && strings.TrimSpace(os.Getenv("ATTN_DATA_DIR")) == "" {
		panic("config: ATTN_DATA_DIR is not set under go test — tests must never resolve the real data dir. " +
			"Set ATTN_DATA_DIR to a temp dir (os.Setenv in a package TestMain, or t.Setenv per-test). " +
			"Never redirect HOME to work around this: see docs/plans/2026-07-18-db-loss-mitigation.md")
	}
}

// ScopeTestEnvironment sets ATTN_DATA_DIR to dataDir and clears
// ATTN_DB_PATH, ATTN_SOCKET_PATH, ATTN_CONFIG_PATH, and ATTN_PLUGIN_DIR.
// Call it from a package TestMain (or an individual test) instead of
// setting ATTN_DATA_DIR directly.
//
// Why clear the other four: DBPath, SocketPath, PluginDir, and the
// config-file path all check their own env-var override before ever
// reaching the attnDir() chokepoint, so ATTN_DATA_DIR alone does not bound
// them. A developer's shell can carry an inherited ATTN_DB_PATH pointing at
// the real ~/.attn/attn.db (e.g. from a scoped profile) even while
// ATTN_DATA_DIR is set to a temp dir for the current test run; without this
// clearing, that inherited override would still route test I/O at the real
// database. This is the same incident class documented in
// docs/plans/2026-07-18-db-loss-mitigation.md, one step removed: the
// backstop in requireExplicitDataDirUnderTest only guards attnDir() itself,
// not these four overrides that sit above it in precedence.
//
// It does not touch HOME or ATTN_PROFILE: ATTN_DATA_DIR already outranks
// profile derivation, and HOME is off-limits for test scoping (see the
// Decisions section of the plan above).
//
// Test-only: panics if called outside testing.Testing(), since it mutates
// process-global environment and must never be reachable from a production
// binary.
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
// $HOME, ignoring ATTN_DATA_DIR and the test backstop entirely. It is a pure
// function of (HOME, profile) with no I/O, so it's safe to call directly
// from tests that want to assert the default-derivation formula without
// tripping requireExplicitDataDirUnderTest.
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

// DataDirForProfile computes the canonical data directory for a given
// profile name (without reading ATTN_PROFILE). Pass "" for the default
// profile. Callers use this to probe whether the *other* profile's
// daemon is running, for friendlier error messages.
//
// This function deliberately bypasses the attnDir() chokepoint and therefore
// does not honor ATTN_DATA_DIR overrides or the go-test backstop. The
// *ForProfile helpers must probe other profiles' directories for cross-profile
// error messages and must not honor the current process's ATTN_DATA_DIR override.
// Tests must never write through this path.
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
//
// This function deliberately bypasses the attnDir() chokepoint and therefore
// does not honor ATTN_DATA_DIR overrides or the go-test backstop. It must not
// honor the current process's override. Tests must never write through this path.
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
	ensureConfigLoaded()
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
	ensureConfigLoaded()
	configMu.RLock()
	configPath := loadedConfig.SocketPath
	configMu.RUnlock()
	if configPath != "" {
		return configPath
	}

	// 3. Default
	return filepath.Join(attnDir(), "attn.sock")
}

// ValidateDaemonIsolation rejects daemon configurations that split the
// runtime root (socket/PID/workers) away from the active profile's data dir
// while still pointing at that profile's default durable store.
//
// That combination lets an auxiliary daemon reconcile the shared session DB
// against a different worker registry and mistakenly reap live sessions.
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
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

// StatePath returns the legacy state file path (for migration/cleanup).
//
// This function deliberately bypasses the attnDir() chokepoint and therefore
// does not honor ATTN_DATA_DIR overrides or the go-test backstop. Tests must
// never write through this path.
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

// AppSupportDirForProfile returns the macOS app-support directory a profile's
// frontend writes into: ~/Library/Application Support/<bundle identifier>.
// This mirrors Tauri's BaseDirectory.AppLocalData resolution on macOS, so it
// is the same directory the frontend's disk-based debug logs (see
// app/src/utils/terminalDiagnosticsLog.ts) land in. macOS only, matching the
// rest of this package's platform scope.
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

// profileFNV hashes a profile name with FNV-1a (32-bit). Shared by every
// per-profile port derivation so the algorithm lives in exactly one place.
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

// E2EDaemonPortForProfile returns the throwaway-daemon WS port the Playwright
// e2e harness should use for a profile. Default → 19849 (unchanged). Named
// profiles hash into [30000,30999] — disjoint from prod 9849, dev 29849, the
// real-profile band [20000,29848], and Vite 1420/1421 — so an e2e daemon never
// collides with a *real* daemon of the same profile.
func E2EDaemonPortForProfile(profile string) string {
	p := normalizeProfileForDerivation(profile)
	if p == "" {
		return "19849"
	}
	return fmt.Sprintf("%d", 30000+int(profileFNV(p)%1000))
}

// E2EVitePortForProfile returns the Vite dev-server port the e2e harness should
// use for a profile. Default → 1421 (unchanged). Named profiles hash into
// [31000,31999]. strictPort makes a rare cross-profile collision fail loudly.
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
// enabled and, if so, the loopback address to bind. It is strictly off unless
// ATTN_PPROF is set, and always binds 127.0.0.1 so it adds no remote attack
// surface (any host in the value is ignored on purpose).
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
	// Accept "host:port", ":port", or a bare port; ignore the host and force
	// loopback so the endpoint can never be exposed off the machine.
	portPart := raw
	if i := strings.LastIndex(portPart, ":"); i >= 0 {
		portPart = portPart[i+1:]
	}
	if p, err := strconv.Atoi(portPart); err == nil && p > 0 && p <= 65535 {
		return fmt.Sprintf("127.0.0.1:%d", p), true
	}
	return "", false
}
