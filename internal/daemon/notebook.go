package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

// originAgent labels notebook changes that arrive over the unix-socket CLI —
// agents (and the user) drive that path. The UI (origin "ui"), the dreaming
// pass ("dreaming"), and external edits ("external") arrive on other paths.
const originAgent = "agent"

// notebookStoreFor resolves the active notebook root and returns the daemon's
// single Store for it. The Store is cached and reused so writes serialize
// through one in-process writer; it is rebuilt only when the resolved root
// changes (e.g. the notebook.root setting was updated).
func (d *Daemon) notebookStoreFor() (*notebook.Store, error) {
	root, err := d.notebookRoot()
	if err != nil {
		return nil, err
	}
	d.notebookMu.Lock()
	defer d.notebookMu.Unlock()
	if d.notebookStore == nil || d.notebookStore.Root() != root {
		d.notebookStore = notebook.NewStore(root)
	}
	return d.notebookStore, nil
}

// notebookRoot returns the configured notebook.root, or the profile-derived
// default (~/attn-notebook[-profile], outside ~/.attn) when unset.
func (d *Daemon) notebookRoot() (string, error) {
	if configured := strings.TrimSpace(d.store.GetSetting(SettingNotebookRoot)); configured != "" {
		return configured, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return notebook.DefaultRoot(home, config.Profile()), nil
}

func (d *Daemon) broadcastNotebookChanged(origin string, paths ...string) {
	d.broadcastMessage(protocol.NotebookChangedMessage{
		Event:  protocol.EventNotebookChanged,
		Paths:  paths,
		Origin: origin,
	})
}

func (d *Daemon) handleNotebookInit(conn net.Conn) {
	store, err := d.notebookStoreFor()
	if err != nil {
		d.sendError(conn, "notebook: "+err.Error())
		return
	}
	created, err := store.EnsureScaffold()
	if err != nil {
		d.sendError(conn, "notebook init: "+err.Error())
		return
	}
	if created {
		d.broadcastNotebookChanged(originAgent, notebook.ScaffoldPaths()...)
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:           true,
		NotebookInit: &protocol.NotebookInitResult{Root: store.Root(), Created: created},
	})
}

func (d *Daemon) handleNotebookList(conn net.Conn, msg *protocol.NotebookListMessage) {
	store, err := d.notebookStoreFor()
	if err != nil {
		d.sendError(conn, "notebook: "+err.Error())
		return
	}
	prefix := ""
	if msg.Prefix != nil {
		prefix = *msg.Prefix
	}
	entries, err := store.List(prefix)
	if err != nil {
		d.sendError(conn, "notebook list: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:              true,
		NotebookEntries: notebookEntriesToProtocol(entries),
	})
}

func (d *Daemon) handleNotebookRead(conn net.Conn, msg *protocol.NotebookReadMessage) {
	store, err := d.notebookStoreFor()
	if err != nil {
		d.sendError(conn, "notebook: "+err.Error())
		return
	}
	content, hash, err := store.Read(msg.Path)
	if err != nil {
		d.sendError(conn, "notebook read: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		NotebookRead: &protocol.NotebookReadResult{
			Path:    msg.Path,
			Content: string(content),
			Hash:    hash,
		},
	})
}

func (d *Daemon) handleNotebookWrite(conn net.Conn, msg *protocol.NotebookWriteMessage) {
	store, err := d.notebookStoreFor()
	if err != nil {
		d.sendError(conn, "notebook: "+err.Error())
		return
	}
	baseHash := ""
	if msg.BaseHash != nil {
		baseHash = *msg.BaseHash
	}
	hash, conflict, err := store.Write(msg.Path, []byte(msg.Content), baseHash)
	if err != nil {
		d.sendError(conn, "notebook write: "+err.Error())
		return
	}
	// A conflict is a successful response (conflict:true) the caller reconciles,
	// not a daemon error.
	res := &protocol.NotebookWriteResult{Path: msg.Path}
	if conflict != nil {
		res.Conflict = true
		if conflict.CurrentHash != "" {
			res.CurrentHash = protocol.Ptr(conflict.CurrentHash)
		}
	} else {
		res.Hash = protocol.Ptr(hash)
		d.broadcastNotebookChanged(originAgent, msg.Path)
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, NotebookWrite: res})
}

func (d *Daemon) handleNotebookAppendJournal(conn net.Conn, msg *protocol.NotebookAppendJournalMessage) {
	store, err := d.notebookStoreFor()
	if err != nil {
		d.sendError(conn, "notebook: "+err.Error())
		return
	}
	date := ""
	if msg.Date != nil {
		date = strings.TrimSpace(*msg.Date)
	}
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	relPath, hash, err := store.AppendJournal(date, msg.Entry)
	if err != nil {
		d.sendError(conn, "notebook append: "+err.Error())
		return
	}
	d.broadcastNotebookChanged(originAgent, relPath)
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:            true,
		NotebookWrite: &protocol.NotebookWriteResult{Path: relPath, Hash: protocol.Ptr(hash)},
	})
}

func notebookEntriesToProtocol(entries []notebook.Entry) []protocol.NotebookEntry {
	out := make([]protocol.NotebookEntry, 0, len(entries))
	for _, e := range entries {
		pe := protocol.NotebookEntry{Path: e.Path, Size: int(e.Size)}
		if e.Kind != "" {
			pe.Kind = protocol.Ptr(e.Kind)
		}
		if e.Title != "" {
			pe.Title = protocol.Ptr(e.Title)
		}
		if e.Summary != "" {
			pe.Summary = protocol.Ptr(e.Summary)
		}
		if e.Updated != "" {
			pe.Updated = protocol.Ptr(e.Updated)
		}
		out = append(out, pe)
	}
	return out
}
