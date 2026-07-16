package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// readTicketResult decodes the single ws event the board handler sent to a client.
func readTicketResult(t *testing.T, ch chan outboundMessage, target any) {
	t.Helper()
	select {
	case message := <-ch:
		if err := json.Unmarshal(message.payload, target); err != nil {
			t.Fatalf("decode ws event: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("no websocket result event was sent")
	}
}

// ticketIDs collects the ids of a broadcast board feed, for membership assertions.
func ticketIDs(tickets []protocol.Ticket) []string {
	ids := make([]string, 0, len(tickets))
	for _, tk := range tickets {
		ids = append(ids, tk.ID)
	}
	return ids
}

// captureTicketBroadcasts records every tickets_updated board push the daemon makes,
// via the in-process hook, so a test can assert the post-mutation board push
// deterministically. Returns an accessor for the most recent broadcast.
func captureTicketBroadcasts(d *Daemon) (latest func() []protocol.Ticket) {
	var broadcasts [][]protocol.Ticket
	d.ticketsBroadcastHook = func(tickets []protocol.Ticket) {
		broadcasts = append(broadcasts, tickets)
	}
	return func() []protocol.Ticket {
		if len(broadcasts) == 0 {
			return nil
		}
		return broadcasts[len(broadcasts)-1]
	}
}

// get_ticket returns the FULL record: the row plus its activity thread (status
// changes with from/to + note, freeform comments) and artifacts — the detail the
// bare board feed deliberately omits.
func TestGetTicketWSResultReturnsFullRecord(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := time.Now()
	if _, err := d.store.CreateTicket(store.Ticket{
		ID:          "store-migration",
		Title:       "Migrate the store",
		Description: "Move to X",
		Status:      store.TicketStatusWorking,
		Assignee:    "sess-1",
		Cwd:         "/repo",
		LastAgentID: "codex",
	}, "chief-1", now); err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if _, err := d.store.SetTicketStatus("store-migration", store.TicketStatusInReview, "sess-1", "ready for a look", now.Add(time.Minute)); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if _, err := d.store.AddTicketComment("store-migration", "chief-1", "looks good", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("add comment: %v", err)
	}
	root := t.TempDir()
	d.store.SetSetting(SettingNotebookRoot, root)
	artifactDir := filepath.Join(root, "tickets", "store-migration")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "report.md"), []byte("the findings"), 0o644); err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendGetTicketWSResult(client, "req-1", "store-migration")

	var res protocol.TicketResultMessage
	readTicketResult(t, client.send, &res)
	if res.Event != protocol.EventTicketResult || res.RequestID != "req-1" || !res.Success {
		t.Fatalf("result = %+v, want success ticket_result for req-1", res)
	}
	tk := res.Ticket
	if tk == nil {
		t.Fatal("ticket payload is nil")
	}
	if tk.ID != "store-migration" || tk.Status != protocol.TicketStatus(store.TicketStatusInReview) {
		t.Fatalf("ticket id/status = %q/%q", tk.ID, tk.Status)
	}

	var sawStatusChange, sawComment bool
	for _, a := range tk.Activity {
		switch a.Kind {
		case protocol.TicketActivityKind(store.TicketActivityStatusChange):
			sawStatusChange = true
			if a.ToStatus == nil || *a.ToStatus != protocol.TicketStatus(store.TicketStatusInReview) {
				t.Fatalf("status-change to_status = %v, want in_review", a.ToStatus)
			}
			if a.Comment == nil || *a.Comment != "ready for a look" {
				t.Fatalf("status-change comment = %v", a.Comment)
			}
		case protocol.TicketActivityKind(store.TicketActivityComment):
			sawComment = true
		}
	}
	if !sawStatusChange || !sawComment {
		t.Fatalf("activity kinds: statusChange=%v comment=%v (activity=%+v)", sawStatusChange, sawComment, tk.Activity)
	}
	if len(tk.Artifacts) != 1 || tk.Artifacts[0].Filename != "report.md" || tk.Artifacts[0].NotebookPath != "tickets/store-migration/report.md" {
		t.Fatalf("artifacts = %+v", tk.Artifacts)
	}
}

// An unknown id is a failed result (error set, no ticket), never a panic — the TTL
// sweep can remove a ticket between a board push and the app's click.
func TestGetTicketWSResultUnknownIDFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendGetTicketWSResult(client, "req-2", "nope")

	var res protocol.TicketResultMessage
	readTicketResult(t, client.send, &res)
	if res.RequestID != "req-2" || res.Success || res.Error == nil {
		t.Fatalf("unknown-id result = %+v, want failure with error", res)
	}
	if res.Ticket != nil {
		t.Fatalf("failure result carried a ticket: %+v", res.Ticket)
	}
}

func TestTicketArtifactsFollowFilesystemAtReadTime(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	root := t.TempDir()
	d.store.SetSetting(SettingNotebookRoot, root)
	if _, err := d.store.CreateTicket(store.Ticket{ID: "filesystem", Title: "Filesystem", Status: store.TicketStatusWorking}, "chief", time.Now()); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "tickets", "filesystem")
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"b.md": "b", "a.md": "a", ".hidden.md": "hidden", "notes.txt": "text", "prototype.html": "<h1>prototype</h1>"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "nested.md"), []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "a.md"), filepath.Join(dir, "link.md")); err != nil {
		t.Logf("symlink unavailable: %v", err)
	}

	ticket, _ := d.store.GetTicket("filesystem")
	first, err := d.ticketToProtocolFull(ticket)
	if err != nil {
		t.Fatal(err)
	}
	if names := artifactNames(first.Artifacts); !reflect.DeepEqual(names, []string{"a.md", "b.md", "notes.txt", "prototype.html"}) {
		t.Fatalf("artifact names = %v", names)
	}
	if err := os.Rename(filepath.Join(dir, "a.md"), filepath.Join(dir, "implementation.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "b.md")); err != nil {
		t.Fatal(err)
	}
	second, err := d.ticketToProtocolFull(ticket)
	if err != nil {
		t.Fatal(err)
	}
	if names := artifactNames(second.Artifacts); !reflect.DeepEqual(names, []string{"implementation.md", "notes.txt", "prototype.html"}) {
		t.Fatalf("artifacts after rename/delete = %v", names)
	}
}

func artifactNames(artifacts []protocol.TicketArtifact) []string {
	names := make([]string, len(artifacts))
	for i, artifact := range artifacts {
		names[i] = artifact.Filename
	}
	return names
}

// The board feed is the non-archived set, and each row is BARE — activity and
// artifacts stay empty so a busy board is cheap to broadcast; the detail fetch
// loads them.
func TestTicketsForBroadcastBareNonArchived(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := time.Now()
	if _, err := d.store.CreateTicket(store.Ticket{
		ID: "open-one", Title: "Open", Status: store.TicketStatusWorking, Assignee: "sess-1",
	}, "chief-1", now); err != nil {
		t.Fatalf("create open: %v", err)
	}
	if _, err := d.store.CreateTicket(store.Ticket{
		ID: "done-one", Title: "Done", Status: store.TicketStatusDone,
	}, "chief-1", now); err != nil {
		t.Fatalf("create done: %v", err)
	}
	if err := d.store.ArchiveTicket("done-one", now.Add(time.Minute)); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := d.store.AddTicketComment("open-one", "chief-1", "note", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("comment: %v", err)
	}

	board := d.ticketsForBroadcast()
	if len(board) != 1 || board[0].ID != "open-one" {
		t.Fatalf("board = %+v, want only the non-archived open-one", board)
	}
	if len(board[0].Activity) != 0 || len(board[0].Artifacts) != 0 {
		t.Fatalf("board row should be bare, got activity=%d artifacts=%d", len(board[0].Activity), len(board[0].Artifacts))
	}
}
