package launchenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActiveAttnExecutable_PrefersProfileWrapperOverStalePath(t *testing.T) {
	root := t.TempDir()
	profileDir := filepath.Join(root, "attn-profile")
	staleDir := filepath.Join(root, "stale-attn")
	for _, dir := range []string{profileDir, staleDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	for _, path := range []string{filepath.Join(profileDir, "attn"), filepath.Join(staleDir, "attn")} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	profileWrapper := filepath.Join(profileDir, "attn")
	t.Setenv(wrapperPathEnv, profileWrapper)
	t.Setenv("PATH", staleDir)

	if got := ActiveAttnExecutable(); got != profileWrapper {
		t.Fatalf("ActiveAttnExecutable() = %q, want active profile wrapper %q", got, profileWrapper)
	}
}

func TestWithActiveAttnFirst_PrependsAndDeduplicatesProfileDirectory(t *testing.T) {
	root := t.TempDir()
	profileDir := filepath.Join(root, "attn-profile")
	staleDir := filepath.Join(root, "stale-attn")
	otherDir := filepath.Join(root, "other-tools")
	env := []string{
		"PATH=" + strings.Join([]string{staleDir, profileDir, otherDir, profileDir + string(filepath.Separator)}, string(os.PathListSeparator)),
		"UNCHANGED=value",
	}

	got := WithActiveAttnFirst(env, filepath.Join(profileDir, "attn"))
	wantPath := strings.Join([]string{profileDir, staleDir, otherDir}, string(os.PathListSeparator))
	if got[0] != "PATH="+wantPath {
		t.Fatalf("PATH entry = %q, want %q", got[0], "PATH="+wantPath)
	}
	if got[1] != "UNCHANGED=value" {
		t.Fatalf("unrelated environment changed: %#v", got)
	}
}
