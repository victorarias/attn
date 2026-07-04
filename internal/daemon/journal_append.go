package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

// handleJournalAppend serializes an append to the notebook's dated daily journal
// (journal/<date>.md) through the daemon's single cached notebook.Store per root
// (see notebookStoreFor), so an agent's journal write can never race the keeper's
// own writes to the same file the way a direct file edit does. date defaults to
// today in the daemon's local timezone; a malformed date is rejected by the store
// itself rather than re-validated here.
func (d *Daemon) handleJournalAppend(conn net.Conn, msg *protocol.JournalAppendMessage) {
	entry := strings.TrimSpace(msg.Entry)
	if entry == "" {
		d.sendError(conn, "journal append: entry is required")
		return
	}
	date := ""
	if msg.Date != nil {
		date = strings.TrimSpace(*msg.Date)
	}
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	store, err := d.notebookStoreFor()
	if err != nil {
		d.sendError(conn, "journal append: "+err.Error())
		return
	}
	rel, hash, err := store.AppendJournal(date, entry)
	if err != nil {
		d.sendError(conn, "journal append: "+err.Error())
		return
	}
	// Mirror the daemon's other agent-originated notebook append
	// (AppendInbox in sendNotebookToChiefWSResult): a content-aware self-write so
	// the watcher does not re-announce this write as an external edit, then a
	// broadcast so an open notebook view refreshes.
	d.noteNotebookSelfWrite(notebook.SelfWrite{Rel: rel, Hash: hash})
	d.broadcastNotebookChanged(originAgent, rel)
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		JournalAppendResult: &protocol.JournalAppendResult{
			RelPath: rel,
			Hash:    hash,
		},
	})
}
