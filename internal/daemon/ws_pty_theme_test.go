package daemon

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
)

// TestHandleSetTerminalTheme_StoresAndFansOutToLiveSessions covers the two
// observable effects handleSetTerminalTheme must have: it stores the theme so
// a subsequent spawn's SpawnOptions carries it (ws_pty.go's spawn site reads
// d.currentTerminalTheme()), and it fans SetTheme out to every session the
// backend currently reports as live.
func TestHandleSetTerminalTheme_StoresAndFansOutToLiveSessions(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{sessionIDs: []string{"sess-a", "sess-b"}}
	d.ptyBackend = backend

	d.handleSetTerminalTheme(nil, &protocol.SetTerminalThemeMessage{
		Foreground: "#aabbcc",
		Background: "#001122",
		Cursor:     "#334455",
	})

	want := pty.TerminalTheme{Foreground: "#aabbcc", Background: "#001122", Cursor: "#334455"}
	if got := d.currentTerminalTheme(); got != want {
		t.Fatalf("currentTerminalTheme() = %+v, want %+v", got, want)
	}

	backend.mu.Lock()
	gotIDs := append([]string(nil), backend.themeCallIDs...)
	gotThemes := append([]pty.TerminalTheme(nil), backend.themeCalls...)
	backend.mu.Unlock()
	if len(gotIDs) != 2 || gotIDs[0] != "sess-a" || gotIDs[1] != "sess-b" {
		t.Fatalf("SetTheme fan-out ids = %v, want [sess-a sess-b]", gotIDs)
	}
	for _, theme := range gotThemes {
		if theme != want {
			t.Fatalf("SetTheme fan-out theme = %+v, want %+v", theme, want)
		}
	}

	// A subsequent spawn must carry the stored theme. Drive the real handler
	// (via spawnForChiefTest's app create ordering: register workspace, add
	// pane, spawn) rather than calling backend.Spawn directly with a
	// hand-built Theme field — that would assert this test's own code
	// instead of the "Theme: d.currentTerminalTheme()" line in
	// handleSpawnSession (ws_pty.go), so it couldn't catch that line being
	// deleted.
	client := newWorkspaceProtocolTestClient()
	spawnForChiefTest(t, d, client, "ws-theme", "sess-c", string(protocol.SessionAgentClaude), false)
	expectSpawnResult(t, client, "sess-c", true)

	opts, ok := backend.LastSpawn()
	if !ok {
		t.Fatal("LastSpawn() ok = false, want true")
	}
	if opts.Theme != want {
		t.Fatalf("spawn Theme = %+v, want %+v", opts.Theme, want)
	}
}

// TestHandleSetTerminalTheme_BlanksInvalidColors covers validation: an
// invalid color field ("red" is not "#rrggbb") is blanked to "" (pty falls
// back to its built-in default) while valid fields are kept as-is.
func TestHandleSetTerminalTheme_BlanksInvalidColors(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}

	d.handleSetTerminalTheme(nil, &protocol.SetTerminalThemeMessage{
		Foreground: "red",
		Background: "#001122",
		Cursor:     "not-a-color",
	})

	want := pty.TerminalTheme{Foreground: "", Background: "#001122", Cursor: ""}
	if got := d.currentTerminalTheme(); got != want {
		t.Fatalf("currentTerminalTheme() = %+v, want %+v", got, want)
	}
}
