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

// originAgent labels notebook changes attn itself makes outside the in-app editor
// — today only scaffold creation. The UI (origin "ui") and external edits
// ("external", incl. agents editing files directly on disk) arrive on other paths.
const originAgent = "agent"

// originExternal labels notebook changes the watcher detects on disk that attn
// did not make itself (an external markdown sync tool, or the user editing files
// directly).
const originExternal = "external"

// originUI labels notebook changes made through the in-app editor (the WS
// notebook_write path), as distinct from agent/CLI writes (origin "agent").
const originUI = "ui"

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
	if d.notebookStore == nil || d.notebookStore.Root() != root {
		d.notebookStore = notebook.NewStore(root)
	}
	store := d.notebookStore
	d.notebookMu.Unlock()
	// Every notebook operation is a chance to (lazily) start watching for
	// external edits. Done after releasing notebookMu — ensureNotebookWatcher
	// takes its own lock — and it no-ops once the active root is already watched.
	d.ensureNotebookWatcher(root)
	return store, nil
}

// ensureNotebookWatcher starts the external-edit watcher for root if it is not
// already watching it. A root that does not exist yet (no notebook on disk) is
// skipped; the next operation after the scaffold is created starts the watcher.
func (d *Daemon) ensureNotebookWatcher(root string) {
	// Never resurrect the watcher during shutdown. Stop() closes d.done before it
	// calls stopNotebookWatcher, so an in-flight notebook handler racing with Stop
	// observes the closed channel here and returns instead of starting a fresh
	// watcher that nothing would ever close (a leaked goroutine + kqueue fd).
	select {
	case <-d.done:
		return
	default:
	}
	d.notebookWatcherMu.Lock()
	defer d.notebookWatcherMu.Unlock()
	if d.notebookWatcher != nil && d.notebookWatchedRoot == root {
		return
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return // nothing to watch yet
	}
	if d.notebookWatcher != nil {
		_ = d.notebookWatcher.Close() // root changed (notebook.root setting edited)
		d.notebookWatcher = nil
		d.notebookWatchedRoot = ""
	}
	w, err := notebook.NewWatcher(root, notebook.DefaultWatchDebounce, func(paths []string) {
		// One filesystem, two surfaces over it: external edits feed both the curated
		// notebook view and the raw fs view. (The watcher only surfaces .md paths
		// today, so fs_changed inherits that limit until the watcher is generalized.)
		d.broadcastNotebookChanged(originExternal, paths...)
		d.broadcastFsChanged(originExternal, paths...)
	})
	if err != nil {
		d.logf("notebook watcher: failed to watch %s: %v", root, err)
		return
	}
	d.notebookWatcher = w
	d.notebookWatchedRoot = root
}

// noteNotebookSelfWrite tells the watcher that attn just wrote these notebook
// paths, so the resulting filesystem events are not reported as external edits.
// Passing the content hash makes suppression content-aware (an external edit that
// races attn's write to the same path within the debounce window is still
// surfaced). Safe to call before the watcher exists (no-op).
func (d *Daemon) noteNotebookSelfWrite(writes ...notebook.SelfWrite) {
	d.notebookWatcherMu.Lock()
	w := d.notebookWatcher
	d.notebookWatcherMu.Unlock()
	w.NoteSelfWrite(writes...)
}

func (d *Daemon) stopNotebookWatcher() {
	d.notebookWatcherMu.Lock()
	w := d.notebookWatcher
	d.notebookWatcher = nil
	d.notebookWatchedRoot = ""
	d.notebookWatcherMu.Unlock()
	_ = w.Close()
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
		// Clean so a settings value with a trailing slash or redundant separators
		// resolves to the same canonical root the store's containment checks expect.
		return filepath.Clean(configured), nil
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
	createdPaths, scaffoldErr := store.EnsureScaffold()
	if len(createdPaths) > 0 {
		// Record/broadcast exactly the files attn wrote — even if a later file in
		// the scaffold failed — so a partial scaffold's own writes are not later
		// mis-surfaced as external edits. (An idempotent re-run writes nothing, so
		// recording all reserved paths would wrongly suppress real external edits.)
		// Scaffold content is static and written once, so unconditional suppression
		// (empty hash) is sufficient here.
		writes := make([]notebook.SelfWrite, len(createdPaths))
		for i, p := range createdPaths {
			writes[i] = notebook.SelfWrite{Rel: p}
		}
		d.noteNotebookSelfWrite(writes...)
		d.broadcastNotebookChanged(originAgent, createdPaths...)
		// The root now exists; start watching it for external edits (the first
		// scaffold may have run before any operation could start the watcher).
		d.ensureNotebookWatcher(store.Root())
	}
	if scaffoldErr != nil {
		return "", false, scaffoldErr
	}
	return store.Root(), len(createdPaths) > 0, nil
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

// notebookActivationPrompt is the bounded doorbell typed into a freshly-promoted
// chief session's PTY. It carries only a pointer to the chief's notebook on disk
// — never guidance content itself. This is the safe exception to the
// chief-of-staff "no arbitrary PTY content" boundary: a fixed trigger pointing at
// a deterministic, attn-authored file. The full operating guidance still flows
// into the system prompt at launch via hooks.NotebookGuidance; a live promotion
// can't reach the system prompt, so it points the agent at the notebook's index.
func notebookActivationPrompt(root string) string {
	return fmt.Sprintf("You are now the chief of staff. Your durable home is the attn Notebook, not this workspace's shared context — read %s to get oriented.", filepath.Join(root, "index.md"))
}

// activateNotebookGuidanceLive types the bounded notebook-activation doorbell
// into a just-promoted chief session's PTY, but only when that session is idle
// or waiting for input — never an agent mid-task. It first ensures the notebook
// scaffold exists. Fire-and-forget: failures are logged, not surfaced.
func (d *Daemon) activateNotebookGuidanceLive(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || d.ptyBackend == nil || d.store == nil {
		return
	}
	root, err := d.notebookRoot()
	if err != nil {
		d.logf("notebook activation: resolve root failed for %s: %v", sessionID, err)
		return
	}
	if _, _, serr := d.ensureNotebookScaffold(); serr != nil {
		d.logf("notebook activation: ensure scaffold failed: %v", serr)
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
	if err := d.ptyBackend.Input(context.Background(), sessionID, []byte(notebookActivationPrompt(root))); err != nil {
		d.logf("notebook activation: input prompt failed for %s: %v", sessionID, err)
		return
	}
	time.Sleep(100 * time.Millisecond)
	if err := d.ptyBackend.Input(context.Background(), sessionID, []byte{'\r'}); err != nil {
		d.logf("notebook activation: input enter failed for %s: %v", sessionID, err)
	}
}

// sendNotebookListWSResult lists notes and replies to a websocket client with a
// notebook_list_result event correlated by requestID. This WS path is the only
// notebook list path; the former unix-socket CLI list command was removed.
func (d *Daemon) sendNotebookListWSResult(client *wsClient, requestID, prefix string) {
	var entries []protocol.NotebookEntry
	store, err := d.notebookStoreFor()
	if err == nil {
		var list []notebook.Entry
		if list, err = store.List(prefix); err == nil {
			entries = notebookEntriesToProtocol(list)
		}
	}
	msg := protocol.NotebookListResultMessage{
		Event:     protocol.EventNotebookListResult,
		RequestID: requestID,
		Success:   err == nil,
		Entries:   entries,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// sendNotebookReadWSResult reads one note and replies with a notebook_read_result
// event correlated by requestID.
func (d *Daemon) sendNotebookReadWSResult(client *wsClient, requestID, path string) {
	var result *protocol.NotebookReadResult
	store, err := d.notebookStoreFor()
	if err == nil {
		var content []byte
		var hash string
		if content, hash, err = store.Read(path); err == nil {
			result = &protocol.NotebookReadResult{Path: path, Content: string(content), Hash: hash}
		}
	}
	msg := protocol.NotebookReadResultMessage{
		Event:     protocol.EventNotebookReadResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// sendNotebookBacklinksWSResult computes backlinks and replies with a
// notebook_backlinks_result event correlated by requestID.
func (d *Daemon) sendNotebookBacklinksWSResult(client *wsClient, requestID, path string) {
	var entries []protocol.NotebookEntry
	store, err := d.notebookStoreFor()
	if err == nil {
		var list []notebook.Entry
		if list, err = store.Backlinks(path); err == nil {
			entries = notebookEntriesToProtocol(list)
		}
	}
	msg := protocol.NotebookBacklinksResultMessage{
		Event:     protocol.EventNotebookBacklinksResult,
		RequestID: requestID,
		Success:   err == nil,
		Entries:   entries,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// sendNotebookWriteWSResult performs a hash-CAS write on behalf of the in-app
// editor and replies with a notebook_write_result event correlated by requestID.
// A conflict (the file changed on disk since the editor loaded it) is a
// successful result carrying conflict=true for the UI to reconcile, not an error.
func (d *Daemon) sendNotebookWriteWSResult(client *wsClient, requestID, path, content, baseHash string) {
	var result *protocol.NotebookWriteResult
	store, err := d.notebookStoreFor()
	if err == nil {
		// Normalize to the form notebook_list/append/watcher key on, so the
		// self-write record and the broadcast agree.
		changed := path
		if rel, cerr := notebook.CleanPath(path); cerr == nil {
			changed = rel
		}
		var hash string
		var conflict *notebook.Conflict
		if hash, conflict, err = store.Write(path, []byte(content), baseHash); err == nil {
			// Echo the normalized path so result.path matches the form
			// notebook_list/notebook_changed key on, not the raw input.
			result = &protocol.NotebookWriteResult{Path: changed}
			if conflict != nil {
				result.Conflict = true
				if conflict.CurrentHash != "" {
					result.CurrentHash = protocol.Ptr(conflict.CurrentHash)
				}
			} else {
				result.Hash = protocol.Ptr(hash)
				// Content-aware self-write so the watcher does not echo this UI edit
				// as an external one: the recorded hash lets a racing external edit of
				// the same path within the debounce window still surface.
				d.noteNotebookSelfWrite(notebook.SelfWrite{Rel: changed, Hash: hash})
				d.broadcastNotebookChanged(originUI, changed)
			}
		}
	}
	msg := protocol.NotebookWriteResultMessage{
		Event:     protocol.EventNotebookWriteResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// maxInboxSelection caps a single "send to chief" selection. The whole inbox note
// is still bounded by MaxFileSize; this rejects one runaway paste up front with a
// clear error rather than letting it bloat the note.
const maxInboxSelection = 32 << 10 // 32 KiB

// chiefInboxNudgePrompt is the bounded doorbell typed into a live chief session
// when a selection lands in its inbox. Like the activation prompt, it carries only
// a pointer to the inbox note on disk — never the selection content itself (that is
// written to the inbox note, the daemon's job, never streamed into the PTY).
func chiefInboxNudgePrompt(root string) string {
	return fmt.Sprintf("A new selection was added to your Notebook inbox. Read %s to see it.", filepath.Join(root, notebook.FileInbox))
}

// sendNotebookToChiefWSResult delivers a Notebook selection to the chief of staff:
// it appends the selection to the chief inbox note (the daemon is the sole writer)
// and, when a chief session is live and idle/waiting, fires a bounded PTY nudge.
// The inbox delivery is the durable channel; the nudge is best-effort immediacy.
// The UI never messages the chief directly — it only hands the selection here.
func (d *Daemon) sendNotebookToChiefWSResult(client *wsClient, requestID, sourcePath, selection string) {
	var result *protocol.NotebookSendToChiefResult
	store, err := d.notebookStoreFor()
	if err == nil {
		if strings.TrimSpace(selection) == "" {
			err = fmt.Errorf("notebook: empty selection")
		} else if len(selection) > maxInboxSelection {
			err = fmt.Errorf("notebook: selection exceeds %d bytes", maxInboxSelection)
		}
	}
	if err == nil {
		var relPath, hash string
		if relPath, hash, err = store.AppendInbox(formatChiefInboxEntry(sourcePath, selection)); err == nil {
			// Content-aware self-write + broadcast so the open browser refreshes but
			// the watcher does not re-announce attn's own write as an external edit.
			d.noteNotebookSelfWrite(notebook.SelfWrite{Rel: relPath, Hash: hash})
			d.broadcastNotebookChanged(originUI, relPath)
			result = &protocol.NotebookSendToChiefResult{
				Path:   relPath,
				Nudged: d.nudgeChiefOfStaff(chiefInboxNudgePrompt(store.Root())),
			}
		}
	}
	msg := protocol.NotebookSendToChiefResultMessage{
		Event:     protocol.EventNotebookSendToChiefResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// formatChiefInboxEntry renders one inbox entry: a heading identifying the source
// note followed by the selection as a markdown blockquote. Both the path and the
// selection are sanitized so no input can corrupt the note's markdown structure.
func formatChiefInboxEntry(sourcePath, selection string) string {
	var b strings.Builder
	b.WriteString(chiefInboxSourceHeading(sourcePath))
	b.WriteString("\n\n")
	// Normalize CR/CRLF so a non-UI client's line endings don't leave stray
	// carriage returns on every blockquoted line.
	normalized := strings.ReplaceAll(selection, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	for _, line := range strings.Split(strings.TrimRight(normalized, "\n"), "\n") {
		b.WriteString("> ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// chiefInboxSourceHeading renders the "## From ..." heading for a source note.
// CleanPath validates path segments but permits characters that would corrupt the
// markdown — control chars, spaces, brackets, parens, backticks — and the notebook
// root is externally syncable, so such filenames are possible. Control chars and
// backticks are dropped (they break every rendering); a clean path then renders a
// clickable root-absolute backlink, while anything else renders as inline code so
// the heading is always well-formed and the path is shown verbatim.
func chiefInboxSourceHeading(sourcePath string) string {
	rel, err := notebook.CleanPath(sourcePath)
	if err != nil {
		return "## From the Notebook"
	}
	rel = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '`' {
			return -1
		}
		return r
	}, rel)
	if rel == "" {
		return "## From the Notebook"
	}
	// A bare path with none of these characters round-trips through the link
	// parser (which stops a target at the first ')' or whitespace) and contains no
	// nested [..](..) that a heading would auto-link.
	if strings.IndexAny(rel, " \t()[]<>") < 0 {
		return fmt.Sprintf("## From [/%s](/%s)", rel, rel)
	}
	return fmt.Sprintf("## From `/%s`", rel)
}

func notebookEntriesToProtocol(entries []notebook.Entry) []protocol.NotebookEntry {
	out := make([]protocol.NotebookEntry, 0, len(entries))
	for _, e := range entries {
		pe := protocol.NotebookEntry{Path: e.Path, Size: int(e.Size)}
		if e.Type != "" {
			pe.Type = protocol.Ptr(e.Type)
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
