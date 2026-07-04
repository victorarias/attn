package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// callJournalAppend drives handleJournalAppend over an in-memory pipe and returns
// the decoded response, waiting for the handler to fully return before the caller
// inspects any daemon-side side effects (self-write record, broadcast).
func callJournalAppend(t *testing.T, d *Daemon, msg *protocol.JournalAppendMessage) protocol.Response {
	t.Helper()
	server, clientConn := net.Pipe()
	defer clientConn.Close()
	done := make(chan struct{})
	go func() {
		d.handleJournalAppend(server, msg)
		_ = server.Close()
		close(done)
	}()
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode journal append response: %v", err)
	}
	<-done
	return resp
}

// A successful append creates journal/<date>.md with the entry and echoes
// rel_path and hash.
func TestJournalAppendCreatesFile(t *testing.T) {
	d := newNotebookDaemon(t)
	resp := callJournalAppend(t, d, &protocol.JournalAppendMessage{
		Cmd:   protocol.CmdJournalAppend,
		Entry: "first entry",
		Date:  protocol.Ptr("2026-07-05"),
	})
	if !resp.Ok {
		t.Fatalf("expected ok response, got error: %v", resp.Error)
	}
	if resp.JournalAppendResult == nil {
		t.Fatal("expected journal append result")
	}
	if resp.JournalAppendResult.RelPath != "journal/2026-07-05.md" {
		t.Fatalf("unexpected rel_path: %q", resp.JournalAppendResult.RelPath)
	}
	if resp.JournalAppendResult.Hash == "" {
		t.Fatal("expected non-empty hash")
	}
	store, err := d.notebookStoreFor()
	if err != nil {
		t.Fatalf("notebookStoreFor: %v", err)
	}
	content, _, err := store.Read(resp.JournalAppendResult.RelPath)
	if err != nil {
		t.Fatalf("read journal file: %v", err)
	}
	if !strings.Contains(string(content), "first entry") {
		t.Fatalf("journal file missing entry: %q", string(content))
	}
}

// A second append lands both entries in the same file — the serialization
// behavior itself belongs to notebook.Store's own tests; this only checks the
// handler wires two calls through to two appends.
func TestJournalAppendSecondCallLandsBothEntries(t *testing.T) {
	d := newNotebookDaemon(t)
	date := protocol.Ptr("2026-07-05")
	first := callJournalAppend(t, d, &protocol.JournalAppendMessage{Cmd: protocol.CmdJournalAppend, Entry: "first entry", Date: date})
	if !first.Ok {
		t.Fatalf("first append failed: %v", first.Error)
	}
	second := callJournalAppend(t, d, &protocol.JournalAppendMessage{Cmd: protocol.CmdJournalAppend, Entry: "second entry", Date: date})
	if !second.Ok {
		t.Fatalf("second append failed: %v", second.Error)
	}
	store, err := d.notebookStoreFor()
	if err != nil {
		t.Fatalf("notebookStoreFor: %v", err)
	}
	content, _, err := store.Read(second.JournalAppendResult.RelPath)
	if err != nil {
		t.Fatalf("read journal file: %v", err)
	}
	if !strings.Contains(string(content), "first entry") || !strings.Contains(string(content), "second entry") {
		t.Fatalf("journal file missing an entry: %q", string(content))
	}
}

// An empty (or whitespace-only) entry is rejected before reaching the store.
func TestJournalAppendEmptyEntryFails(t *testing.T) {
	d := newNotebookDaemon(t)
	resp := callJournalAppend(t, d, &protocol.JournalAppendMessage{Cmd: protocol.CmdJournalAppend, Entry: "   "})
	if resp.Ok {
		t.Fatal("expected empty entry to be rejected")
	}
}

// A malformed date is rejected — the store's own validation error passes through
// rather than being duplicated in the handler.
func TestJournalAppendMalformedDateFails(t *testing.T) {
	d := newNotebookDaemon(t)
	resp := callJournalAppend(t, d, &protocol.JournalAppendMessage{
		Cmd:   protocol.CmdJournalAppend,
		Entry: "an entry",
		Date:  protocol.Ptr("not-a-date"),
	})
	if resp.Ok {
		t.Fatal("expected malformed date to be rejected")
	}
}

// An empty date defaults to today, in the daemon's local timezone.
func TestJournalAppendDefaultsDateToToday(t *testing.T) {
	d := newNotebookDaemon(t)
	resp := callJournalAppend(t, d, &protocol.JournalAppendMessage{Cmd: protocol.CmdJournalAppend, Entry: "an entry"})
	if !resp.Ok {
		t.Fatalf("expected ok response, got error: %v", resp.Error)
	}
	today := time.Now().Format("2006-01-02")
	if resp.JournalAppendResult.RelPath != filepath.ToSlash(filepath.Join("journal", today+".md")) {
		t.Fatalf("expected today's journal file, got %q", resp.JournalAppendResult.RelPath)
	}
}
