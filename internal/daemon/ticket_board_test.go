package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// The SDK snapshot's board is the non-archived set, and each row is SLIM — the
// brief, the history thread and the artifacts belong to a read by id.
func TestAppTicketRowsBareNonArchived(t *testing.T) {
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

	board := d.appTicketRows()
	if len(board) != 1 || board[0].ID != "open-one" {
		t.Fatalf("board = %+v, want only the non-archived open-one", board)
	}
}

// The SDK's row carries the board and not the brief. The brief is the bulk of a
// ticket and an app reading current state does not render one from a row — it
// is fetched by id.
func TestAppTicketRowCarriesTheBoardAndNotTheBrief(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := time.Now()
	brief := strings.Repeat("a delegation brief nobody renders from a board row. ", 200)
	if _, err := d.store.CreateTicket(store.Ticket{
		ID:          "store-migration",
		Title:       "Migrate the store",
		Description: brief,
		Status:      store.TicketStatusWorking,
		Assignee:    "sess-1",
		Cwd:         "/repo",
		LastAgentID: "codex",
	}, "chief-1", now); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := d.store.AddTicketComment("store-migration", "chief-1", "a note", now.Add(time.Minute)); err != nil {
		t.Fatalf("comment: %v", err)
	}

	board := d.appTicketRows()
	if len(board) != 1 {
		t.Fatalf("board = %+v, want one row", board)
	}
	raw, err := json.Marshal(board[0])
	if err != nil {
		t.Fatalf("marshal row: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal row: %v", err)
	}
	for _, field := range []string{"description", "activity", "artifacts"} {
		if _, present := wire[field]; present {
			t.Fatalf("board row carries %q: %s", field, raw)
		}
	}
	if strings.Contains(string(raw), "delegation brief") {
		t.Fatalf("the brief reached the board row: %s", raw)
	}
	// What a row does carry.
	for field, want := range map[string]any{
		"id":            "store-migration",
		"title":         "Migrate the store",
		"status":        string(store.TicketStatusWorking),
		"assignee":      "sess-1",
		"cwd":           "/repo",
		"last_agent_id": "codex",
	} {
		if wire[field] != want {
			t.Fatalf("row %s = %v, want %v", field, wire[field], want)
		}
	}
	if wire["updated_at"] == "" || wire["updated_at"] == nil {
		t.Fatalf("row is missing updated_at: %s", raw)
	}
}

// Slimming the SDK's rows must not reach the agent's board read: an agent lists
// tickets to find work, and the brief is the work.
func TestTicketListRowsStillCarryTheBrief(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	if _, err := d.store.CreateTicket(store.Ticket{
		ID:          "store-migration",
		Title:       "Migrate the store",
		Description: "move the store to X",
		Status:      store.TicketStatusTodo,
	}, "chief-1", time.Now()); err != nil {
		t.Fatalf("create: %v", err)
	}

	rows := d.ticketRows(store.TicketListFilter{})
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one", rows)
	}
	if rows[0].Description != "move the store to X" {
		t.Fatalf("ticket_list row description = %q, want the brief", rows[0].Description)
	}
}
