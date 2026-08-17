package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scopeRouting sets every routing variable for one subtest. An empty value
// clears the variable, so each case states the whole environment it means.
func scopeRouting(t *testing.T, profile string, overrides map[string]string) {
	t.Helper()
	t.Setenv("ATTN_PROFILE", profile)
	for _, name := range routingOverrideEnv {
		t.Setenv(name, overrides[name])
		if overrides[name] == "" {
			os.Unsetenv(name)
		}
	}
	// ATTN_DATA_DIR must always be set under go test (see the backstop in
	// config.go); cases that mean "no override" still need a scratch dir.
	if overrides["ATTN_DATA_DIR"] == "" {
		t.Setenv("ATTN_DATA_DIR", t.TempDir())
	}
	ReloadForTesting()
}

func TestValidateProfileRouting_NoProfileIsAlwaysLegal(t *testing.T) {
	// The one shape every test suite and harness depends on: an explicit data
	// dir with no profile.
	scopeRouting(t, "", nil)
	if err := ValidateProfileRouting(); err != nil {
		t.Fatalf("ATTN_DATA_DIR without ATTN_PROFILE must stay legal, got: %v", err)
	}
}

func TestValidateProfileRouting_LeakedDataDirIsRefused(t *testing.T) {
	// The 2026-08-17 incident: an attn agent session's production routing env
	// inherited into `make install PROFILE=fb2lists`.
	prod := DataDirForProfile("")
	scopeRouting(t, "fb2lists", map[string]string{
		"ATTN_DATA_DIR":    prod,
		"ATTN_SOCKET_PATH": filepath.Join(prod, "attn.sock"),
		"ATTN_DB_PATH":     filepath.Join(prod, "attn.db"),
		"ATTN_CONFIG_PATH": filepath.Join(prod, "config.json"),
		"ATTN_PLUGIN_DIR":  filepath.Join(prod, "plugins"),
		"ATTN_WS_PORT":     "9849",
	})

	err := ValidateProfileRouting()
	if err == nil {
		t.Fatal("a profile pointed at another profile's data dir must be refused")
	}
	message := err.Error()
	for _, want := range []string{
		"ATTN_PROFILE=fb2lists",
		DataDirForProfile("fb2lists"),
		WSPortForProfile("fb2lists"),
		prod,
		"attn profile-env fb2lists",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("error must name %q; got:\n%s", want, message)
		}
	}
	// Every variable that disagreed is named, and the printed fix clears all
	// of them — an agent reading this error can act on it in one step.
	for _, name := range routingOverrideEnv {
		if !strings.Contains(message, name) {
			t.Errorf("error must name the disagreeing variable %s; got:\n%s", name, message)
		}
		if !strings.Contains(message, "-u "+name) {
			t.Errorf("printed fix must clear %s; got:\n%s", name, message)
		}
	}
}

func TestValidateProfileRouting_ProfileOwnPathsAgree(t *testing.T) {
	// A caller that sets the overrides *to the profile's own values* (the
	// real-app harness does this) is not a contradiction.
	dir := DataDirForProfile("agent7")
	scopeRouting(t, "agent7", map[string]string{
		"ATTN_DATA_DIR":    dir,
		"ATTN_SOCKET_PATH": filepath.Join(dir, "attn.sock"),
		"ATTN_DB_PATH":     filepath.Join(dir, "attn.db"),
		"ATTN_CONFIG_PATH": filepath.Join(dir, "config.json"),
		"ATTN_PLUGIN_DIR":  filepath.Join(dir, "plugins"),
		"ATTN_WS_PORT":     WSPortForProfile("agent7"),
	})

	if err := ValidateProfileRouting(); err != nil {
		t.Fatalf("overrides that match the profile must pass, got: %v", err)
	}
}

func TestValidateProfileRouting_SingleOverrideNamesOnlyItself(t *testing.T) {
	dir := DataDirForProfile("agent7")
	scopeRouting(t, "agent7", map[string]string{
		"ATTN_DATA_DIR": dir,
		"ATTN_DB_PATH":  filepath.Join(DataDirForProfile(""), "attn.db"),
	})

	err := ValidateProfileRouting()
	if err == nil {
		t.Fatal("a database from another profile must be refused")
	}
	message := err.Error()
	if !strings.Contains(message, "ATTN_DB_PATH") {
		t.Fatalf("error must name ATTN_DB_PATH; got:\n%s", message)
	}
	if strings.Contains(message, "ATTN_SOCKET_PATH") {
		t.Fatalf("error must not name variables that agree; got:\n%s", message)
	}
}

func TestValidateProfileRouting_WSPortDisagreement(t *testing.T) {
	dir := DataDirForProfile("agent7")
	scopeRouting(t, "agent7", map[string]string{
		"ATTN_DATA_DIR": dir,
		"ATTN_WS_PORT":  "9849",
	})

	err := ValidateProfileRouting()
	if err == nil {
		t.Fatal("a port belonging to another profile must be refused")
	}
	if !strings.Contains(err.Error(), "ATTN_WS_PORT") {
		t.Fatalf("error must name ATTN_WS_PORT; got:\n%v", err)
	}
}

// A divergent path can also come from the profile's own config.json, and no
// amount of `env -u` fixes a file — so that message must point at the file.
func TestFormatRoutingConflict_ConfigSourcedValueNamesTheFile(t *testing.T) {
	configFile := "/Users/x/.attn-agent7/config.json"
	err := formatRoutingConflict("agent7", "/Users/x/.attn-agent7", "22944", []routingConflict{
		{label: "ATTN_DB_PATH", value: "/elsewhere/attn.db", configKey: "db_path", configFile: configFile},
	})

	message := err.Error()
	for _, want := range []string{"db_path", configFile, "attn profile clean agent7"} {
		if !strings.Contains(message, want) {
			t.Errorf("error must name %q; got:\n%s", want, message)
		}
	}
	if strings.Contains(message, "env -u") {
		t.Errorf("scrubbing the environment cannot fix a config file, so do not offer it; got:\n%s", message)
	}
}

func TestValidateProfileRouting_SymlinkedDataDirAgrees(t *testing.T) {
	// The fence compares canonically: the same directory reached through a
	// symlink is the same world, and refusing it would be a false positive.
	real := filepath.Join(t.TempDir(), "world")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	scopeRouting(t, "agent7", map[string]string{"ATTN_DATA_DIR": link})
	// Stand the profile's canonical dir on the same inode by comparing against
	// what the fence expects: only the symlink shape is under test here, so
	// assert the comparator directly.
	agree, err := routingValuesAgree(link, real, true)
	if err != nil {
		t.Fatal(err)
	}
	if !agree {
		t.Fatalf("a symlinked path to the same directory must compare equal")
	}
}
