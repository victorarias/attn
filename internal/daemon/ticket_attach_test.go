package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ticketnotify"
)

// callTicketAttach drives handleTicketAttach over an in-memory pipe and returns
// the decoded response, so a test can assert both the ok and error paths. It waits
// for the handler to fully return — the response is encoded BEFORE the notify +
// broadcast fan-out, so without this barrier a test could assert on those side
// effects before they run.
func callTicketAttach(t *testing.T, d *Daemon, msg *protocol.TicketAttachMessage) protocol.Response {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	go func() {
		d.handleTicketAttach(server, msg)
		_ = server.Close()
		close(done)
	}()
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("decode ticket attach response: %v", err)
	}
	<-done
	return resp
}

// The happy path: an agent attaches a file to its own bound ticket; the bytes land
// verbatim under .attn/tickets/<id>/ and the attachment is recorded on the ticket.
func TestTicketAttachCopiesAndRecords(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	_, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)

	src := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(src, []byte("the findings"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	resp := callTicketAttach(t, d, &protocol.TicketAttachMessage{
		Cmd:             protocol.CmdTicketAttach,
		SourceSessionID: agentID,
		SourcePath:      src,
		Filename:        "report.md",
		Note:            protocol.Ptr("for review"),
	})
	if !resp.Ok || resp.TicketAttachResult == nil {
		t.Fatalf("response = %+v, want ok with attach result", resp)
	}
	if resp.TicketAttachResult.TicketID != ticketID || resp.TicketAttachResult.Filename != "report.md" {
		t.Fatalf("attach result = %+v, want ticket %q / report.md", resp.TicketAttachResult, ticketID)
	}

	ticket, _ := d.store.GetTicket(ticketID)
	if ticket == nil || len(ticket.Attachments) != 1 {
		t.Fatalf("ticket attachments = %+v, want exactly one", ticket)
	}
	att := ticket.Attachments[0]
	if att.Filename != "report.md" || att.Note != "for review" {
		t.Fatalf("attachment = %+v, want report.md / for review", att)
	}
	// The copy landed under the ticket store dir with the source bytes intact.
	if dir := filepath.Dir(att.Path); filepath.Base(filepath.Dir(dir)) != "tickets" {
		t.Fatalf("attachment path %q not under .attn/tickets/<id>/", att.Path)
	}
	got, err := os.ReadFile(att.Path)
	if err != nil {
		t.Fatalf("read copied attachment: %v", err)
	}
	if string(got) != "the findings" {
		t.Fatalf("copied bytes = %q, want %q", got, "the findings")
	}
}

// Two attachments sharing a filename both land, the second deduped on disk so it
// never clobbers the first; both display the original name.
func TestTicketAttachDedupesOnDiskName(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	_, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)

	srcDir := t.TempDir()
	first := filepath.Join(srcDir, "a", "log.txt")
	second := filepath.Join(srcDir, "b", "log.txt")
	for _, p := range []string{first, second} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	_ = os.WriteFile(first, []byte("first"), 0o644)
	_ = os.WriteFile(second, []byte("second"), 0o644)

	for _, src := range []string{first, second} {
		resp := callTicketAttach(t, d, &protocol.TicketAttachMessage{
			Cmd:             protocol.CmdTicketAttach,
			SourceSessionID: agentID,
			SourcePath:      src,
			Filename:        "log.txt",
		})
		if !resp.Ok {
			t.Fatalf("attach %q failed: %+v", src, resp)
		}
	}

	ticket, _ := d.store.GetTicket(ticketID)
	if len(ticket.Attachments) != 2 {
		t.Fatalf("attachments = %d, want 2", len(ticket.Attachments))
	}
	a, b := ticket.Attachments[0], ticket.Attachments[1]
	if a.Filename != "log.txt" || b.Filename != "log.txt" {
		t.Fatalf("display names = %q,%q, want log.txt twice", a.Filename, b.Filename)
	}
	if a.Path == b.Path {
		t.Fatalf("both attachments share on-disk path %q — dedup failed", a.Path)
	}
	if string(mustRead(t, a.Path)) != "first" || string(mustRead(t, b.Path)) != "second" {
		t.Fatalf("deduped copies clobbered each other: %q / %q", mustRead(t, a.Path), mustRead(t, b.Path))
	}
}

// A session with no bound ticket cannot attach.
func TestTicketAttachNoActiveTicketFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	src := filepath.Join(t.TempDir(), "x.md")
	_ = os.WriteFile(src, []byte("x"), 0o644)

	resp := callTicketAttach(t, d, &protocol.TicketAttachMessage{
		Cmd:             protocol.CmdTicketAttach,
		SourceSessionID: "no-such-session",
		SourcePath:      src,
		Filename:        "x.md",
	})
	if resp.Ok || resp.Error == nil {
		t.Fatalf("response = %+v, want failure for unbound session", resp)
	}
}

// A missing source file is a clean error, not a panic or a recorded attachment.
func TestTicketAttachMissingFileFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	_, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)

	resp := callTicketAttach(t, d, &protocol.TicketAttachMessage{
		Cmd:             protocol.CmdTicketAttach,
		SourceSessionID: agentID,
		SourcePath:      filepath.Join(t.TempDir(), "does-not-exist.md"),
		Filename:        "does-not-exist.md",
	})
	if resp.Ok || resp.Error == nil {
		t.Fatalf("response = %+v, want failure for missing file", resp)
	}
	ticket, _ := d.store.GetTicket(ticketID)
	if len(ticket.Attachments) != 0 {
		t.Fatalf("attachments = %d, want 0 (nothing recorded on a failed copy)", len(ticket.Attachments))
	}
}

// The attach fan-out reaches both the chief and the board. The agent self-authors
// the attachment, so the involved chief (idle, not self-monitoring) is nudged while
// the self-author is not, and the whole board is re-broadcast carrying the row.
func TestTicketAttachNotifiesChiefAndBroadcasts(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	chiefID, agentID, inputs := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	// Both sides have read their inbox, so the only new event is the attachment the
	// agent is about to self-author. That isolates the fan-out: the chief is nudged
	// solely because of the attachment, and the self-authoring agent is not nudged.
	for _, id := range []string{chiefID, agentID} {
		if _, err := ticketnotify.ConsumeAll(d.store, d.ticketObserversForSession(id), time.Now()); err != nil {
			t.Fatalf("consume inbox for %s: %v", id, err)
		}
	}
	d.store.UpdateState(chiefID, protocol.StateIdle)
	d.store.UpdateState(agentID, protocol.StateIdle)
	latestBroadcast := captureTicketBroadcasts(d)

	src := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(src, []byte("the findings"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	resp := callTicketAttach(t, d, &protocol.TicketAttachMessage{
		Cmd:             protocol.CmdTicketAttach,
		SourceSessionID: agentID,
		SourcePath:      src,
		Filename:        "report.md",
	})
	if !resp.Ok {
		t.Fatalf("attach failed: %+v", resp)
	}

	fireNudgeNow(t, d, chiefID) // the attachment armed the chief's countdown
	if !wasNudged(inputs(chiefID)) {
		t.Fatal("chief was not notified of the agent's attachment")
	}
	if wasNudged(inputs(agentID)) {
		t.Fatal("the self-authoring agent should not be nudged about its own attachment")
	}

	board := latestBroadcast()
	if board == nil {
		t.Fatal("the attach fired no tickets_updated board push")
	}
	seen := false
	for _, tk := range board {
		if tk.ID == ticketID {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("tickets_updated %v missing the attached ticket %q", ticketIDs(board), ticketID)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return data
}
