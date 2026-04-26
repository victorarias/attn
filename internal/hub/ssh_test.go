package hub

import (
	"strings"
	"testing"
)

func TestRemoteShellCommandExportsRemoteOverrideEnv(t *testing.T) {
	t.Setenv("ATTN_REMOTE_ATTN_BIN", "/tmp/attn-harness/bin/attn")
	t.Setenv("ATTN_REMOTE_SOCKET_PATH", "/tmp/attn-harness/attn.sock")
	t.Setenv("ATTN_REMOTE_WS_PORT", "19549")
	t.Setenv("ATTN_REMOTE_DB_PATH", "/tmp/attn-harness/attn.db")

	command := remoteShellCommand("", "printf ready")
	for _, fragment := range []string{
		"ATTN_REMOTE_ATTN_BIN",
		"ATTN_SOCKET_PATH",
		"ATTN_WS_PORT",
		"ATTN_DB_PATH",
		"printf ready",
	} {
		if !strings.Contains(command, fragment) {
			t.Fatalf("remoteShellCommand() missing %q in %q", fragment, command)
		}
	}
	if strings.Contains(command, "ATTN_PROFILE") {
		t.Fatalf("remoteShellCommand(\"\") leaked ATTN_PROFILE: %q", command)
	}
}

func TestRemoteShellCommandExportsProfileWhenSet(t *testing.T) {
	command := remoteShellCommand("dev", "printf ready")
	if !strings.Contains(command, "export ATTN_PROFILE=") {
		t.Fatalf("remoteShellCommand(\"dev\") missing ATTN_PROFILE export: %q", command)
	}
	if !strings.Contains(command, "dev") {
		t.Fatalf("remoteShellCommand(\"dev\") missing profile name: %q", command)
	}
}

func TestRemoteAttnCommandHonorsRemoteBinaryOverride(t *testing.T) {
	command := remoteAttnCommand("", "daemon")
	if !strings.Contains(command, "ATTN_REMOTE_ATTN_BIN") {
		t.Fatalf("remoteAttnCommand() = %q, want ATTN_REMOTE_ATTN_BIN override support", command)
	}
	if !strings.Contains(command, `daemon`) {
		t.Fatalf("remoteAttnCommand() = %q, want daemon arg", command)
	}
	if !strings.Contains(command, "$HOME/.local/bin/attn") {
		t.Fatalf("remoteAttnCommand(\"\") = %q, want default $HOME/.local/bin/attn", command)
	}
}

func TestRemoteAttnCommandUsesProfileBinary(t *testing.T) {
	command := remoteAttnCommand("dev", "ws-relay")
	if !strings.Contains(command, "$HOME/.local/bin/attn-dev") {
		t.Fatalf("remoteAttnCommand(\"dev\") = %q, want attn-dev binary path", command)
	}
}

func TestRemoteBinaryName(t *testing.T) {
	cases := []struct {
		profile string
		want    string
	}{
		{"", "attn"},
		{"  ", "attn"},
		{"dev", "attn-dev"},
		{"foo", "attn-foo"},
	}
	for _, c := range cases {
		t.Run(c.profile, func(t *testing.T) {
			got := remoteBinaryName(c.profile)
			if got != c.want {
				t.Fatalf("remoteBinaryName(%q) = %q, want %q", c.profile, got, c.want)
			}
		})
	}
}
