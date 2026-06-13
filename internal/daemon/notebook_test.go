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

func TestNotebookBacklinksOverSocket(t *testing.T) {
	d := newNotebookDaemon(t)
	const target = "memory/decisions/target.md"
	sendNotebookCmd(t, d, protocol.NotebookWriteMessage{Cmd: protocol.CmdNotebookWrite, Path: target, Content: "---\nkind: memory\ntitle: Target\n---\nbody\n"})
	sendNotebookCmd(t, d, protocol.NotebookWriteMessage{Cmd: protocol.CmdNotebookWrite, Path: "memory/decisions/linker.md", Content: "---\nkind: memory\n---\nsee [t](/memory/decisions/target.md#why)\n"})
	sendNotebookCmd(t, d, protocol.NotebookWriteMessage{Cmd: protocol.CmdNotebookWrite, Path: "memory/gotchas/other.md", Content: "---\nkind: memory\n---\nno link here\n"})

	resp := sendNotebookCmd(t, d, protocol.NotebookBacklinksMessage{Cmd: protocol.CmdNotebookBacklinks, Path: "/memory/decisions/target.md"})
	if len(resp.NotebookEntries) != 1 || resp.NotebookEntries[0].Path != "memory/decisions/linker.md" {
		t.Fatalf("backlinks = %+v, want [memory/decisions/linker.md]", resp.NotebookEntries)
	}
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
	sendNotebookCmd(t, d, protocol.NotebookWriteMessage{Cmd: protocol.CmdNotebookWrite, Path: "memory/decisions/a.md", Content: "---\nkind: memory\n---\nbody\n"})
	sendNotebookCmd(t, d, protocol.NotebookWriteMessage{Cmd: protocol.CmdNotebookWrite, Path: "memory/decisions/b.md", Content: "---\nkind: memory\n---\nsee [a](/memory/decisions/a.md)\n"})
	client := &wsClient{send: make(chan outboundMessage, 4)}

	d.sendNotebookListWSResult(client, "list-1", "")
	var list protocol.NotebookListResultMessage
	readNotebookWSEvent(t, client.send, &list)
	if list.Event != protocol.EventNotebookListResult || list.RequestID != "list-1" || !list.Success || len(list.Entries) < 2 {
		t.Fatalf("list result = %+v, want >=2 entries for list-1", list)
	}

	d.sendNotebookBacklinksWSResult(client, "back-1", "/memory/decisions/a.md")
	var back protocol.NotebookBacklinksResultMessage
	readNotebookWSEvent(t, client.send, &back)
	if back.Event != protocol.EventNotebookBacklinksResult || back.RequestID != "back-1" || !back.Success ||
		len(back.Entries) != 1 || back.Entries[0].Path != "memory/decisions/b.md" {
		t.Fatalf("backlinks result = %+v, want [memory/decisions/b.md] for back-1", back)
	}
}

// The notebook reads must dispatch correctly through handleClientMessage — the
// path the real frontend uses — not just when the WS-result handlers are called
// directly. This covers the request_id/prefix/path argument extraction in the
// websocket switch (a swapped Deref on notebook_list would compile and ship).
func TestNotebookReadsDispatchThroughClientMessage(t *testing.T) {
	d := newNotebookDaemon(t)
	sendNotebookCmd(t, d, protocol.NotebookWriteMessage{Cmd: protocol.CmdNotebookWrite, Path: "memory/decisions/a.md", Content: "---\nkind: memory\n---\nbody\n"})
	sendNotebookCmd(t, d, protocol.NotebookWriteMessage{Cmd: protocol.CmdNotebookWrite, Path: "memory/decisions/b.md", Content: "---\nkind: memory\n---\nsee [a](/memory/decisions/a.md)\n"})
	sendNotebookCmd(t, d, protocol.NotebookWriteMessage{Cmd: protocol.CmdNotebookWrite, Path: "journal/2026-06-13.md", Content: "---\nkind: journal\n---\nentry\n"})

	client := newWorkspaceProtocolTestClient()
	client.setIdentity("test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	// notebook_list with BOTH request_id and prefix set: a swapped Deref would
	// put the prefix in request_id (and vice versa), so we assert the result is
	// correlated to "rl" AND the prefix actually scoped the result to memory/.
	d.handleClientMessage(client, []byte(`{"cmd":"notebook_list","request_id":"rl","prefix":"/memory"}`))
	var list protocol.NotebookListResultMessage
	readNotebookWSEvent(t, client.send, &list)
	if list.Event != protocol.EventNotebookListResult || list.RequestID != "rl" || !list.Success {
		t.Fatalf("list result = %+v, want success notebook_list_result for rl", list)
	}
	for _, e := range list.Entries {
		if !strings.HasPrefix(e.Path, "memory/") {
			t.Fatalf("prefix not applied: list returned %q outside memory/", e.Path)
		}
	}
	if len(list.Entries) != 2 {
		t.Fatalf("list entries = %d, want 2 under memory/", len(list.Entries))
	}

	d.handleClientMessage(client, []byte(`{"cmd":"notebook_read","request_id":"rr","path":"/memory/decisions/a.md"}`))
	var read protocol.NotebookReadResultMessage
	readNotebookWSEvent(t, client.send, &read)
	if read.Event != protocol.EventNotebookReadResult || read.RequestID != "rr" || !read.Success ||
		read.Result == nil || read.Result.Path != "/memory/decisions/a.md" {
		t.Fatalf("read result = %+v, want success notebook_read_result for rr", read)
	}

	d.handleClientMessage(client, []byte(`{"cmd":"notebook_backlinks","request_id":"rb","path":"/memory/decisions/a.md"}`))
	var back protocol.NotebookBacklinksResultMessage
	readNotebookWSEvent(t, client.send, &back)
	if back.Event != protocol.EventNotebookBacklinksResult || back.RequestID != "rb" || !back.Success ||
		len(back.Entries) != 1 || back.Entries[0].Path != "memory/decisions/b.md" {
		t.Fatalf("backlinks result = %+v, want [memory/decisions/b.md] for rb", back)
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
	sendNotebookCmd(t, d, protocol.NotebookListMessage{Cmd: protocol.CmdNotebookList})
	time.Sleep(80 * time.Millisecond)

	// attn's own write records the path as a self-write before it lands, so the
	// resulting filesystem event must not be reported as external.
	sendNotebookCmd(t, d, protocol.NotebookWriteMessage{
		Cmd: protocol.CmdNotebookWrite, Path: "own.md",
		Content: "---\nkind: memory\n---\nattn wrote this\n",
	})
	// An edit straight to disk (bypassing the daemon) must surface as external.
	if err := os.WriteFile(filepath.Join(root, "ext.md"),
		[]byte("---\nkind: memory\n---\nedited externally\n"), 0o644); err != nil {
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

	sendNotebookCmd(t, d, protocol.NotebookListMessage{Cmd: protocol.CmdNotebookList})
	time.Sleep(80 * time.Millisecond)

	// CAS write against a missing file => conflict, no write, no event.
	resp := sendNotebookCmd(t, d, protocol.NotebookWriteMessage{
		Cmd: protocol.CmdNotebookWrite, Path: "doc.md", Content: "x", BaseHash: protocol.Ptr("deadbeef"),
	})
	if resp.NotebookWrite == nil || !resp.NotebookWrite.Conflict {
		t.Fatalf("expected a conflict, got %+v", resp.NotebookWrite)
	}

	// The external edit of that same path must still surface as external.
	if err := os.WriteFile(filepath.Join(root, "doc.md"),
		[]byte("---\nkind: memory\n---\nexternally created\n"), 0o644); err != nil {
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

	sendNotebookCmd(t, d, protocol.NotebookListMessage{Cmd: protocol.CmdNotebookList})
	time.Sleep(80 * time.Millisecond)

	// Repoint the root and drive an op so ensureNotebookWatcher restarts on B.
	d.store.SetSetting(SettingNotebookRoot, rootB)
	sendNotebookCmd(t, d, protocol.NotebookListMessage{Cmd: protocol.CmdNotebookList})
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

	sendNotebookCmd(t, d, protocol.NotebookListMessage{Cmd: protocol.CmdNotebookList})
	time.Sleep(80 * time.Millisecond)

	// attn writes the note (handler records a content-aware self-write)...
	sendNotebookCmd(t, d, protocol.NotebookWriteMessage{
		Cmd: protocol.CmdNotebookWrite, Path: "race.md",
		Content: "---\nkind: memory\n---\nattn wrote this\n",
	})
	// ...and an external tool immediately overwrites the SAME path with different
	// bytes, within the same debounce window.
	if err := os.WriteFile(filepath.Join(root, "race.md"),
		[]byte("---\nkind: memory\n---\nexternal overwrote it\n"), 0o644); err != nil {
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
	sendNotebookCmd(t, d, protocol.NotebookListMessage{Cmd: protocol.CmdNotebookList})
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

	// The chief request ensured the scaffold exists.
	list := sendNotebookCmd(t, d, protocol.NotebookListMessage{Cmd: protocol.CmdNotebookList})
	if len(list.NotebookEntries) == 0 {
		t.Fatal("chief guide should have ensured the notebook scaffold")
	}

	worker := sendNotebookCmd(t, d, protocol.NotebookGuideMessage{Cmd: protocol.CmdNotebookGuide, SessionID: protocol.Ptr("worker")})
	if worker.NotebookGuide == nil || worker.NotebookGuide.SessionIsChief {
		t.Fatalf("worker guide = %+v, want session_is_chief=false", worker.NotebookGuide)
	}
	if worker.NotebookGuide.Guidance == "" {
		t.Fatal("guidance text should be returned regardless of role")
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
	list := sendNotebookCmd(t, d, protocol.NotebookListMessage{Cmd: protocol.CmdNotebookList})
	if len(list.NotebookEntries) != 0 {
		t.Fatalf("non-chief guide should not scaffold; got %v", list.NotebookEntries)
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

// Live activation types a bounded "run `attn notebook guide`" doorbell + Enter
// into a just-promoted chief session's PTY only when it is idle or waiting for
// input — never into a busy/parked agent (working, pending_approval typing Enter
// would answer a prompt, scheduled would disrupt auto-resume, etc.).
func TestActivateNotebookGuidanceLiveInjectStates(t *testing.T) {
	inject := []protocol.SessionState{protocol.SessionStateIdle, protocol.SessionStateWaitingInput}
	skip := []protocol.SessionState{
		protocol.SessionStateWorking, protocol.SessionStatePendingApproval,
		protocol.SessionStateScheduled, protocol.SessionStateLaunching, protocol.SessionStateUnknown,
	}
	run := func(t *testing.T, state protocol.SessionState, wantInputs int) {
		d := newNotebookDaemon(t)
		var mu sync.Mutex
		var inputs []string
		d.ptyBackend = recordingBackend(&inputs, &mu)
		addIdleNotebookSession(d, "chief", state)
		if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
			t.Fatal(err)
		}

		d.activateNotebookGuidanceLive("chief")

		mu.Lock()
		defer mu.Unlock()
		if len(inputs) != wantInputs {
			t.Fatalf("state %s: PTY inputs = %q, want %d", state, inputs, wantInputs)
		}
		if wantInputs == 2 && (inputs[0] != notebookActivationPrompt || inputs[1] != "\r" ||
			!strings.Contains(inputs[0], "attn notebook guide")) {
			t.Fatalf("state %s: doorbell inputs = %q, want [prompt with `attn notebook guide`, \\r]", state, inputs)
		}
	}
	for _, state := range inject {
		t.Run("inject/"+string(state), func(t *testing.T) { run(t, state, 2) })
	}
	for _, state := range skip {
		t.Run("skip/"+string(state), func(t *testing.T) { run(t, state, 0) })
	}
}

// TOCTOU: a session demoted (chief role transferred away) between the goroutine
// launch and execution must not be told it is now the chief, even while idle.
func TestActivateNotebookGuidanceLiveSkipsDemotedSession(t *testing.T) {
	d := newNotebookDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)
	addIdleNotebookSession(d, "old-chief", protocol.SessionStateIdle)
	addIdleNotebookSession(d, "new-chief", protocol.SessionStateIdle)
	// The role now belongs to new-chief; old-chief's stale goroutine must bail.
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "new-chief"); err != nil {
		t.Fatal(err)
	}

	d.activateNotebookGuidanceLive("old-chief")

	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 0 {
		t.Fatalf("PTY inputs = %q, want none (demoted session must not be told it is chief)", inputs)
	}
}

func TestActivateNotebookGuidanceLiveSkipsMissingSession(t *testing.T) {
	d := newNotebookDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)
	// No session registered for this id.
	d.activateNotebookGuidanceLive("ghost")

	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 0 {
		t.Fatalf("PTY inputs = %q, want none for a missing session", inputs)
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

// A root-absolute (or otherwise un-normalized) write path must still broadcast
// the normalized relative form, matching notebook_list/append.
func TestNotebookWriteBroadcastsNormalizedPath(t *testing.T) {
	d := newNotebookDaemon(t)
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.wsHub.clients[client] = true
	go d.wsHub.run()

	resp := sendNotebookCmd(t, d, protocol.NotebookWriteMessage{
		Cmd: protocol.CmdNotebookWrite, Path: "/memory/decisions/foo.md", Content: "---\nkind: memory\n---\nx\n",
	})
	if resp.NotebookWrite == nil || resp.NotebookWrite.Conflict {
		t.Fatalf("write = %+v", resp.NotebookWrite)
	}
	select {
	case message := <-client.send:
		var event protocol.NotebookChangedMessage
		if err := json.Unmarshal(message.payload, &event); err != nil {
			t.Fatal(err)
		}
		if len(event.Paths) != 1 || event.Paths[0] != "memory/decisions/foo.md" {
			t.Fatalf("broadcast paths = %v, want normalized [memory/decisions/foo.md]", event.Paths)
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
