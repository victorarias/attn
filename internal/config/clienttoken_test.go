package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureClientTokenMintsOnceAndKeepsItOwnerOnly(t *testing.T) {
	dir := t.TempDir()

	first, err := EnsureClientToken(dir)
	if err != nil {
		t.Fatalf("EnsureClientToken() error = %v", err)
	}
	if len(first) != 64 {
		t.Fatalf("token = %q (%d chars), want 64 hex chars", first, len(first))
	}

	// A restart must reuse it: a fresh token would strand every client that is
	// already holding the old one.
	second, err := EnsureClientToken(dir)
	if err != nil {
		t.Fatalf("EnsureClientToken() second call error = %v", err)
	}
	if second != first {
		t.Fatalf("token changed across calls: %q then %q", first, second)
	}

	info, err := os.Stat(filepath.Join(dir, ClientTokenFile))
	if err != nil {
		t.Fatalf("stat token: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("token mode = %#o, want 0600", mode)
	}
}

func TestEnsureClientTokenMintsDistinctTokensPerProfile(t *testing.T) {
	one, err := EnsureClientToken(t.TempDir())
	if err != nil {
		t.Fatalf("EnsureClientToken() error = %v", err)
	}
	two, err := EnsureClientToken(t.TempDir())
	if err != nil {
		t.Fatalf("EnsureClientToken() error = %v", err)
	}
	if one == two {
		t.Fatal("two profiles minted the same token; the whole point is that they differ")
	}
}

func TestClientTokenPrefersTheEnvironmentOverTheFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ATTN_DATA_DIR", dir)
	if _, err := EnsureClientToken(dir); err != nil {
		t.Fatalf("EnsureClientToken() error = %v", err)
	}

	t.Setenv("ATTN_CLIENT_TOKEN", "handed-over")
	if got := ClientToken(); got != "handed-over" {
		t.Fatalf("ClientToken() = %q, want the environment's value", got)
	}
	got, err := EnsureClientToken(dir)
	if err != nil {
		t.Fatalf("EnsureClientToken() error = %v", err)
	}
	if got != "handed-over" {
		t.Fatalf("EnsureClientToken() = %q, want the environment's value", got)
	}
}

func TestClientTokenIsEmptyWhenNothingMintedIt(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	if got := ClientToken(); got != "" {
		t.Fatalf("ClientToken() = %q, want empty so the daemon's refusal names the path", got)
	}
}
