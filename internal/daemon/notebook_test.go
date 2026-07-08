package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/notebook"
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
// pipe, returning the decoded Response. notebook_guide is the only notebook
// command still served on the unix socket; the rest moved to the WS path (the
// WS-result helpers and writeNote/listNotes below) when the CLI was removed.
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

// writeNote populates a note through the surviving WS write path (the in-app
// editor's path) and fails the test if the write did not apply. The former
// unix-socket write command was removed, so tests set up notebook content here.
func writeNote(t *testing.T, d *Daemon, path, content string) {
	t.Helper()
	res := writeNoteCAS(t, d, path, content, "")
	if !res.Success || res.Result == nil || res.Result.Conflict {
		t.Fatalf("setup write %q failed: %+v (err %v)", path, res.Result, res.Error)
	}
}

// writeNoteCAS performs a hash-CAS write over the WS path and returns the decoded
// result event (a successful result may carry conflict=true), so a test can drive
// a deliberate conflict. Uses a throwaway client; the originUI broadcast goes to
// the hub (not this client), so only the synchronous result is delivered here.
func writeNoteCAS(t *testing.T, d *Daemon, path, content, baseHash string) protocol.NotebookWriteResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendNotebookWriteWSResult(client, "setup-write", path, content, baseHash)
	var res protocol.NotebookWriteResultMessage
	readNotebookWSEvent(t, client.send, &res)
	return res
}

// listNotes lists notes over the WS path (the only surviving list path) and
// returns the entries. Like any notebook operation it lazily starts the
// external-edit watcher, so watcher tests use it to "touch" the notebook.
func listNotes(t *testing.T, d *Daemon, prefix string) []protocol.NotebookEntry {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 8)}
	d.sendNotebookListWSResult(client, "setup-list", prefix)
	var res protocol.NotebookListResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success {
		t.Fatalf("list(%q) failed: %v", prefix, res.Error)
	}
	return res.Entries
}

// readNotebookWSEvent reads one outbound message from a client's send channel and
// decodes it into target, failing if none arrives.
func readNotebookWSEvent(t *testing.T, ch chan outboundMessage, target any) {
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

// The websocket read path carries notebook reads back as result events
// correlated by request_id — distinct from the unix CLI's synchronous Response.
func TestNotebookReadWSResultCorrelatesRequest(t *testing.T) {
	d := newNotebookDaemon(t)
	if _, _, err := d.ensureNotebookScaffold(); err != nil {
		t.Fatal(err)
	}
	client := &wsClient{send: make(chan outboundMessage, 4)}

	d.sendNotebookReadWSResult(client, "req-1", "/index.md")
	var read protocol.NotebookReadResultMessage
	readNotebookWSEvent(t, client.send, &read)
	if read.Event != protocol.EventNotebookReadResult || read.RequestID != "req-1" || !read.Success {
		t.Fatalf("read result = %+v, want success notebook_read_result for req-1", read)
	}
	if read.Result == nil || read.Result.Content == "" || read.Result.Hash == "" {
		t.Fatalf("read result payload = %+v", read.Result)
	}

	// A missing note is a failed result (error set), not a panic or empty success.
	d.sendNotebookReadWSResult(client, "req-2", "/does/not/exist.md")
	var missing protocol.NotebookReadResultMessage
	readNotebookWSEvent(t, client.send, &missing)
	if missing.RequestID != "req-2" || missing.Success || missing.Error == nil {
		t.Fatalf("missing read result = %+v, want failure with error", missing)
	}
}

func TestNotebookListAndBacklinksWSResults(t *testing.T) {
	d := newNotebookDaemon(t)
	writeNote(t, d, "knowledge/areas/a.md", "---\ntype: note\n---\nbody\n")
	writeNote(t, d, "knowledge/areas/b.md", "---\ntype: note\n---\nsee [a](/knowledge/areas/a.md)\n")
	client := &wsClient{send: make(chan outboundMessage, 4)}

	d.sendNotebookListWSResult(client, "list-1", "")
	var list protocol.NotebookListResultMessage
	readNotebookWSEvent(t, client.send, &list)
	if list.Event != protocol.EventNotebookListResult || list.RequestID != "list-1" || !list.Success || len(list.Entries) < 2 {
		t.Fatalf("list result = %+v, want >=2 entries for list-1", list)
	}

	d.sendNotebookBacklinksWSResult(client, "back-1", "/knowledge/areas/a.md")
	var back protocol.NotebookBacklinksResultMessage
	readNotebookWSEvent(t, client.send, &back)
	if back.Event != protocol.EventNotebookBacklinksResult || back.RequestID != "back-1" || !back.Success ||
		len(back.Entries) != 1 || back.Entries[0].Path != "knowledge/areas/b.md" {
		t.Fatalf("backlinks result = %+v, want [knowledge/areas/b.md] for back-1", back)
	}
}

// The in-app editor saves over the WS notebook_write path: a hash-CAS write
// replies with a notebook_write_result, and a stale base hash comes back as a
// successful result carrying conflict=true (for the UI to reconcile), not an error.
func TestNotebookWriteWSResultSaveAndConflict(t *testing.T) {
	d := newNotebookDaemon(t)
	client := &wsClient{send: make(chan outboundMessage, 8)}

	d.sendNotebookWriteWSResult(client, "w1", "knowledge/areas/foo.md", "---\ntype: note\n---\nv1\n", "")
	var create protocol.NotebookWriteResultMessage
	readNotebookWSEvent(t, client.send, &create)
	if create.Event != protocol.EventNotebookWriteResult || create.RequestID != "w1" || !create.Success ||
		create.Result == nil || create.Result.Conflict || create.Result.Hash == nil {
		t.Fatalf("create result = %+v", create.Result)
	}
	h1 := *create.Result.Hash

	// Stale base hash => success with conflict=true carrying the current hash.
	d.sendNotebookWriteWSResult(client, "w2", "knowledge/areas/foo.md", "v2", "deadbeef")
	var conflict protocol.NotebookWriteResultMessage
	readNotebookWSEvent(t, client.send, &conflict)
	if !conflict.Success || conflict.Result == nil || !conflict.Result.Conflict ||
		conflict.Result.CurrentHash == nil || *conflict.Result.CurrentHash != h1 {
		t.Fatalf("stale write result = %+v, want conflict with current hash %q", conflict.Result, h1)
	}

	// Correct base hash => the edit applies.
	d.sendNotebookWriteWSResult(client, "w3", "knowledge/areas/foo.md", "---\ntype: note\n---\nv2\n", h1)
	var ok protocol.NotebookWriteResultMessage
	readNotebookWSEvent(t, client.send, &ok)
	if !ok.Success || ok.Result == nil || ok.Result.Conflict || ok.Result.Hash == nil {
		t.Fatalf("CAS edit result = %+v", ok.Result)
	}
}

// The notebook reads must dispatch correctly through handleClientMessage — the
// path the real frontend uses — not just when the WS-result handlers are called
// directly. This covers the request_id/prefix/path argument extraction in the
// websocket switch (a swapped Deref on notebook_list would compile and ship).
func TestNotebookReadsDispatchThroughClientMessage(t *testing.T) {
	d := newNotebookDaemon(t)
	writeNote(t, d, "knowledge/areas/a.md", "---\ntype: note\n---\nbody\n")
	writeNote(t, d, "knowledge/areas/b.md", "---\ntype: note\n---\nsee [a](/knowledge/areas/a.md)\n")
	writeNote(t, d, "journal/2026-06-13.md", "---\ntype: journal\n---\nentry\n")

	client := newWorkspaceProtocolTestClient()
	client.setIdentity("test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	// notebook_list with BOTH request_id and prefix set: a swapped Deref would
	// put the prefix in request_id (and vice versa), so we assert the result is
	// correlated to "rl" AND the prefix actually scoped the result to knowledge/.
	d.handleClientMessage(client, []byte(`{"cmd":"notebook_list","request_id":"rl","prefix":"/knowledge"}`))
	var list protocol.NotebookListResultMessage
	readNotebookWSEvent(t, client.send, &list)
	if list.Event != protocol.EventNotebookListResult || list.RequestID != "rl" || !list.Success {
		t.Fatalf("list result = %+v, want success notebook_list_result for rl", list)
	}
	for _, e := range list.Entries {
		if !strings.HasPrefix(e.Path, "knowledge/") {
			t.Fatalf("prefix not applied: list returned %q outside knowledge/", e.Path)
		}
	}
	if len(list.Entries) != 2 {
		t.Fatalf("list entries = %d, want 2 under knowledge/", len(list.Entries))
	}

	d.handleClientMessage(client, []byte(`{"cmd":"notebook_read","request_id":"rr","path":"/knowledge/areas/a.md"}`))
	var read protocol.NotebookReadResultMessage
	readNotebookWSEvent(t, client.send, &read)
	if read.Event != protocol.EventNotebookReadResult || read.RequestID != "rr" || !read.Success ||
		read.Result == nil || read.Result.Path != "/knowledge/areas/a.md" {
		t.Fatalf("read result = %+v, want success notebook_read_result for rr", read)
	}

	d.handleClientMessage(client, []byte(`{"cmd":"notebook_backlinks","request_id":"rb","path":"/knowledge/areas/a.md"}`))
	var back protocol.NotebookBacklinksResultMessage
	readNotebookWSEvent(t, client.send, &back)
	if back.Event != protocol.EventNotebookBacklinksResult || back.RequestID != "rb" || !back.Success ||
		len(back.Entries) != 1 || back.Entries[0].Path != "knowledge/areas/b.md" {
		t.Fatalf("backlinks result = %+v, want [knowledge/areas/b.md] for rb", back)
	}
}

// notebook_write must dispatch through handleClientMessage with its request_id,
// path, content, and base_hash all extracted correctly (a swapped/dropped Deref
// would compile and ship). Covers a create, a read-back, and a stale base_hash
// conflict — the editor's full save round-trip over the WS path.
func TestNotebookWriteDispatchesThroughClientMessage(t *testing.T) {
	d := newNotebookDaemon(t)
	client := newWorkspaceProtocolTestClient()
	client.setIdentity("test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.handleClientMessage(client, []byte(`{"cmd":"notebook_write","request_id":"w1","path":"/knowledge/areas/a.md","content":"---\ntype: note\n---\nbody\n"}`))
	var res protocol.NotebookWriteResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Event != protocol.EventNotebookWriteResult || res.RequestID != "w1" || !res.Success ||
		res.Result == nil || res.Result.Conflict || res.Result.Hash == nil {
		t.Fatalf("write dispatch result = %+v", res)
	}
	// The echoed result path is normalized (not the raw leading-slash input), so
	// it matches the form notebook_list/notebook_changed key on.
	if res.Result.Path != "knowledge/areas/a.md" {
		t.Fatalf("result path = %q, want normalized knowledge/areas/a.md", res.Result.Path)
	}

	// The note is readable with the hash the write returned.
	d.handleClientMessage(client, []byte(`{"cmd":"notebook_read","request_id":"r1","path":"/knowledge/areas/a.md"}`))
	var read protocol.NotebookReadResultMessage
	readNotebookWSEvent(t, client.send, &read)
	if !read.Success || read.Result == nil || read.Result.Hash != *res.Result.Hash {
		t.Fatalf("read after write dispatch = %+v, want hash %q", read.Result, *res.Result.Hash)
	}

	// A stale base_hash exercises base_hash extraction and returns a conflict.
	d.handleClientMessage(client, []byte(`{"cmd":"notebook_write","request_id":"w2","path":"/knowledge/areas/a.md","content":"x","base_hash":"deadbeef"}`))
	var conflict protocol.NotebookWriteResultMessage
	readNotebookWSEvent(t, client.send, &conflict)
	if !conflict.Success || conflict.Result == nil || !conflict.Result.Conflict {
		t.Fatalf("stale base_hash dispatch = %+v, want conflict", conflict.Result)
	}
}

// The watcher reports edits made on disk outside attn (Obsidian, external sync)
// as origin=external, but suppresses attn's own writes so they don't echo.
func TestNotebookWatcherReportsExternalEditsNotSelfWrites(t *testing.T) {
	d := newNotebookDaemon(t)
	root := d.store.GetSetting(SettingNotebookRoot)
	client := &wsClient{send: make(chan outboundMessage, 64)}
	d.wsHub.clients[client] = true
	go d.wsHub.run()

	// Touch the notebook so the lazy watcher starts (root already exists), then
	// let the watch registration settle before mutating the tree.
	listNotes(t, d, "")
	time.Sleep(80 * time.Millisecond)

	// attn's own write records the path as a self-write before it lands, so the
	// resulting filesystem event must not be reported as external.
	writeNote(t, d, "own.md", "---\ntype: note\n---\nattn wrote this\n")
	// An edit straight to disk (bypassing the daemon) must surface as external.
	if err := os.WriteFile(filepath.Join(root, "ext.md"),
		[]byte("---\ntype: note\n---\nedited externally\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := waitForExternalNotebookChange(t, client.send)
	if !slices.Contains(ext, "ext.md") {
		t.Fatalf("external change %v missing ext.md", ext)
	}
	if slices.Contains(ext, "own.md") {
		t.Fatalf("external change %v wrongly included attn's own write own.md", ext)
	}
}

// A CAS-conflicting write performs no file mutation, so it must NOT leave a
// self-write record that suppresses a later genuine external edit of that path.
func TestNotebookWatcherReportsExternalEditAfterConflictingWrite(t *testing.T) {
	d := newNotebookDaemon(t)
	root := d.store.GetSetting(SettingNotebookRoot)
	client := &wsClient{send: make(chan outboundMessage, 64)}
	d.wsHub.clients[client] = true
	go d.wsHub.run()

	listNotes(t, d, "")
	time.Sleep(80 * time.Millisecond)

	// CAS write against a missing file => conflict, no write, no event.
	resp := writeNoteCAS(t, d, "doc.md", "x", "deadbeef")
	if resp.Result == nil || !resp.Result.Conflict {
		t.Fatalf("expected a conflict, got %+v (err %v)", resp.Result, resp.Error)
	}

	// The external edit of that same path must still surface as external.
	if err := os.WriteFile(filepath.Join(root, "doc.md"),
		[]byte("---\ntype: note\n---\nexternally created\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ext := waitForExternalNotebookChange(t, client.send)
	if !slices.Contains(ext, "doc.md") {
		t.Fatalf("external change %v missing doc.md — a conflicting write wrongly suppressed it", ext)
	}
}

// Changing notebook.root restarts the watcher on the new root and stops
// reporting the old one.
func TestNotebookWatcherFollowsRootChange(t *testing.T) {
	d := newNotebookDaemon(t)
	rootA := d.store.GetSetting(SettingNotebookRoot)
	rootB := t.TempDir()
	client := &wsClient{send: make(chan outboundMessage, 64)}
	d.wsHub.clients[client] = true
	go d.wsHub.run()

	listNotes(t, d, "")
	time.Sleep(80 * time.Millisecond)

	// Repoint the root and drive an op so ensureNotebookWatcher restarts on B.
	d.store.SetSetting(SettingNotebookRoot, rootB)
	listNotes(t, d, "")
	time.Sleep(80 * time.Millisecond)

	// An edit under the new root is reported.
	if err := os.WriteFile(filepath.Join(rootB, "b.md"), []byte("# b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ext := waitForExternalNotebookChange(t, client.send)
	if !slices.Contains(ext, "b.md") {
		t.Fatalf("edit under the new root was not reported: %v", ext)
	}

	// An edit under the OLD root is no longer reported (watcher moved off it).
	if err := os.WriteFile(filepath.Join(rootA, "a.md"), []byte("# a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expectNoExternalNotebookChange(t, client.send)
}

// A genuine external edit that races attn's own write to the SAME path within
// the debounce window must still surface: suppression is content-aware, so once
// the on-disk bytes no longer match what attn wrote, the self-write must not
// swallow the external edit.
func TestNotebookWatcherSurfacesSameWindowExternalEdit(t *testing.T) {
	d := newNotebookDaemon(t)
	root := d.store.GetSetting(SettingNotebookRoot)
	client := &wsClient{send: make(chan outboundMessage, 64)}
	d.wsHub.clients[client] = true
	go d.wsHub.run()

	listNotes(t, d, "")
	time.Sleep(80 * time.Millisecond)

	// attn writes the note (handler records a content-aware self-write)...
	writeNote(t, d, "race.md", "---\ntype: note\n---\nattn wrote this\n")
	// ...and an external tool immediately overwrites the SAME path with different
	// bytes, within the same debounce window.
	if err := os.WriteFile(filepath.Join(root, "race.md"),
		[]byte("---\ntype: note\n---\nexternal overwrote it\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := waitForExternalNotebookChange(t, client.send)
	if !slices.Contains(ext, "race.md") {
		t.Fatalf("external change %v missing race.md — a same-window external edit was wrongly suppressed", ext)
	}
}

// After shutdown, an in-flight notebook handler racing Stop must not resurrect
// the watcher. Stop closes d.done before stopping the watcher, and
// ensureNotebookWatcher bails on a closed d.done, so no orphan watcher (a leaked
// goroutine + kqueue fd that nothing would ever close) is left behind.
func TestEnsureNotebookWatcherDoesNotResurrectAfterShutdown(t *testing.T) {
	d := newNotebookDaemon(t)
	root := d.store.GetSetting(SettingNotebookRoot)

	// A notebook op on an existing root starts the watcher lazily.
	listNotes(t, d, "")
	d.notebookWatcherMu.Lock()
	started := d.notebookWatcher != nil
	d.notebookWatcherMu.Unlock()
	if !started {
		t.Fatal("watcher should have started after a notebook op on an existing root")
	}

	// Mirror Stop's shutdown prefix: close done, then stop the watcher.
	close(d.done)
	d.stopNotebookWatcher()

	// The racing handler's ensureNotebookWatcher must observe the closed d.done
	// and return without starting a fresh watcher.
	d.ensureNotebookWatcher(root)
	d.notebookWatcherMu.Lock()
	resurrected := d.notebookWatcher != nil
	d.notebookWatcherMu.Unlock()
	if resurrected {
		t.Fatal("ensureNotebookWatcher resurrected the watcher after shutdown (leaks a goroutine + kqueue fd)")
	}
}

// waitForExternalNotebookChange returns the paths of the first origin=external
// notebook_changed broadcast, skipping origin=agent (and any non-notebook) events.
func waitForExternalNotebookChange(t *testing.T, ch chan outboundMessage) []string {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case msg := <-ch:
			var ev protocol.NotebookChangedMessage
			if err := json.Unmarshal(msg.payload, &ev); err != nil {
				continue
			}
			if ev.Event == protocol.EventNotebookChanged && ev.Origin == originExternal {
				return ev.Paths
			}
		case <-deadline:
			t.Fatal("no external notebook change was broadcast")
			return nil
		}
	}
}

// expectNoExternalNotebookChange fails if any origin=external notebook_changed
// arrives within a short window (origin=agent and other events are ignored).
func expectNoExternalNotebookChange(t *testing.T, ch chan outboundMessage) {
	t.Helper()
	deadline := time.After(600 * time.Millisecond)
	for {
		select {
		case msg := <-ch:
			var ev protocol.NotebookChangedMessage
			if err := json.Unmarshal(msg.payload, &ev); err != nil {
				continue
			}
			if ev.Event == protocol.EventNotebookChanged && ev.Origin == originExternal {
				t.Fatalf("unexpected external notebook change: %v", ev.Paths)
			}
		case <-deadline:
			return
		}
	}
}

func addIdleNotebookSession(d *Daemon, id string, state protocol.SessionState) {
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: id, Agent: protocol.SessionAgentClaude,
		Directory: "/tmp/" + id, WorkspaceID: "workspace-" + id,
		State: state, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

// notebook_guide is the single source of operating guidance. session_is_chief is
// true only for the session that holds the chief role, and a chief request also
// ensures the scaffold exists so the chief has a real notebook to work in.
func TestNotebookGuideChiefVsNonChief(t *testing.T) {
	d := newNotebookDaemon(t)
	wantRoot := d.store.GetSetting(SettingNotebookRoot)
	addIdleNotebookSession(d, "chief", protocol.SessionStateIdle)
	addIdleNotebookSession(d, "worker", protocol.SessionStateIdle)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	chief := sendNotebookCmd(t, d, protocol.NotebookGuideMessage{Cmd: protocol.CmdNotebookGuide, SessionID: protocol.Ptr("chief")})
	if chief.NotebookGuide == nil || !chief.NotebookGuide.SessionIsChief {
		t.Fatalf("chief guide = %+v, want session_is_chief=true", chief.NotebookGuide)
	}
	if chief.NotebookGuide.Root != wantRoot || chief.NotebookGuide.Guidance == "" {
		t.Fatalf("chief guide = %+v, want root=%q and non-empty guidance", chief.NotebookGuide, wantRoot)
	}
	if !strings.Contains(chief.NotebookGuide.Guidance, "arm a harness Monitor") {
		t.Fatalf("Claude chief guide should carry the self-monitor watch path: %q", chief.NotebookGuide.Guidance)
	}

	// The chief request ensured the scaffold exists, including the reserved files.
	entries := listNotes(t, d, "")
	found := map[string]bool{}
	for _, e := range entries {
		found[e.Path] = true
	}
	for _, want := range []string{"index.md", "log.md", "knowledge/index.md"} {
		if !found[want] {
			t.Fatalf("chief guide should have scaffolded %q; got %v", want, entries)
		}
	}

	worker := sendNotebookCmd(t, d, protocol.NotebookGuideMessage{Cmd: protocol.CmdNotebookGuide, SessionID: protocol.Ptr("worker")})
	if worker.NotebookGuide == nil || worker.NotebookGuide.SessionIsChief {
		t.Fatalf("worker guide = %+v, want session_is_chief=false", worker.NotebookGuide)
	}
	if worker.NotebookGuide.Guidance == "" {
		t.Fatal("guidance text should be returned regardless of role")
	}
}

func TestNotebookGuideUsesCodexTicketNudgeGuidance(t *testing.T) {
	d := newNotebookDaemon(t)
	addIdleNotebookSession(d, "chief", protocol.SessionStateIdle)
	session := d.store.Get("chief")
	session.Agent = protocol.SessionAgentCodex
	d.store.Add(session)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	response := sendNotebookCmd(t, d, protocol.NotebookGuideMessage{Cmd: protocol.CmdNotebookGuide, SessionID: protocol.Ptr("chief")})
	if response.NotebookGuide == nil {
		t.Fatal("missing notebook guide")
	}
	guidance := response.NotebookGuide.Guidance
	if !strings.Contains(guidance, "ticket nudges are the supported wake-up mechanism") {
		t.Fatalf("Codex chief guide should carry nudge guidance: %q", guidance)
	}
	if strings.Contains(guidance, "arm a harness Monitor") {
		t.Fatalf("Codex chief guide should not carry self-monitor guidance: %q", guidance)
	}
}

// A non-chief request must not scaffold the notebook (only the chief's home is
// auto-created).
func TestNotebookGuideNonChiefDoesNotScaffold(t *testing.T) {
	d := newNotebookDaemon(t)
	addIdleNotebookSession(d, "worker", protocol.SessionStateIdle)

	res := sendNotebookCmd(t, d, protocol.NotebookGuideMessage{Cmd: protocol.CmdNotebookGuide, SessionID: protocol.Ptr("worker")})
	if res.NotebookGuide == nil || res.NotebookGuide.SessionIsChief {
		t.Fatalf("guide = %+v, want session_is_chief=false", res.NotebookGuide)
	}
	if entries := listNotes(t, d, ""); len(entries) != 0 {
		t.Fatalf("non-chief guide should not scaffold; got %v", entries)
	}
}

// recordingBackend captures PTY inputs for live-activation assertions.
func recordingBackend(inputs *[]string, mu *sync.Mutex) *fakeSpawnBackend {
	return &fakeSpawnBackend{onInput: func(_ string, data []byte) {
		mu.Lock()
		*inputs = append(*inputs, string(data))
		mu.Unlock()
	}}
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

// A root-absolute (or otherwise un-normalized) write path must still broadcast
// the normalized relative form, matching notebook_list.
func TestNotebookWriteBroadcastsNormalizedPath(t *testing.T) {
	d := newNotebookDaemon(t)
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.wsHub.clients[client] = true
	go d.wsHub.run()

	// The write goes through a throwaway client; its originUI broadcast reaches the
	// hub-registered client above, which is what asserts the normalized path.
	res := writeNoteCAS(t, d, "/knowledge/areas/foo.md", "---\ntype: note\n---\nx\n", "")
	if !res.Success || res.Result == nil || res.Result.Conflict {
		t.Fatalf("write = %+v (err %v)", res.Result, res.Error)
	}
	// The echoed result path must be normalized too (not the raw leading-slash
	// input), so any consumer keying on it matches notebook_list/changed paths.
	if res.Result.Path != "knowledge/areas/foo.md" {
		t.Fatalf("result path = %q, want normalized knowledge/areas/foo.md", res.Result.Path)
	}
	select {
	case message := <-client.send:
		var event protocol.NotebookChangedMessage
		if err := json.Unmarshal(message.payload, &event); err != nil {
			t.Fatal(err)
		}
		if len(event.Paths) != 1 || event.Paths[0] != "knowledge/areas/foo.md" {
			t.Fatalf("broadcast paths = %v, want normalized [knowledge/areas/foo.md]", event.Paths)
		}
	case <-time.After(time.Second):
		t.Fatal("notebook_changed was not broadcast")
	}
}

// notebook.root must be settable via the validated settings path (empty =
// default, absolute path accepted), and rejected when relative or inside the
// attn data dir (it must stay an external, syncable directory).
func TestValidateNotebookRoot(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	if err := d.validateSetting(SettingNotebookRoot, ""); err != nil {
		t.Fatalf("empty notebook.root (default) should be valid: %v", err)
	}
	if err := d.validateSetting(SettingNotebookRoot, filepath.Join(t.TempDir(), "nb")); err != nil {
		t.Fatalf("absolute notebook.root should be valid: %v", err)
	}
	if err := d.validateSetting(SettingNotebookRoot, "relative/path"); err == nil {
		t.Fatal("relative notebook.root should be rejected")
	}
	inside := filepath.Join(config.DataDir(), "notebook")
	if err := d.validateSetting(SettingNotebookRoot, inside); err == nil {
		t.Fatalf("notebook.root inside the data dir (%s) should be rejected", inside)
	}
}

// The settings payload must surface the daemon-resolved notebook root under the
// read-only notebook.root.effective key so the UI can show where the notebook
// lives even when the override is blank. The key is computed, not stored, and
// must never be accepted by set_setting.
func TestNotebookRootEffectiveSurfacedReadOnly(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	custom := t.TempDir()
	d.store.SetSetting(SettingNotebookRoot, custom)
	settings := d.settingsWithAgentAvailability()
	if got := settings[SettingNotebookRootEffective]; got != custom {
		t.Fatalf("notebook.root.effective = %v, want resolved root %q", got, custom)
	}

	// Blank override still resolves to (and surfaces) the profile default.
	d.store.SetSetting(SettingNotebookRoot, "")
	resolved, err := d.notebookRoot()
	if err != nil {
		t.Fatalf("notebookRoot: %v", err)
	}
	settings = d.settingsWithAgentAvailability()
	if got := settings[SettingNotebookRootEffective]; got != resolved {
		t.Fatalf("notebook.root.effective with default = %v, want %q", got, resolved)
	}

	// The computed key is read-only: set_setting must reject it.
	if err := d.validateSetting(SettingNotebookRootEffective, "/tmp/whatever"); err == nil {
		t.Fatal("notebook.root.effective must not be settable")
	}
}

// readInboxNote returns the inbox note body for assertions.
func readInboxNote(t *testing.T, d *Daemon) string {
	t.Helper()
	store, err := d.notebookStoreFor()
	if err != nil {
		t.Fatalf("notebook store: %v", err)
	}
	content, _, err := store.Read(notebook.FileInbox)
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	return string(content)
}

// Sending a selection to a live, idle chief appends it to the inbox note AND
// fires the bounded PTY nudge (and only that bounded text).
func TestNotebookSendToChiefAppendsAndNudges(t *testing.T) {
	d := newNotebookDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)
	addIdleNotebookSession(d, "chief", protocol.SessionStateIdle)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}
	client := &wsClient{send: make(chan outboundMessage, 4)}

	d.sendNotebookToChiefWSResult(client, "c1", "/knowledge/index.md", "remember this decision")

	var res protocol.NotebookSendToChiefResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Event != protocol.EventNotebookSendToChiefResult || res.RequestID != "c1" || !res.Success ||
		res.Result == nil || res.Result.Path != notebook.FileInbox || !res.Result.Nudged {
		t.Fatalf("send-to-chief result = %+v, want success inbox.md nudged", res.Result)
	}

	// Only the bounded doorbell + Enter was typed into the chief PTY — never the
	// selection content itself (that goes to the inbox note, not the terminal).
	mu.Lock()
	got := append([]string(nil), inputs...)
	mu.Unlock()
	wantNudge := chiefInboxNudgePrompt(d.store.GetSetting(SettingNotebookRoot))
	if len(got) != 2 || got[0] != wantNudge || got[1] != "\r" {
		t.Fatalf("PTY inputs = %q, want [nudge prompt, \\r]", got)
	}

	body := readInboxNote(t, d)
	if !strings.Contains(body, "> remember this decision") || !strings.Contains(body, "(/knowledge/index.md)") {
		t.Fatalf("inbox note = %q, want blockquoted selection + backlink to the source", body)
	}
}

// With no live chief (role unset), the selection still lands in the inbox note —
// the durable channel — but no PTY nudge is sent and nudged is false.
func TestNotebookSendToChiefQueuesWithoutLiveChief(t *testing.T) {
	d := newNotebookDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)
	client := &wsClient{send: make(chan outboundMessage, 4)}

	d.sendNotebookToChiefWSResult(client, "c2", "", "queued for later")

	var res protocol.NotebookSendToChiefResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.Result == nil || res.Result.Path != notebook.FileInbox || res.Result.Nudged {
		t.Fatalf("send-to-chief result = %+v, want success inbox.md not nudged", res.Result)
	}
	mu.Lock()
	n := len(inputs)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("PTY inputs = %d, want 0 with no live chief", n)
	}
	if body := readInboxNote(t, d); !strings.Contains(body, "> queued for later") {
		t.Fatalf("inbox note = %q, want the selection delivered even without a live chief", body)
	}
}

// A busy chief (working/pending) is not interrupted: the inbox delivery happens
// but the nudge is withheld, mirroring the activation doorbell's state gate.
func TestNotebookSendToChiefDoesNotNudgeBusyChief(t *testing.T) {
	d := newNotebookDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)
	addIdleNotebookSession(d, "chief", protocol.SessionStateWorking)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}
	client := &wsClient{send: make(chan outboundMessage, 4)}

	d.sendNotebookToChiefWSResult(client, "c3", "/index.md", "do not interrupt")

	var res protocol.NotebookSendToChiefResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.Result == nil || res.Result.Nudged {
		t.Fatalf("send-to-chief result = %+v, want success not nudged for a busy chief", res.Result)
	}
	mu.Lock()
	n := len(inputs)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("PTY inputs = %d, want 0 (never interrupt a working chief)", n)
	}
}

// An empty selection is rejected as a daemon error (success=false), not silently
// turned into an empty inbox entry.
func TestNotebookSendToChiefRejectsEmptySelection(t *testing.T) {
	d := newNotebookDaemon(t)
	client := &wsClient{send: make(chan outboundMessage, 4)}

	d.sendNotebookToChiefWSResult(client, "c4", "/index.md", "   ")

	var res protocol.NotebookSendToChiefResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Result != nil || res.Error == nil {
		t.Fatalf("empty selection result = %+v (err=%v), want failure", res.Result, res.Error)
	}
}

// A selection larger than the up-front cap is rejected before any write, so one
// runaway paste cannot bloat the inbox note. The inbox note is not created.
func TestNotebookSendToChiefRejectsOversizeSelection(t *testing.T) {
	d := newNotebookDaemon(t)
	client := &wsClient{send: make(chan outboundMessage, 4)}

	d.sendNotebookToChiefWSResult(client, "c5", "/index.md", strings.Repeat("a", maxInboxSelection+1))

	var res protocol.NotebookSendToChiefResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Result != nil || res.Error == nil {
		t.Fatalf("oversize selection result = %+v (err=%v), want failure", res.Result, res.Error)
	}
	store, err := d.notebookStoreFor()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, rerr := store.Read(notebook.FileInbox); rerr == nil {
		t.Fatal("inbox note must not be created when the selection is rejected")
	}
}

// The inbox entry must survive hostile/odd inputs: a clean source path renders a
// backlink that the link parser actually resolves; a name with markdown-breaking
// characters renders as inline code (never a broken link or injected structure);
// control chars and CRLF are neutralized.
func TestFormatChiefInboxEntry(t *testing.T) {
	// A clean path yields a real, resolvable backlink.
	clean := formatChiefInboxEntry("/knowledge/areas/x.md", "a decision")
	links := notebook.Links(clean)
	if len(links) != 1 || links[0] != "/knowledge/areas/x.md" {
		t.Fatalf("clean-path links = %v, want [/knowledge/areas/x.md]", links)
	}
	if !strings.Contains(clean, "> a decision") {
		t.Fatalf("clean entry missing blockquoted selection:\n%s", clean)
	}

	// A name with spaces/parens renders as inline code — no broken link syntax,
	// and the link parser finds nothing to (mis)resolve.
	special := formatChiefInboxEntry("/knowledge/areas/Q3 (draft).md", "x")
	if strings.Contains(special, "](") {
		t.Fatalf("special-name heading must not emit link syntax:\n%s", special)
	}
	if !strings.Contains(special, "`/knowledge/areas/Q3 (draft).md`") {
		t.Fatalf("special-name heading should show the path as inline code:\n%s", special)
	}
	if len(notebook.Links(special)) != 0 {
		t.Fatalf("special-name entry must yield no parseable link, got %v", notebook.Links(special))
	}

	// A newline in the source path cannot inject a second heading line.
	inject := formatChiefInboxEntry("/knowledge/areas/a.md\n## INJECTED\nb.md", "x")
	if strings.Contains(inject, "\n## ") {
		t.Fatalf("source path must not inject a heading line:\n%s", inject)
	}

	// CRLF in the selection leaves no stray carriage returns.
	crlf := formatChiefInboxEntry("/index.md", "line1\r\nline2")
	if strings.Contains(crlf, "\r") {
		t.Fatalf("CRLF selection left a stray carriage return:\n%q", crlf)
	}
	if !strings.Contains(crlf, "> line1\n> line2") {
		t.Fatalf("CRLF selection not normalized into clean blockquote lines:\n%s", crlf)
	}
}

// notebook_send_to_chief must dispatch through handleClientMessage with its
// request_id, selection, and source_path all extracted (a dropped Deref would
// compile and ship) — the real frontend path.
func TestNotebookSendToChiefDispatchesThroughClientMessage(t *testing.T) {
	d := newNotebookDaemon(t)
	client := newWorkspaceProtocolTestClient()
	client.setIdentity("test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.handleClientMessage(client, []byte(`{"cmd":"notebook_send_to_chief","request_id":"d1","selection":"dispatched selection","source_path":"/knowledge/index.md"}`))
	var res protocol.NotebookSendToChiefResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Event != protocol.EventNotebookSendToChiefResult || res.RequestID != "d1" || !res.Success ||
		res.Result == nil || res.Result.Path != notebook.FileInbox {
		t.Fatalf("dispatch result = %+v", res.Result)
	}
	if body := readInboxNote(t, d); !strings.Contains(body, "> dispatched selection") {
		t.Fatalf("inbox note = %q, want the dispatched selection", body)
	}
}
