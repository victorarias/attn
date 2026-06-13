package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
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

// Live activation types a bounded "run `attn notebook guide`" doorbell + Enter
// into a just-promoted chief session's PTY, but only when it is idle/waiting.
func TestActivateNotebookGuidanceLiveInjectsIntoIdleSession(t *testing.T) {
	d := newNotebookDaemon(t)
	var mu sync.Mutex
	var inputs []string
	backend := &fakeSpawnBackend{onInput: func(_ string, data []byte) {
		mu.Lock()
		inputs = append(inputs, string(data))
		mu.Unlock()
	}}
	d.ptyBackend = backend
	addIdleNotebookSession(d, "chief", protocol.SessionStateWaitingInput)

	d.activateNotebookGuidanceLive("chief")

	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 2 || inputs[0] != notebookActivationPrompt || inputs[1] != "\r" {
		t.Fatalf("PTY inputs = %q, want [prompt, \\r]", inputs)
	}
	if !strings.Contains(inputs[0], "attn notebook guide") {
		t.Fatalf("doorbell should instruct running the guide CLI: %q", inputs[0])
	}
}

func TestActivateNotebookGuidanceLiveSkipsWorkingSession(t *testing.T) {
	d := newNotebookDaemon(t)
	var mu sync.Mutex
	var inputs []string
	backend := &fakeSpawnBackend{onInput: func(_ string, data []byte) {
		mu.Lock()
		inputs = append(inputs, string(data))
		mu.Unlock()
	}}
	d.ptyBackend = backend
	addIdleNotebookSession(d, "chief", protocol.SessionStateWorking)

	d.activateNotebookGuidanceLive("chief")

	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 0 {
		t.Fatalf("PTY inputs = %q, want none (must not inject into a working agent)", inputs)
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
