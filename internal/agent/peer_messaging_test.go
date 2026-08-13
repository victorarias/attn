package agent

import (
	"slices"
	"testing"
)

// attn's own messaging (`attn agent list` / `attn agent msg`) is the address
// book; Claude Code's peer tools are a second, claude-only one keyed on the
// working directory, so a launched agent never gets them.
func TestClaudeBuildCommand_DeniesPeerMessagingTools(t *testing.T) {
	cmd := (&Claude{}).BuildCommand(SpawnOpts{
		SessionID:  "sess-1",
		CWD:        "/tmp/project",
		Executable: "claude",
	})
	i := slices.Index(cmd.Args, "--disallowed-tools")
	if i < 0 {
		t.Fatalf("args = %#v, want --disallowed-tools", cmd.Args)
	}
	// One element per rule, the form the flag documents. A joined
	// "ListAgents SendMessage" parses the same on 2.1.228, but a claude that
	// tightened parsing would leave the deny inert with this test still green.
	if got := cmd.Args[i+1:]; len(got) != 2 || got[0] != "ListAgents" || got[1] != "SendMessage" {
		t.Fatalf("--disallowed-tools = %#v, want two elements ListAgents, SendMessage", got)
	}
}

// Everything after "--" is the initial prompt, so a flag appended past it would
// be typed at the agent instead of read by it.
func TestClaudeBuildCommand_DenyPrecedesInitialPrompt(t *testing.T) {
	cmd := (&Claude{}).BuildCommand(SpawnOpts{
		SessionID:     "sess-1",
		CWD:           "/tmp/project",
		Executable:    "claude",
		InitialPrompt: "get to work",
	})
	deny := slices.Index(cmd.Args, "--disallowed-tools")
	sep := slices.Index(cmd.Args, "--")
	if deny < 0 || sep < 0 || deny > sep {
		t.Fatalf("args = %#v, want --disallowed-tools before the -- prompt separator", cmd.Args)
	}
}

func TestClaudeBuildCommand_PeerMessagingEnvRestoresTools(t *testing.T) {
	t.Setenv("ATTN_CLAUDE_PEER_MESSAGING", "1")
	cmd := (&Claude{}).BuildCommand(SpawnOpts{
		SessionID:  "sess-1",
		CWD:        "/tmp/project",
		Executable: "claude",
	})
	if slices.Contains(cmd.Args, "--disallowed-tools") {
		t.Fatalf("args = %#v, want no --disallowed-tools with ATTN_CLAUDE_PEER_MESSAGING=1", cmd.Args)
	}
}

// Only Claude Code has these tools; the other drivers must not grow the flag.
func TestOtherDriversHaveNoPeerMessagingDeny(t *testing.T) {
	for _, driver := range []Driver{&Codex{}, &Copilot{}} {
		cmd := driver.BuildCommand(SpawnOpts{
			SessionID:  "sess-1",
			CWD:        "/tmp/project",
			Executable: driver.DefaultExecutable(),
		})
		if slices.Contains(cmd.Args, "--disallowed-tools") {
			t.Fatalf("%s args = %#v, want no --disallowed-tools", driver.Name(), cmd.Args)
		}
	}
}
