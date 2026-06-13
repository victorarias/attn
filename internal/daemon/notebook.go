package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/hooks"
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
		if strings.HasPrefix(configured, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("resolve home directory: %w", err)
			}
			return filepath.Join(home, configured[2:]), nil
		}
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

// ensureNotebookScaffold creates the notebook scaffold if absent (idempotent),
// broadcasting notebook_changed only when it actually created files. Returns the
// resolved root so callers can report it.
func (d *Daemon) ensureNotebookScaffold() (root string, created bool, err error) {
	store, err := d.notebookStoreFor()
	if err != nil {
		return "", false, err
	}
	created, err = store.EnsureScaffold()
	if err != nil {
		return "", false, err
	}
	if created {
		d.broadcastNotebookChanged(originAgent, notebook.ScaffoldPaths()...)
	}
	return store.Root(), created, nil
}

func (d *Daemon) handleNotebookInit(conn net.Conn) {
	root, created, err := d.ensureNotebookScaffold()
	if err != nil {
		d.sendError(conn, "notebook init: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:           true,
		NotebookInit: &protocol.NotebookInitResult{Root: root, Created: created},
	})
}

// handleNotebookGuide returns the canonical notebook operating guidance (the
// single source for both the at-launch injection and the live pull). When the
// requesting session currently holds the chief role, it also ensures the
// notebook scaffold exists so the chief always has a real notebook to work in.
func (d *Daemon) handleNotebookGuide(conn net.Conn, msg *protocol.NotebookGuideMessage) {
	root, err := d.notebookRoot()
	if err != nil {
		d.sendError(conn, "notebook: "+err.Error())
		return
	}
	sessionID := strings.TrimSpace(protocol.Deref(msg.SessionID))
	sessionIsChief := sessionID != "" && sessionID == d.chiefOfStaffSessionID()
	if sessionIsChief {
		if _, _, serr := d.ensureNotebookScaffold(); serr != nil {
			d.logf("notebook guide: ensure scaffold failed: %v", serr)
		}
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		NotebookGuide: &protocol.NotebookGuideResult{
			Guidance:       hooks.NotebookGuidance(root),
			Root:           root,
			SessionIsChief: sessionIsChief,
		},
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
		// Broadcast the normalized relative path so notebook_changed always
		// carries the same form as notebook_list/append (a leading-slash or
		// un-cleaned write path would otherwise miss a consumer keyed on it).
		changed := msg.Path
		if rel, cerr := notebook.CleanPath(msg.Path); cerr == nil {
			changed = rel
		}
		d.broadcastNotebookChanged(originAgent, changed)
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

// notebookActivationPrompt is the bounded doorbell typed into a freshly-promoted
// chief session's PTY. It carries only an instruction to pull guidance from the
// daemon-owned CLI — never guidance content itself. This is the safe exception
// to the chief-of-staff "no arbitrary PTY content" boundary: a fixed trigger,
// content pulled deterministically from `attn notebook guide`.
const notebookActivationPrompt = "You are now the chief of staff. Run `attn notebook guide` and follow it: your durable memory is the attn Notebook, not this workspace's shared context."

// activateNotebookGuidanceLive types the bounded notebook-activation doorbell
// into a just-promoted chief session's PTY, but only when that session is idle
// or waiting for input — never an agent mid-task. It first ensures the notebook
// scaffold exists. Fire-and-forget: failures are logged, not surfaced.
func (d *Daemon) activateNotebookGuidanceLive(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || d.ptyBackend == nil || d.store == nil {
		return
	}
	if _, _, err := d.ensureNotebookScaffold(); err != nil {
		d.logf("notebook activation: ensure scaffold failed: %v", err)
	}
	session := d.store.Get(sessionID)
	if session == nil {
		d.logf("notebook activation: session %s closed or remote; skipping live trigger", sessionID)
		return
	}
	if session.State != protocol.SessionStateIdle && session.State != protocol.SessionStateWaitingInput {
		d.logf("notebook activation: session %s is %s, not idle/waiting; skipping live trigger", sessionID, session.State)
		return
	}
	// Re-confirm ownership right before typing: the role is a single-holder
	// upsert, so a promotion of another session between the goroutine's launch
	// and here would mean this session is no longer the chief. Never tell a
	// demoted session it is now the chief.
	if d.chiefOfStaffSessionID() != sessionID {
		d.logf("notebook activation: session %s no longer holds the chief role; skipping live trigger", sessionID)
		return
	}
	if err := d.ptyBackend.Input(context.Background(), sessionID, []byte(notebookActivationPrompt)); err != nil {
		d.logf("notebook activation: input prompt failed for %s: %v", sessionID, err)
		return
	}
	time.Sleep(100 * time.Millisecond)
	if err := d.ptyBackend.Input(context.Background(), sessionID, []byte{'\r'}); err != nil {
		d.logf("notebook activation: input enter failed for %s: %v", sessionID, err)
	}
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
