package daemon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/protocol"
)

// newFsDaemon returns a test daemon whose root (notebook.root, shared by the fs
// surface) points at an isolated temp dir.
func newFsDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	return d
}

// fsWriteCAS performs a hash-CAS fs_write over the WS path and returns the decoded
// result event (a successful result may carry conflict=true). Uses a throwaway
// client; the origin=ui broadcast goes to the hub, not this client.
func fsWriteCAS(t *testing.T, d *Daemon, path, content, baseHash string) protocol.FsWriteResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 8)}
	d.sendFsWriteWSResult(client, "setup-fs-write", path, content, baseHash)
	var res protocol.FsWriteResultMessage
	readNotebookWSEvent(t, client.send, &res)
	return res
}

// listFs lists one directory over the WS fs path and returns the entries.
func listFs(t *testing.T, d *Daemon, dir string) []protocol.FsEntry {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 8)}
	d.sendFsListWSResult(client, "setup-fs-list", dir)
	var res protocol.FsListResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success {
		t.Fatalf("fs list(%q) failed: %v", dir, res.Error)
	}
	return res.Entries
}

// fsExists checks one path over the WS fs path and returns the decoded result event.
func fsExists(t *testing.T, d *Daemon, path string) protocol.FsExistsResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 8)}
	d.sendFsExistsWSResult(client, "setup-fs-exists", path)
	var res protocol.FsExistsResultMessage
	readNotebookWSEvent(t, client.send, &res)
	return res
}

// waitForFsChange returns the paths of the first fs_changed broadcast matching the
// given origin, ignoring other events (notebook_changed, other origins).
func waitForFsChange(t *testing.T, ch chan outboundMessage, origin string) []string {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case msg := <-ch:
			var ev protocol.FsChangedMessage
			if err := json.Unmarshal(msg.payload, &ev); err != nil {
				continue
			}
			if ev.Event == protocol.EventFsChanged && ev.Origin == origin {
				return ev.Paths
			}
		case <-deadline:
			t.Fatalf("no fs_changed with origin %q was broadcast", origin)
			return nil
		}
	}
}

// The fs surface writes, lists, and reads arbitrary files (not just .md) over the
// WS path, round-tripping content and the hash used for hash-CAS edits.
func TestFsWriteListReadWSResults(t *testing.T) {
	d := newFsDaemon(t)

	create := fsWriteCAS(t, d, "notes/todo.txt", "buy milk", "")
	if create.Event != protocol.EventFsWriteResult || !create.Success ||
		create.Result == nil || create.Result.Conflict || create.Result.Hash == nil {
		t.Fatalf("create result = %+v", create.Result)
	}
	hash := *create.Result.Hash

	// List the root: the notes/ directory shows up (is_dir), not the file inside it
	// (shallow).
	rootEntries := listFs(t, d, "")
	if len(rootEntries) != 1 || rootEntries[0].Name != "notes" || !rootEntries[0].IsDir {
		t.Fatalf("root list = %+v, want a single notes/ directory", rootEntries)
	}

	// List notes/: the file shows up with its byte size and a non-empty mtime.
	sub := listFs(t, d, "notes")
	if len(sub) != 1 || sub[0].Path != "notes/todo.txt" || sub[0].IsDir ||
		sub[0].Size != int(len("buy milk")) || sub[0].Modified == nil {
		t.Fatalf("notes list = %+v", sub)
	}

	// Read the file: content + the same hash the write returned.
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendFsReadWSResult(client, "r1", "notes/todo.txt")
	var read protocol.FsReadResultMessage
	readNotebookWSEvent(t, client.send, &read)
	if read.Event != protocol.EventFsReadResult || read.RequestID != "r1" || !read.Success ||
		read.Result == nil || read.Result.Content != "buy milk" || read.Result.Hash != hash {
		t.Fatalf("read result = %+v", read.Result)
	}

	// A missing file is a failed result, not a panic or empty success.
	d.sendFsReadWSResult(client, "r2", "nope.txt")
	var missing protocol.FsReadResultMessage
	readNotebookWSEvent(t, client.send, &missing)
	if missing.RequestID != "r2" || missing.Success || missing.Error == nil {
		t.Fatalf("missing read result = %+v, want failure with error", missing)
	}
}

func TestFsReadWSRejectsOversizedFile(t *testing.T) {
	d := newFsDaemon(t)
	root, err := d.notebookRoot()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "attachments", "too-large.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), fsdoc.MaxFileSize+1), 0o644); err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendFsReadWSResult(client, "oversized", "attachments/too-large.txt")
	var read protocol.FsReadResultMessage
	readNotebookWSEvent(t, client.send, &read)
	if read.Success || read.Error == nil || !strings.Contains(*read.Error, "read cap") {
		t.Fatalf("oversized read = %+v, want read-cap error", read)
	}
}

// fs_exists answers presence without reading: a present file is exists=true, a
// genuinely absent path is a successful exists=false (the broken-link signal), and a
// path the rules reject (a dotfile) is a failed result the UI leaves unflagged.
func TestFsExistsWSResults(t *testing.T) {
	d := newFsDaemon(t)
	if c := fsWriteCAS(t, d, "knowledge/areas/foo.md", "x", ""); !c.Success || c.Result == nil {
		t.Fatalf("seed write = %+v", c.Result)
	}

	present := fsExists(t, d, "/knowledge/areas/foo.md")
	if present.Event != protocol.EventFsExistsResult || present.RequestID != "setup-fs-exists" ||
		!present.Success || present.Result == nil || !present.Result.Exists {
		t.Fatalf("exists(present) = %+v", present)
	}

	absent := fsExists(t, d, "/knowledge/areas/missing.md")
	if !absent.Success || absent.Result == nil || absent.Result.Exists {
		t.Fatalf("exists(absent) = %+v, want a successful exists=false", absent)
	}

	bad := fsExists(t, d, ".secret")
	if bad.Success || bad.Error == nil {
		t.Fatalf("exists(dotfile) = %+v, want a failed result with error", bad)
	}
}

// fs_write is hash-CAS: a stale base hash comes back as a successful result
// carrying conflict=true (for the UI to reconcile), and a matching base applies.
func TestFsWriteWSResultSaveAndConflict(t *testing.T) {
	d := newFsDaemon(t)

	create := fsWriteCAS(t, d, "a.txt", "v1", "")
	if create.Result == nil || create.Result.Hash == nil {
		t.Fatalf("create = %+v", create.Result)
	}
	h1 := *create.Result.Hash

	stale := fsWriteCAS(t, d, "a.txt", "v2", "deadbeef")
	if !stale.Success || stale.Result == nil || !stale.Result.Conflict ||
		stale.Result.CurrentHash == nil || *stale.Result.CurrentHash != h1 {
		t.Fatalf("stale write = %+v, want conflict with current hash %q", stale.Result, h1)
	}

	ok := fsWriteCAS(t, d, "a.txt", "v2", h1)
	if !ok.Success || ok.Result == nil || ok.Result.Conflict || ok.Result.Hash == nil {
		t.Fatalf("CAS edit = %+v", ok.Result)
	}
}

// The fs reads/writes must dispatch correctly through handleClientMessage — the
// real frontend path — not only when the WS-result handlers are called directly.
// This covers the request_id/path/content/base_hash extraction in the websocket
// switch (a swapped Deref would compile and ship).
func TestFsDispatchThroughClientMessage(t *testing.T) {
	d := newFsDaemon(t)
	client := newWorkspaceProtocolTestClient()
	client.setIdentity("test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.handleClientMessage(client, []byte(`{"cmd":"fs_write","request_id":"w1","path":"docs/readme.md","content":"# hi\n"}`))
	var write protocol.FsWriteResultMessage
	readNotebookWSEvent(t, client.send, &write)
	if write.Event != protocol.EventFsWriteResult || write.RequestID != "w1" || !write.Success ||
		write.Result == nil || write.Result.Conflict || write.Result.Hash == nil {
		t.Fatalf("write dispatch = %+v", write)
	}

	// fs_list with request_id AND path set: a swapped Deref would put the path in
	// request_id (and vice versa), so assert both correlation and scoping.
	d.handleClientMessage(client, []byte(`{"cmd":"fs_list","request_id":"l1","path":"docs"}`))
	var list protocol.FsListResultMessage
	readNotebookWSEvent(t, client.send, &list)
	if list.RequestID != "l1" || !list.Success || len(list.Entries) != 1 ||
		list.Entries[0].Path != "docs/readme.md" {
		t.Fatalf("list dispatch = %+v", list)
	}

	d.handleClientMessage(client, []byte(`{"cmd":"fs_read","request_id":"r1","path":"docs/readme.md"}`))
	var read protocol.FsReadResultMessage
	readNotebookWSEvent(t, client.send, &read)
	if read.RequestID != "r1" || !read.Success || read.Result == nil || read.Result.Content != "# hi\n" {
		t.Fatalf("read dispatch = %+v", read.Result)
	}

	// fs_exists with request_id AND path set: assert both correlation and that the
	// just-written note resolves as present (a swapped Deref would misroute either).
	d.handleClientMessage(client, []byte(`{"cmd":"fs_exists","request_id":"e1","path":"docs/readme.md"}`))
	var exists protocol.FsExistsResultMessage
	readNotebookWSEvent(t, client.send, &exists)
	if exists.RequestID != "e1" || !exists.Success || exists.Result == nil ||
		exists.Result.Path != "docs/readme.md" || !exists.Result.Exists {
		t.Fatalf("exists dispatch = %+v", exists)
	}

	d.handleClientMessage(client, []byte(`{"cmd":"fs_rename","request_id":"rn1","path":"docs/readme.md","new_path":"docs/plan.md"}`))
	var renamed protocol.FsRenameResultMessage
	readNotebookWSEvent(t, client.send, &renamed)
	if renamed.RequestID != "rn1" || !renamed.Success || renamed.Result == nil || renamed.Result.NewPath != "docs/plan.md" {
		t.Fatalf("rename dispatch = %+v", renamed)
	}

	d.handleClientMessage(client, []byte(`{"cmd":"fs_delete","request_id":"d1","path":"docs/plan.md"}`))
	var deleted protocol.FsDeleteResultMessage
	readNotebookWSEvent(t, client.send, &deleted)
	if deleted.RequestID != "d1" || !deleted.Success || deleted.Result == nil || deleted.Result.Path != "docs/plan.md" {
		t.Fatalf("delete dispatch = %+v", deleted)
	}
}

// fs_write broadcasts fs_changed(origin=ui) with the written path, so an open fs
// view refreshes after an in-app save. (A non-.md path is used so the watcher does
// not also fire — this isolates the direct ui broadcast.)
func TestFsWriteBroadcastsUiChange(t *testing.T) {
	d := newFsDaemon(t)
	hubClient := &wsClient{send: make(chan outboundMessage, 64)}
	d.wsHub.clients[hubClient] = true
	go d.wsHub.run()

	writer := &wsClient{send: make(chan outboundMessage, 8)}
	d.sendFsWriteWSResult(writer, "w1", "notes/todo.txt", "hello", "")
	var res protocol.FsWriteResultMessage
	readNotebookWSEvent(t, writer.send, &res)
	if !res.Success || res.Result == nil || res.Result.Conflict {
		t.Fatalf("write = %+v", res.Result)
	}

	got := waitForFsChange(t, hubClient.send, originUI)
	if !slices.Contains(got, "notes/todo.txt") {
		t.Fatalf("ui fs_changed = %v, want notes/todo.txt", got)
	}
}

// A write whose path arrives root-absolute (leading slash) echoes and broadcasts
// the normalized root-relative path — the form fs_list returns — so the UI never
// has to reconcile two spellings of the same file.
func TestFsWriteNormalizesEchoedPath(t *testing.T) {
	d := newFsDaemon(t)
	hubClient := &wsClient{send: make(chan outboundMessage, 64)}
	d.wsHub.clients[hubClient] = true
	go d.wsHub.run()

	res := fsWriteCAS(t, d, "/notes/todo.md", "x", "")
	if res.Result == nil || res.Result.Path != "notes/todo.md" {
		t.Fatalf("write result path = %+v, want normalized notes/todo.md", res.Result)
	}
	got := waitForFsChange(t, hubClient.send, originUI)
	if !slices.Contains(got, "notes/todo.md") || slices.Contains(got, "/notes/todo.md") {
		t.Fatalf("ui fs_changed = %v, want normalized notes/todo.md", got)
	}
}

// An external .md edit on disk surfaces as fs_changed(origin=external), while the
// fs surface's own write is suppressed (self-write) and does not echo as external.
// (The shared watcher only surfaces .md today, so this uses .md files.)
func TestFsChangedExternalEditNotSelfWrite(t *testing.T) {
	d := newFsDaemon(t)
	root := d.store.GetSetting(SettingNotebookRoot)
	client := &wsClient{send: make(chan outboundMessage, 64)}
	d.wsHub.clients[client] = true
	go d.wsHub.run()

	// Touch the fs surface so the lazy shared watcher starts, then let it settle.
	listFs(t, d, "")
	time.Sleep(80 * time.Millisecond)

	// An fs_write records a self-write before it lands, so the watcher must not
	// report it as external.
	if res := fsWriteCAS(t, d, "own.md", "attn wrote this", ""); !res.Success || res.Result == nil || res.Result.Conflict {
		t.Fatalf("own write = %+v", res.Result)
	}
	// An edit straight to disk (bypassing the daemon) must surface as external.
	if err := os.WriteFile(filepath.Join(root, "ext.md"), []byte("edited externally"), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := waitForFsChange(t, client.send, originExternal)
	if !slices.Contains(ext, "ext.md") {
		t.Fatalf("external fs_changed %v missing ext.md", ext)
	}
	if slices.Contains(ext, "own.md") {
		t.Fatalf("external fs_changed %v wrongly included attn's own write own.md", ext)
	}
}

// pngHeaderBytes is a minimal valid PNG signature + IHDR-ish prefix. It does not
// need to decode as a real image: fs_read_asset only serves bytes, it never
// decodes them.
var pngHeaderBytes = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}

// fsReadAsset reads one asset over the WS fs path and returns the decoded result
// event.
func fsReadAsset(t *testing.T, d *Daemon, requestID, path string) protocol.FsReadAssetResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendFsReadAssetWSResult(client, requestID, path)
	var res protocol.FsReadAssetResultMessage
	readNotebookWSEvent(t, client.send, &res)
	return res
}

// fs_read_asset serves an allowlisted image's bytes as base64 with its mime type,
// round-tripping the exact bytes written to disk.
func TestFsReadAssetWSResult(t *testing.T) {
	d := newFsDaemon(t)
	if res := fsWriteCAS(t, d, "assets/pic.png", string(pngHeaderBytes), ""); !res.Success || res.Result == nil {
		t.Fatalf("seed write = %+v", res.Result)
	}

	got := fsReadAsset(t, d, "a1", "assets/pic.png")
	if got.Event != protocol.EventFsReadAssetResult || got.RequestID != "a1" || !got.Success || got.Result == nil {
		t.Fatalf("read asset = %+v", got)
	}
	if got.Result.MimeType != "image/png" {
		t.Fatalf("mime type = %q, want image/png", got.Result.MimeType)
	}
	decoded, err := base64.StdEncoding.DecodeString(got.Result.DataBase64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if !bytes.Equal(decoded, pngHeaderBytes) {
		t.Fatalf("decoded bytes = %v, want %v", decoded, pngHeaderBytes)
	}
}

// A path that escapes the notebook root is rejected by the same containment guard
// fs_read uses (fsStoreFor/store.Read), not by any asset-specific path logic.
func TestFsReadAssetPathEscape(t *testing.T) {
	d := newFsDaemon(t)
	got := fsReadAsset(t, d, "a1", "../outside.png")
	if got.Success {
		t.Fatalf("read asset(path escape) = %+v, want failure", got)
	}
}

// A missing asset is a failed result with an error, not a panic or empty success.
func TestFsReadAssetMissingFile(t *testing.T) {
	d := newFsDaemon(t)
	got := fsReadAsset(t, d, "a1", "assets/nope.png")
	if got.Success || got.Error == nil {
		t.Fatalf("read asset(missing) = %+v, want failure with error", got)
	}
}

// A file over the per-asset byte cap is rejected with an error mentioning the cap,
// even though it has a supported extension. Written directly to disk (bypassing
// fs_write, which has its own smaller 2MiB cap) so the asset cap is what's tested.
func TestFsReadAssetOversize(t *testing.T) {
	d := newFsDaemon(t)
	root := d.store.GetSetting(SettingNotebookRoot)
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte{0xFF}, maxAssetBytes+1)
	if err := os.WriteFile(filepath.Join(root, "assets", "huge.png"), big, 0o644); err != nil {
		t.Fatal(err)
	}

	got := fsReadAsset(t, d, "a1", "assets/huge.png")
	if got.Success || got.Error == nil || !strings.Contains(*got.Error, "cap") {
		t.Fatalf("read asset(oversize) = %+v, want failure mentioning the cap", got)
	}
}

// A file at exactly the per-asset byte cap is accepted, and the resulting
// fs_read_asset_result message still fits maxAssetMessageBytes once marshaled.
// This is the boundary the cap derivation exists to guarantee: it fails if
// someone raises maxAssetBytes without re-deriving it, or if the base64
// envelope math is wrong.
func TestFsReadAssetMaxSizeFitsMessageCap(t *testing.T) {
	d := newFsDaemon(t)
	root := d.store.GetSetting(SettingNotebookRoot)
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	max := bytes.Repeat([]byte{0xFF}, maxAssetBytes)
	if err := os.WriteFile(filepath.Join(root, "assets", "max.png"), max, 0o644); err != nil {
		t.Fatal(err)
	}

	got := fsReadAsset(t, d, "a1", "assets/max.png")
	if !got.Success || got.Result == nil {
		t.Fatalf("read asset(max size) = %+v, want success", got)
	}

	marshaled, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if len(marshaled) > maxAssetMessageBytes {
		t.Fatalf("marshaled message = %d bytes, want <= %d (maxAssetMessageBytes)", len(marshaled), maxAssetMessageBytes)
	}
}

// assetMessageFits is the exact per-request wire-size check that guards against
// an arbitrarily long path pushing a max-size asset's message over the cap. It
// is a pure function, tested directly here rather than through a real long path
// on disk: macOS's PATH_MAX (1024) makes a real >4 KiB path uncreatable, but the
// check itself must still hold for paths far longer than that.
func TestAssetMessageFitsRejectsLongPath(t *testing.T) {
	longPath := strings.Repeat("d/", 4096) + "x.png"
	if fits, err := assetMessageFits("a1", longPath, "image/png", maxAssetBytes); err != nil {
		t.Fatalf("assetMessageFits(long path): %v", err)
	} else if fits {
		t.Fatalf("assetMessageFits(long path) = true, want false")
	}

	if fits, err := assetMessageFits("a1", "assets/pic.png", "image/png", maxAssetBytes); err != nil {
		t.Fatalf("assetMessageFits(short path): %v", err)
	} else if !fits {
		t.Fatalf("assetMessageFits(short path) = false, want true")
	}
}

// An extension outside the image allowlist is rejected before the file is even
// read, regardless of the file's actual content.
func TestFsReadAssetUnsupportedExtension(t *testing.T) {
	d := newFsDaemon(t)
	if res := fsWriteCAS(t, d, "assets/doc.pdf", "not an image", ""); !res.Success || res.Result == nil {
		t.Fatalf("seed write = %+v", res.Result)
	}

	got := fsReadAsset(t, d, "a1", "assets/doc.pdf")
	if got.Success || got.Error == nil || *got.Error != "not a supported image asset" {
		t.Fatalf("read asset(unsupported ext) = %+v, want \"not a supported image asset\"", got)
	}
}
