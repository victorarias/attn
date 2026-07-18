package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// fsWatch drives handleFsWatch directly and returns the decoded result event.
func fsWatch(t *testing.T, d *Daemon, client *wsClient, requestID, root string) protocol.FsWatchResultMessage {
	t.Helper()
	d.handleFsWatch(client, requestID, root)
	var res protocol.FsWatchResultMessage
	readNotebookWSEvent(t, client.send, &res)
	return res
}

// fsUnwatch drives handleFsUnwatch directly and returns the decoded result event.
func fsUnwatch(t *testing.T, d *Daemon, client *wsClient, requestID, root string) protocol.FsUnwatchResultMessage {
	t.Helper()
	d.handleFsUnwatch(client, requestID, root)
	var res protocol.FsUnwatchResultMessage
	readNotebookWSEvent(t, client.send, &res)
	return res
}

// assertNoFsChangedForRoot fails the test if an fs_changed broadcast for the
// given (root, origin) pair shows up on ch within the wait window. Any other
// events (including fs_changed for a different root/origin) are drained and
// re-queued so a later assertion in the same test still sees them.
func assertNoFsChangedForRoot(t *testing.T, ch chan outboundMessage, root, origin string, wait time.Duration) {
	t.Helper()
	deadline := time.After(wait)
	var drained []outboundMessage
	for {
		select {
		case msg := <-ch:
			var ev protocol.FsChangedMessage
			if err := json.Unmarshal(msg.payload, &ev); err == nil &&
				ev.Event == protocol.EventFsChanged && ev.Root == root && ev.Origin == origin {
				t.Fatalf("unexpected fs_changed(root=%q origin=%q)", root, origin)
			}
			drained = append(drained, msg)
		case <-deadline:
			for _, m := range drained {
				ch <- m
			}
			return
		}
	}
}

// fs_watch on a generic (non-notebook) root starts a live watcher whose external
// edits surface as fs_changed(origin=external) for ANY file type — catching a
// watcher that failed to start, or one still filtered to .md-only paths. The
// event is delivered to the subscribed client directly, not via the hub
// broadcast: a generic root's fs_changed audience is the watch's own
// subscriber set (see TestFsWatchAudienceRestrictedToSubscribers), not every
// connected client.
func TestFsWatchExternalEditSurfacesForAnyFileType(t *testing.T) {
	d := newFsDaemon(t)
	root := t.TempDir()

	watchClient := trustedFsClient(8)
	res := fsWatch(t, d, watchClient, "w1", root)
	if !res.Success || res.Root == nil || *res.Root != root {
		t.Fatalf("fs_watch result = %+v", res)
	}

	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	ev := waitForFsChangeWithRoot(t, watchClient.send, originExternal)
	if ev.Root != root || !slices.Contains(ev.Paths, "note.txt") {
		t.Fatalf("external fs_changed = %+v, want root=%q paths containing note.txt", ev, root)
	}
}

// The audience restriction that fixes the trust-boundary leak: a generic
// root's fs_changed must reach only clients that hold an fs_watch ref on that
// root. A second connected (and even trusted) client that never subscribed —
// and the hub's broadcast path — must see nothing, even though the notebook
// root's fs_changed still goes to everyone.
func TestFsWatchAudienceRestrictedToSubscribers(t *testing.T) {
	d := newFsDaemon(t)
	root := t.TempDir()
	hubClient := &wsClient{send: make(chan outboundMessage, 64)}
	d.wsHub.clients[hubClient] = true
	go d.wsHub.run()

	watchClient := trustedFsClient(8)
	nonSubscriber := trustedFsClient(8)
	if res := fsWatch(t, d, watchClient, "w1", root); !res.Success {
		t.Fatalf("fs_watch = %+v", res)
	}

	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	ev := waitForFsChangeWithRoot(t, watchClient.send, originExternal)
	if ev.Root != root || !slices.Contains(ev.Paths, "note.txt") {
		t.Fatalf("subscriber fs_changed = %+v, want root=%q paths containing note.txt", ev, root)
	}
	assertNoFsChangedForRoot(t, hubClient.send, root, originExternal, 700*time.Millisecond)
	assertNoFsChangedForRoot(t, nonSubscriber.send, root, originExternal, 700*time.Millisecond)
}

// Once fs_unwatch drops the last ref on a root, its watcher must actually stop:
// a subsequent external write produces no fs_changed for that root. Catches a
// leaked watcher that keeps running after the last subscriber leaves.
func TestFsUnwatchStopsWatcherAtZeroRefs(t *testing.T) {
	d := newFsDaemon(t)
	root := t.TempDir()

	client := trustedFsClient(8)
	if res := fsWatch(t, d, client, "w1", root); !res.Success {
		t.Fatalf("fs_watch = %+v", res)
	}
	if res := fsUnwatch(t, d, client, "u1", root); !res.Success {
		t.Fatalf("fs_unwatch = %+v", res)
	}

	if err := os.WriteFile(filepath.Join(root, "after-unwatch.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertNoFsChangedForRoot(t, client.send, root, originExternal, 700*time.Millisecond)
}

// Two clients watching the same root: dropping one client's refs (as disconnect
// does) must not close the watcher while the other client still holds it; only
// once the last client unwatches does the watcher actually stop. Catches
// disconnect cleanup closing too eagerly, or a refcount leak that never closes.
func TestFsWatchRefcountedAcrossClients(t *testing.T) {
	d := newFsDaemon(t)
	root := t.TempDir()

	clientA := trustedFsClient(8)
	clientB := trustedFsClient(8)
	if res := fsWatch(t, d, clientA, "wa", root); !res.Success {
		t.Fatalf("fs_watch A = %+v", res)
	}
	if res := fsWatch(t, d, clientB, "wb", root); !res.Success {
		t.Fatalf("fs_watch B = %+v", res)
	}

	d.dropFsWatchClient(clientA)

	if err := os.WriteFile(filepath.Join(root, "still-watched.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ev := waitForFsChangeWithRoot(t, clientB.send, originExternal)
	if ev.Root != root || !slices.Contains(ev.Paths, "still-watched.txt") {
		t.Fatalf("fs_changed after dropping one client = %+v", ev)
	}

	if res := fsUnwatch(t, d, clientB, "ub", root); !res.Success {
		t.Fatalf("fs_unwatch B = %+v", res)
	}
	if err := os.WriteFile(filepath.Join(root, "no-longer-watched.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertNoFsChangedForRoot(t, clientB.send, root, originExternal, 700*time.Millisecond)
}

// A WS fs_write to a watched generic root must not echo back as its own
// fs_changed(origin=external) — the self-write suppression must apply to
// non-notebook watchers too, not just the notebook watcher. The origin=ui
// broadcast from the write itself still fires as normal.
func TestFsWatchSelfWriteNotEchoedAsExternal(t *testing.T) {
	d := newFsDaemon(t)
	root := t.TempDir()

	watchClient := trustedFsClient(8)
	if res := fsWatch(t, d, watchClient, "w1", root); !res.Success {
		t.Fatalf("fs_watch = %+v", res)
	}

	if res := fsWriteCASRoot(t, d, "own.txt", "attn wrote this", "", root); !res.Success || res.Result == nil {
		t.Fatalf("write = %+v", res.Result)
	}

	ev := waitForFsChangeWithRoot(t, watchClient.send, originUI)
	if !slices.Contains(ev.Paths, "own.txt") {
		t.Fatalf("ui fs_changed = %+v, want own.txt", ev)
	}
	assertNoFsChangedForRoot(t, watchClient.send, root, originExternal, 700*time.Millisecond)
}

// The notebook root's watcher is now generic: an external edit to a non-.md file
// must surface as fs_changed but must NOT also fire notebook_changed (a .txt
// file is not a note), while an external .md edit must fire BOTH. Catches a
// broken split in the notebook watcher's onChange wiring.
func TestNotebookRootWatcherSplitsFsAndNotebookBroadcasts(t *testing.T) {
	d := newFsDaemon(t)
	root := d.store.GetSetting(SettingNotebookRoot)
	hubClient := &wsClient{send: make(chan outboundMessage, 64)}
	d.wsHub.clients[hubClient] = true
	go d.wsHub.run()

	// Touch the notebook so the always-on watcher starts, then let it settle.
	listFs(t, d, "")
	time.Sleep(80 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(root, "plain.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	ev := waitForFsChangeWithRoot(t, hubClient.send, originExternal)
	if !slices.Contains(ev.Paths, "plain.txt") {
		t.Fatalf("external fs_changed = %+v, want plain.txt", ev)
	}
	assertNoBroadcast(t, hubClient.send, protocol.EventNotebookChanged, 300*time.Millisecond)

	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("---\ntype: note\n---\nhi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fsEv := waitForFsChangeWithRoot(t, hubClient.send, originExternal)
	if !slices.Contains(fsEv.Paths, "note.md") {
		t.Fatalf("external fs_changed = %+v, want note.md", fsEv)
	}
	nbPaths := waitForNotebookChangeEvent(t, hubClient.send)
	if !slices.Contains(nbPaths, "note.md") {
		t.Fatalf("notebook_changed = %v, want note.md", nbPaths)
	}
}

// A 17th distinct watched root is rejected with a clear error, bounding the
// number of live non-notebook watchers so a client cannot grow the daemon's
// goroutine/fd count without limit.
func TestFsWatchCapsLiveWatchers(t *testing.T) {
	d := newFsDaemon(t)
	client := trustedFsClient(32)
	for i := 0; i < maxFsWatchers; i++ {
		root := t.TempDir()
		res := fsWatch(t, d, client, "w", root)
		if !res.Success {
			t.Fatalf("fs_watch #%d = %+v, want success", i, res)
		}
	}
	overflowRoot := t.TempDir()
	res := fsWatch(t, d, client, "overflow", overflowRoot)
	if res.Success || res.Error == nil || *res.Error != "too many watched roots" {
		t.Fatalf("fs_watch(17th root) = %+v, want failure with 'too many watched roots'", res)
	}
}

// fs_watch inherits the fs surface's root-auth gate via resolveFsRoot: an
// ordinary (unauthenticated) client asking to watch an explicit root must be
// denied, and — the behavior that actually matters, not just the reported
// error — no watcher must be registered for it, so a direct external write
// under that root produces no fs_changed at all. Catches handleFsWatch
// bypassing the resolveFsRoot chokepoint (e.g. calling addFsWatchRef before
// checking the resolve error, or checking success but not identity).
func TestFsWatchWithExplicitRootDeniedForUntrustedClient(t *testing.T) {
	d := newFsDaemon(t)
	root := t.TempDir()
	hubClient := &wsClient{send: make(chan outboundMessage, 64)}
	d.wsHub.clients[hubClient] = true
	go d.wsHub.run()

	untrusted := &wsClient{send: make(chan outboundMessage, 8)}
	res := fsWatch(t, d, untrusted, "w1", root)
	if res.Success || res.Error == nil {
		t.Fatalf("fs_watch(explicit root, untrusted client) = %+v, want failure", res)
	}
	if !strings.Contains(*res.Error, "authenticated") {
		t.Fatalf("fs_watch(explicit root, untrusted client) error = %q, want it to mention the authenticated app", *res.Error)
	}

	if err := os.WriteFile(filepath.Join(root, "unwatched.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertNoFsChangedForRoot(t, hubClient.send, root, originExternal, 700*time.Millisecond)
}

// The same untrusted client must still succeed watching with root omitted (the
// notebook root) — the gate is scoped to the explicit-root escape hatch, not
// fs_watch as a whole.
func TestFsWatchOmittedRootStillWorksForUntrustedClient(t *testing.T) {
	d := newFsDaemon(t)
	untrusted := &wsClient{send: make(chan outboundMessage, 8)}
	res := fsWatch(t, d, untrusted, "w1", "")
	if !res.Success {
		t.Fatalf("fs_watch(omitted root, untrusted client) = %+v, want success", res)
	}
}
