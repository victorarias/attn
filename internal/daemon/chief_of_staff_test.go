package daemon

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func readChiefOfStaffResult(t *testing.T, client *wsClient) protocol.ChiefOfStaffResultMessage {
	t.Helper()
	select {
	case raw := <-client.send:
		var result protocol.ChiefOfStaffResultMessage
		if err := json.Unmarshal(raw.payload, &result); err != nil {
			t.Fatalf("decode chief_of_staff_result: %v", err)
		}
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for chief_of_staff_result")
		return protocol.ChiefOfStaffResultMessage{}
	}
}

func newChiefOfStaffTestDaemon(t *testing.T) (*Daemon, *wsClient) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	return d, newRenameTestClient()
}

func addChiefOfStaffTestSession(d *Daemon, id, label string) {
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: label, Agent: protocol.SessionAgentCodex,
		Directory: "/tmp/" + id, WorkspaceID: "workspace-" + id,
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

func TestSetChiefOfStaffTransfersSingletonRole(t *testing.T) {
	d, client := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "session-a", "first")
	addChiefOfStaffTestSession(d, "session-b", "second")

	d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
		Cmd: protocol.CmdSetChiefOfStaff, SessionID: "session-a", ChiefOfStaff: true,
	})
	first := readChiefOfStaffResult(t, client)
	if !first.Success || d.chiefOfStaffSessionID() != "session-a" {
		t.Fatalf("first assignment = %+v role=%q", first, d.chiefOfStaffSessionID())
	}

	d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
		Cmd: protocol.CmdSetChiefOfStaff, SessionID: "session-b", ChiefOfStaff: true,
	})
	second := readChiefOfStaffResult(t, client)
	if !second.Success || protocol.Deref(second.PreviousSessionID) != "session-a" {
		t.Fatalf("transfer result = %+v", second)
	}
	if got := d.chiefOfStaffSessionID(); got != "session-b" {
		t.Fatalf("role after transfer = %q, want session-b", got)
	}

	sessions := d.mergedSessionsForBroadcast()
	for _, session := range sessions {
		switch session.ID {
		case "session-a":
			if protocol.Deref(session.ChiefOfStaff) {
				t.Fatal("previous session still marked chief")
			}
		case "session-b":
			if !protocol.Deref(session.ChiefOfStaff) {
				t.Fatal("new session not marked chief")
			}
		}
	}
}

func TestSetChiefOfStaffRejectsUnknownSession(t *testing.T) {
	d, client := newChiefOfStaffTestDaemon(t)

	d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
		Cmd: protocol.CmdSetChiefOfStaff, SessionID: "missing", ChiefOfStaff: true,
	})
	result := readChiefOfStaffResult(t, client)
	if result.Success || protocol.Deref(result.Error) == "" {
		t.Fatalf("result = %+v, want failure", result)
	}
	if got := d.chiefOfStaffSessionID(); got != "" {
		t.Fatalf("role = %q, want empty", got)
	}
}

func TestClearChiefOfStaffKeepsTransferredRole(t *testing.T) {
	d, client := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "session-a", "first")
	addChiefOfStaffTestSession(d, "session-b", "second")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "session-b"); err != nil {
		t.Fatal(err)
	}

	d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
		Cmd: protocol.CmdSetChiefOfStaff, SessionID: "session-a", ChiefOfStaff: false,
	})
	result := readChiefOfStaffResult(t, client)
	if !result.Success {
		t.Fatalf("clear result = %+v", result)
	}
	if got := d.chiefOfStaffSessionID(); got != "session-b" {
		t.Fatalf("role after stale clear = %q, want session-b", got)
	}
}

// Enter must reach the PTY as its own write, a real interval after the paste
// terminator. Claude Code folds a CR arriving in the same read as the paste end
// into the pasted text — the payload then sits unsent in the composer — and an
// undelayed second write lands in that same read.
func TestTypeDoorbellDelaysEnterAfterThePaste(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	addChiefOfStaffTestSession(d, "delayed-enter", "target")

	const gap = 40 * time.Millisecond
	previous := doorbellSubmitDelay
	doorbellSubmitDelay = gap
	t.Cleanup(func() { doorbellSubmitDelay = previous })

	var mu sync.Mutex
	var writes []string
	var at []time.Time
	d.ptyBackend = &fakeSpawnBackend{onInput: func(_ string, data []byte) {
		mu.Lock()
		defer mu.Unlock()
		writes = append(writes, string(data))
		at = append(at, time.Now())
	}}

	if err := d.typeDoorbell("delayed-enter", "ping"); err != nil {
		t.Fatalf("typeDoorbell() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	wantPaste := bracketedPasteStart + "ping" + bracketedPasteEnd
	if len(writes) != 2 || writes[0] != wantPaste || writes[1] != "\r" {
		t.Fatalf("PTY writes = %q, want [%q, %q]", writes, wantPaste, "\r")
	}
	if elapsed := at[1].Sub(at[0]); elapsed < gap {
		t.Fatalf("Enter followed the paste after %v, want at least %v", elapsed, gap)
	}
}

// A keystroke racing the doorbell's paste-to-Enter gap must not be spliced into
// the submission. The user types while the paste is on the wire and the Enter
// has not been sent yet; the fence has to write their bytes after the Enter, so
// the doorbell submits its own payload and the typed line stays in the composer.
func TestTypeDoorbellDoesNotSubmitInputRacingTheGap(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	addChiefOfStaffTestSession(d, "splice-race", "target")

	previous := doorbellSubmitDelay
	doorbellSubmitDelay = 60 * time.Millisecond
	t.Cleanup(func() { doorbellSubmitDelay = previous })

	var mu sync.Mutex
	var writes []string
	var writtenAt []time.Time
	pasteSeen := make(chan struct{})
	var pasteOnce sync.Once
	d.ptyBackend = &fakeSpawnBackend{onInput: func(_ string, data []byte) {
		mu.Lock()
		writes = append(writes, string(data))
		writtenAt = append(writtenAt, time.Now())
		mu.Unlock()
		if strings.HasPrefix(string(data), bracketedPasteStart) {
			pasteOnce.Do(func() { close(pasteSeen) })
		}
	}}

	doorbellDone := make(chan error, 1)
	go func() { doorbellDone <- d.typeDoorbell("splice-race", "ping") }()

	// Type into the same session while the doorbell sits in its submit delay.
	<-pasteSeen
	typed := make(chan struct{})
	var typedAt time.Time
	go func() {
		defer close(typed)
		typedAt = time.Now()
		d.handlePtyInput(&wsClient{send: make(chan outboundMessage, 4)}, &protocol.PtyInputMessage{
			Cmd: protocol.CmdPtyInput, ID: "splice-race", Data: "half-typed line",
		})
	}()

	if err := <-doorbellDone; err != nil {
		t.Fatalf("typeDoorbell() error = %v", err)
	}
	<-typed

	mu.Lock()
	defer mu.Unlock()
	wantPaste := bracketedPasteStart + "ping" + bracketedPasteEnd
	want := []string{wantPaste, "\r", "half-typed line"}
	if len(writes) != len(want) {
		t.Fatalf("PTY writes = %q, want %q", writes, want)
	}
	for i := range want {
		if writes[i] != want[i] {
			t.Fatalf("PTY writes = %q, want %q (the keystroke was spliced into the submission)", writes, want)
		}
	}
	// Without this the ordering above could hold because the keystroke simply
	// arrived late: it has to have entered the gap before the Enter went out.
	if !typedAt.Before(writtenAt[1]) {
		t.Fatalf("keystroke started at %v, after the Enter at %v — the race never happened", typedAt, writtenAt[1])
	}
}
