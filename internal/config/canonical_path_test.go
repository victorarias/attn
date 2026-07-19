package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalRuntimePathResolvesRelativePathAndSymlinkedAncestor(t *testing.T) {
	realRoot := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	linkRoot := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Dir(linkRoot)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	got, err := CanonicalRuntimePath(filepath.Join(filepath.Base(linkRoot), "not-created", "attn.sock"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalRoot, "not-created", "attn.sock")
	if got != want {
		t.Fatalf("canonical path = %q, want %q", got, want)
	}
}
