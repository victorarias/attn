package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// routingOverrideEnv is every variable that can outrank ATTN_PROFILE when a
// path or endpoint is resolved: the set the fence checks, and the set
// `attn profile-env` clears. Order is the order the fence reports them in.
var routingOverrideEnv = []string{
	"ATTN_DATA_DIR",
	"ATTN_SOCKET_PATH",
	"ATTN_DB_PATH",
	"ATTN_CONFIG_PATH",
	"ATTN_PLUGIN_DIR",
	"ATTN_WS_PORT",
}

// RoutingOverrideEnv returns the routing variables that outrank ATTN_PROFILE.
func RoutingOverrideEnv() []string {
	return append([]string(nil), routingOverrideEnv...)
}

// ValidateProfileRouting refuses a process whose ATTN_PROFILE disagrees with
// the routing it actually resolved. An explicit path override outranks the
// profile, so one inherited from a parent attn session points a named profile
// at another profile's world while every profile-derived name still says
// otherwise. On 2026-08-17 that combination let `make install PROFILE=<name>`
// take the production PID lock and migrate the production database.
//
// Call it before anything opens a database, takes the PID lock, binds a
// socket, or stops another daemon. The contradiction is never intentional:
// there is no correction, only a refusal naming both sides.
//
// ATTN_DATA_DIR with no ATTN_PROFILE stays legal and unchecked — that is how
// every test suite and harness scopes itself away from a real profile.
func ValidateProfileRouting() error {
	profile := Profile()
	if profile == "" {
		return nil
	}
	profileDir := DataDirForProfile(profile)
	profilePort := WSPortForProfile(profile)

	// ATTN_DATA_DIR comes first: every other default derives from it, so
	// reporting it once explains the rest. configKey names the config.json
	// field that can also produce the value — the only other source there is.
	checks := []struct {
		env       string
		configKey string
		resolved  string
		expected  string
		isPath    bool
	}{
		{"ATTN_DATA_DIR", "", DataDir(), profileDir, true},
		{"ATTN_SOCKET_PATH", "socket_path", SocketPath(), filepath.Join(profileDir, "attn.sock"), true},
		{"ATTN_DB_PATH", "db_path", DBPath(), filepath.Join(profileDir, "attn.db"), true},
		{"ATTN_CONFIG_PATH", "", ConfigPath(), filepath.Join(profileDir, "config.json"), true},
		{"ATTN_PLUGIN_DIR", "", PluginDir(), filepath.Join(profileDir, "plugins"), true},
		{"ATTN_WS_PORT", "", WSPort(), profilePort, false},
	}

	var (
		conflicts       []routingConflict
		dataDirConflict bool
	)
	for _, check := range checks {
		agree, err := routingValuesAgree(check.resolved, check.expected, check.isPath)
		if err != nil {
			return fmt.Errorf("resolve %s for profile %s: %w", check.env, profile, err)
		}
		if agree {
			continue
		}
		if check.env == "ATTN_DATA_DIR" {
			dataDirConflict = true
		}
		if envValue, ok := lookupRoutingOverride(check.env); ok {
			conflicts = append(conflicts, routingConflict{label: check.env, value: envValue, env: check.env})
			continue
		}
		if dataDirConflict || check.configKey == "" {
			// Derived from the data dir that is already reported; one line is
			// enough to explain the whole set.
			continue
		}
		conflicts = append(conflicts, routingConflict{
			label:      check.env,
			value:      check.resolved,
			configKey:  check.configKey,
			configFile: ConfigPath(),
		})
	}
	if len(conflicts) == 0 {
		return nil
	}
	return formatRoutingConflict(profile, profileDir, profilePort, conflicts)
}

// routingConflict is one resolved value that does not belong to the active
// profile, and where it came from: an environment variable, or a field in the
// profile's own config.json. The source decides the remedy — scrubbing the
// environment cannot fix a file.
type routingConflict struct {
	label      string
	value      string
	env        string
	configKey  string
	configFile string
}

func formatRoutingConflict(profile, profileDir, profilePort string, conflicts []routingConflict) error {
	var b strings.Builder
	fmt.Fprintf(&b, "ATTN_PROFILE=%s disagrees with the routing this process resolved.\n", profile)
	fmt.Fprintf(&b, "  profile %s is %s (port %s), but:\n", profile, profileDir, profilePort)

	var envNames []string
	var files []string
	for _, conflict := range conflicts {
		if conflict.env != "" {
			fmt.Fprintf(&b, "    %-16s = %s\n", conflict.label, conflict.value)
			envNames = append(envNames, conflict.env)
			continue
		}
		fmt.Fprintf(&b, "    %-16s = %s (%s in %s)\n", conflict.label, conflict.value, conflict.configKey, conflict.configFile)
		files = append(files, conflict.configFile)
	}
	fmt.Fprintf(&b, "  An explicit override outranks ATTN_PROFILE, so this process would act as profile %s"+
		" against another profile's data. Refusing before anything opens it.\n", profile)

	if len(envNames) > 0 {
		fmt.Fprintf(&b, "  Fix: env%s ATTN_PROFILE=%s <command>\n", scrubFlags(envNames), profile)
		fmt.Fprintf(&b, "  Or clear them in your shell: eval \"$(attn profile-env %s)\"\n", profile)
	}
	if len(files) > 0 {
		fmt.Fprintf(&b, "  No environment change fixes %s: edit it, or start the profile over with `attn profile clean %s`\n",
			files[0], profile)
	}
	return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}

// scrubFlags renders the `-u NAME` flags for the variables that are actually
// set, so the printed command clears exactly what disagreed.
func scrubFlags(names []string) string {
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, " -u %s", name)
	}
	return b.String()
}

// lookupRoutingOverride reports whether a routing variable is set to a
// non-empty value, and what it holds.
func lookupRoutingOverride(name string) (string, bool) {
	value := strings.TrimSpace(os.Getenv(name))
	return value, value != ""
}

// routingValuesAgree compares a resolved routing value against the profile's
// canonical one. Paths compare canonically (an env override may be relative or
// reach the same directory through a symlink); ports compare as trimmed text.
func routingValuesAgree(resolved, expected string, isPath bool) (bool, error) {
	if !isPath {
		return strings.TrimSpace(resolved) == strings.TrimSpace(expected), nil
	}
	resolvedPath, err := CanonicalRuntimePath(resolved)
	if err != nil {
		return false, err
	}
	expectedPath, err := CanonicalRuntimePath(expected)
	if err != nil {
		return false, err
	}
	return resolvedPath == expectedPath, nil
}

