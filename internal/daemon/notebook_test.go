package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// newNotebookDaemon returns a test daemon whose notebook.root points at an
// isolated temp dir, so tests never touch the real ~/attn-notebook.
func newNotebookDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	return d
}

// sendNotebookCmd drives one command through the full unix-socket path
// (handleConnection -> ParseMessage -> handler -> Response) over an in-memory
// pipe, returning the decoded Response.
func sendNotebookCmd(t *testing.T, d *Daemon, cmd any) protocol.Response {
	t.Helper()
	server, clientConn := net.Pipe()
	defer clientConn.Close()
	go d.handleConnection(server)
	if err := json.NewEncoder(clientConn).Encode(cmd); err != nil {
		t.Fatalf("encode command: %v", err)
	}
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Ok {
		errMsg := ""
		if resp.Error != nil {
			errMsg = *resp.Error
		}
		t.Fatalf("daemon error: %s", errMsg)
	}
	return resp
}

func TestNotebookInitListReadOverSocket(t *testing.T) {
	d := newNotebookDaemon(t)
	wantRoot := d.store.GetSetting(SettingNotebookRoot)

	init := sendNotebookCmd(t, d, protocol.NotebookInitMessage{Cmd: protocol.CmdNotebookInit})
	if init.NotebookInit == nil || init.NotebookInit.Root != wantRoot || !init.NotebookInit.Created {
		t.Fatalf("init result = %+v, want root=%q created=true", init.NotebookInit, wantRoot)
	}

	list := sendNotebookCmd(t, d, protocol.NotebookListMessage{Cmd: protocol.CmdNotebookList})
	found := map[string]bool{}
	for _, e := range list.NotebookEntries {
		found[e.Path] = true
	}
	for _, want := range []string{"index.md", "log.md", "memory/index.md"} {
		if !found[want] {
			t.Fatalf("list missing scaffold file %q; got %v", want, list.NotebookEntries)
		}
	}

	read := sendNotebookCmd(t, d, protocol.NotebookReadMessage{Cmd: protocol.CmdNotebookRead, Path: "/index.md"})
	if read.NotebookRead == nil || read.NotebookRead.Content == "" || read.NotebookRead.Hash == "" {
		t.Fatalf("read result = %+v", read.NotebookRead)
	}
}

func TestNotebookWriteReadAndCASConflict(t *testing.T) {
	d := newNotebookDaemon(t)
	const path = "memory/decisions/foo.md"
	v1 := "---\nkind: memory\n---\nv1\n"

	create := sendNotebookCmd(t, d, protocol.NotebookWriteMessage{Cmd: protocol.CmdNotebookWrite, Path: path, Content: v1})
	if create.NotebookWrite == nil || create.NotebookWrite.Conflict || create.NotebookWrite.Hash == nil {
		t.Fatalf("create result = %+v", create.NotebookWrite)
	}
	h1 := *create.NotebookWrite.Hash

	read := sendNotebookCmd(t, d, protocol.NotebookReadMessage{Cmd: protocol.CmdNotebookRead, Path: path})
	if read.NotebookRead.Content != v1 || read.NotebookRead.Hash != h1 {
		t.Fatalf("read after create = %+v, want content=%q hash=%q", read.NotebookRead, v1, h1)
	}

	// Stale base hash => conflict carrying the current hash, no write.
	stale := sendNotebookCmd(t, d, protocol.NotebookWriteMessage{
		Cmd: protocol.CmdNotebookWrite, Path: path, Content: "v2", BaseHash: protocol.Ptr("deadbeef"),
	})
	if stale.NotebookWrite == nil || !stale.NotebookWrite.Conflict ||
		stale.NotebookWrite.CurrentHash == nil || *stale.NotebookWrite.CurrentHash != h1 {
		t.Fatalf("stale write = %+v, want conflict with current hash %q", stale.NotebookWrite, h1)
	}

	// Correct base hash => applies.
	v2 := "---\nkind: memory\n---\nv2\n"
	ok := sendNotebookCmd(t, d, protocol.NotebookWriteMessage{
		Cmd: protocol.CmdNotebookWrite, Path: path, Content: v2, BaseHash: protocol.Ptr(h1),
	})
	if ok.NotebookWrite == nil || ok.NotebookWrite.Conflict || ok.NotebookWrite.Hash == nil {
		t.Fatalf("CAS edit = %+v", ok.NotebookWrite)
	}
	read = sendNotebookCmd(t, d, protocol.NotebookReadMessage{Cmd: protocol.CmdNotebookRead, Path: path})
	if read.NotebookRead.Content != v2 {
		t.Fatalf("content after CAS edit = %q, want %q", read.NotebookRead.Content, v2)
	}
}

func TestNotebookAppendJournalBroadcastsChange(t *testing.T) {
	d := newNotebookDaemon(t)
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.wsHub.clients[client] = true
	go d.wsHub.run()

	resp := sendNotebookCmd(t, d, protocol.NotebookAppendJournalMessage{
		Cmd: protocol.CmdNotebookAppendJournal, Entry: "did a thing", Date: protocol.Ptr("2026-06-13"),
	})
	if resp.NotebookWrite == nil || resp.NotebookWrite.Path != "journal/2026-06-13.md" {
		t.Fatalf("append result = %+v", resp.NotebookWrite)
	}

	select {
	case message := <-client.send:
		var event protocol.NotebookChangedMessage
		if err := json.Unmarshal(message.payload, &event); err != nil {
			t.Fatalf("decode notebook_changed: %v", err)
		}
		if event.Event != protocol.EventNotebookChanged || event.Origin != originAgent ||
			len(event.Paths) != 1 || event.Paths[0] != "journal/2026-06-13.md" {
			t.Fatalf("notebook_changed event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("notebook_changed was not broadcast")
	}
}

func TestNotebookRootResolution(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	custom := t.TempDir()
	d.store.SetSetting(SettingNotebookRoot, custom)
	got, err := d.notebookRoot()
	if err != nil {
		t.Fatal(err)
	}
	if got != custom {
		t.Fatalf("notebookRoot with setting = %q, want %q", got, custom)
	}
}
