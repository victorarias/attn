package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// callTicketAttach drives handleTicketAttach over an in-memory pipe and returns
// the decoded response, so a test can assert both the ok and error paths.
func callTicketAttach(t *testing.T, d *Daemon, msg *protocol.TicketAttachMessage) protocol.Response {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	go func() {
		d.handleTicketAttach(server, msg)
		_ = server.Close()
	}()
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("decode ticket attach response: %v", err)
	}
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

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return data
}
