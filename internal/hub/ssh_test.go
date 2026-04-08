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

	command := remoteShellCommand("printf ready")
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
}

func TestRemoteAttnCommandHonorsRemoteBinaryOverride(t *testing.T) {
	command := remoteAttnCommand("daemon")
	if !strings.Contains(command, "ATTN_REMOTE_ATTN_BIN") {
		t.Fatalf("remoteAttnCommand() = %q, want ATTN_REMOTE_ATTN_BIN override support", command)
	}
	if !strings.Contains(command, `daemon`) {
		t.Fatalf("remoteAttnCommand() = %q, want daemon arg", command)
	}
}
