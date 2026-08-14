package daemon

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The registry as the UI sees it, and what comes back when one of its mounts
// crashes. What these pin: a tile learns where its bundle is and whether the app
// is on, a version flip re-pushes the whole list (that IS the reload mechanism),
// and a crash report is written to the invocation log only when it names a
// version the app really has.

func appViewManifest(views ...appbuild.View) appbuild.Manifest {
	return appbuild.Manifest{Description: "reviews approvals", Views: views}
}

func tileView(name, title string) appbuild.View {
	return appbuild.View{Name: name, Kind: appbuild.ViewKindTile, Title: title, Entrypoint: "src/views/" + name + ".tsx"}
}

func registryEntry(t *testing.T, entries []protocol.AppRegistryEntry, name string) protocol.AppRegistryEntry {
	t.Helper()
	for _, e := range entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("app %q is not in the registry snapshot (%d entries)", name, len(entries))
	return protocol.AppRegistryEntry{}
}

func TestAppRegistrySnapshotCarriesWhatATileNeedsToMount(t *testing.T) {
	d := newAppDaemon(t)
	view := tileView("approvals", "Pending approvals")
	view.Params = &appbuild.ViewParams{Label: "Ticket id", Placeholder: "t-1234"}
	version := installApp(t, d, "reviewer", appViewManifest(view))

	entry := registryEntry(t, d.appRegistryForWire(), "reviewer")
	if !entry.Enabled {
		t.Fatalf("a freshly installed app is not enabled in the snapshot")
	}
	if protocol.Deref(entry.VersionID) != int(version.ID) {
		t.Fatalf("version id is %v, want %d", entry.VersionID, version.ID)
	}
	if protocol.Deref(entry.ContentHash) != version.ContentHash {
		t.Fatalf("content hash is %v, want %q — a tile builds its bundle URL from it", entry.ContentHash, version.ContentHash)
	}
	if protocol.Deref(entry.Description) != "reviews approvals" {
		t.Fatalf("description is %v", entry.Description)
	}
	if len(entry.Views) != 1 {
		t.Fatalf("views are %+v, want the one declared", entry.Views)
	}
	got := entry.Views[0]
	if got.Name != "approvals" || got.Kind != appbuild.ViewKindTile || got.Title != "Pending approvals" {
		t.Fatalf("view is %+v", got)
	}
	if protocol.Deref(got.ParamsLabel) != "Ticket id" || protocol.Deref(got.ParamsPlaceholder) != "t-1234" {
		t.Fatalf("the params field did not survive to the wire: %+v", got)
	}
}

func TestAppRegistrySnapshotOffersTheViewsTheServingVersionWasBuiltWith(t *testing.T) {
	d := newAppDaemon(t)
	first := installApp(t, d, "roller", appViewManifest(tileView("approvals", "Pending approvals")))
	installApp(t, d, "roller", appViewManifest(tileView("approvals", "Pending approvals"), tileView("history", "History")))

	if names := viewNamesOf(t, d, "roller"); len(names) != 2 {
		t.Fatalf("after the second apply the snapshot offers %v", names)
	}
	// Rolling back is what makes the frozen declaration matter: what docks is
	// what serves, not what the manifest on disk says today.
	if err := d.store.SetAppCurrentVersion("roller", first.ID, first.CreatedAt); err != nil {
		t.Fatalf("roll back to version %d: %v", first.ID, err)
	}
	if names := viewNamesOf(t, d, "roller"); len(names) != 1 || names[0] != "approvals" {
		t.Fatalf("after the rollback the snapshot offers %v, want just the view the old version declared", names)
	}
}

func viewNamesOf(t *testing.T, d *Daemon, app string) []string {
	t.Helper()
	entry := registryEntry(t, d.appRegistryForWire(), app)
	out := make([]string, 0, len(entry.Views))
	for _, v := range entry.Views {
		out = append(out, v.Name)
	}
	return out
}

func TestDisablingAnAppRePushesTheRegistrySoADockedTileCanSaySo(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "reviewer", appViewManifest(tileView("approvals", "Pending approvals")))

	pushes := make(chan []protocol.AppRegistryEntry, 8)
	d.appsBroadcastHook = func(entries []protocol.AppRegistryEntry) { pushes <- entries }

	if resp := appSetEnabled(t, d, "reviewer", false); !resp.Ok {
		t.Fatalf("disable reviewer: %v", protocol.Deref(resp.Error))
	}
	select {
	case entries := <-pushes:
		if registryEntry(t, entries, "reviewer").Enabled {
			t.Fatalf("the pushed snapshot still says reviewer is enabled")
		}
	default:
		t.Fatalf("disabling an app pushed no registry snapshot")
	}
}

func TestAViewCrashIsRecordedAgainstTheVersionThatServedIt(t *testing.T) {
	d := newAppDaemon(t)
	version := installApp(t, d, "reviewer", appViewManifest(tileView("approvals", "Pending approvals")))

	d.handleAppViewCrash(nil, &protocol.AppViewCrashMessage{
		Cmd:       protocol.CmdAppViewCrash,
		App:       "reviewer",
		View:      "approvals",
		VersionID: int(version.ID),
		TileID:    "tile-7",
		Error:     "TypeError: cannot read properties of undefined",
	})

	invocations := appInvocationsOf(t, d, "reviewer")
	if len(invocations) != 1 {
		t.Fatalf("the crash produced %d invocations", len(invocations))
	}
	got := invocations[0]
	if got.VersionID != version.ID {
		t.Fatalf("the crash is stamped with version %d, want %d", got.VersionID, version.ID)
	}
	// The handler name is what makes `attn app logs` say which surface failed.
	if got.Handler != appViewCrashHandler+"approvals" {
		t.Fatalf("handler is %q", got.Handler)
	}
	if got.Status != appInvocationStatusError || !strings.Contains(got.Error, "TypeError") {
		t.Fatalf("the authoring agent cannot see the crash: %+v", got)
	}
	if got.EventSubject != "tile-7" {
		t.Fatalf("event subject is %q, want the tile that crashed", got.EventSubject)
	}
}

func TestAViewCrashLandsInTheAppLogTheTileTellsYouToRead(t *testing.T) {
	d := newAppDaemon(t)
	version := installApp(t, d, "reviewer", appViewManifest(tileView("approvals", "Pending approvals")))

	d.handleAppViewCrash(nil, &protocol.AppViewCrashMessage{
		Cmd:       protocol.CmdAppViewCrash,
		App:       "reviewer",
		View:      "approvals",
		VersionID: int(version.ID),
		TileID:    "tile-7",
		Error:     "TypeError: board is undefined\n    at Approvals (approvals.js:1:199)",
	})

	// Read it the way `attn app logs reviewer` does — through the tag filter, so
	// a line written without the app's tag would be invisible to the author.
	lines, _, err := readAppLog(AppRuntimeLogPath(d.socketPath), "reviewer", false, 20)
	if err != nil {
		t.Fatalf("reading the app log: %v", err)
	}
	whole := strings.Join(lines, "\n")
	for _, want := range []string{
		"view approvals crashed while rendering (version 1)",
		"TypeError: board is undefined",
		"at Approvals (approvals.js:1:199)",
	} {
		if !strings.Contains(whole, want) {
			t.Fatalf("`attn app logs reviewer` does not carry %q:\n%s", want, whole)
		}
	}
}

func TestAViewCrashNamingAnotherAppsVersionIsDropped(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "reviewer", appViewManifest(tileView("approvals", "Pending approvals")))
	other := installApp(t, d, "planner", appViewManifest(tileView("plan", "Plan")))

	d.handleAppViewCrash(nil, &protocol.AppViewCrashMessage{
		Cmd: protocol.CmdAppViewCrash, App: "reviewer", View: "approvals",
		VersionID: int(other.ID), TileID: "tile-7", Error: "boom",
	})

	if got := appInvocationsOf(t, d, "reviewer"); len(got) != 0 {
		t.Fatalf("a report stamped with another app's version was recorded: %+v", got)
	}
}

func TestAViewCrashWithAHugeStackIsTruncatedRatherThanDropped(t *testing.T) {
	d := newAppDaemon(t)
	version := installApp(t, d, "reviewer", appViewManifest(tileView("approvals", "Pending approvals")))

	d.handleAppViewCrash(nil, &protocol.AppViewCrashMessage{
		Cmd: protocol.CmdAppViewCrash, App: "reviewer", View: "approvals",
		VersionID: int(version.ID), TileID: "tile-7",
		Error: "TypeError: boom\n" + strings.Repeat("    at Component (bundle.js:1:1)\n", 4000),
	})

	invocations := appInvocationsOf(t, d, "reviewer")
	if len(invocations) != 1 {
		t.Fatalf("an oversized crash report produced %d invocations", len(invocations))
	}
	got := invocations[0].Error
	if len(got) > appViewCrashErrorLimit+128 {
		t.Fatalf("the recorded error is %d bytes, past the %d-byte limit", len(got), appViewCrashErrorLimit)
	}
	// Half a stack still names the component, so truncation says so rather than
	// leaving the reader with a message that looks complete.
	if !strings.Contains(got, "TypeError: boom") || !strings.Contains(got, "truncated") {
		t.Fatalf("the truncated error is unusable: %q", got[max(0, len(got)-200):])
	}
}

func appInvocationsOf(t *testing.T, d *Daemon, app string) []store.AppInvocation {
	t.Helper()
	rows, err := d.store.ListAppInvocations(app, 20)
	if err != nil {
		t.Fatalf("list invocations of %s: %v", app, err)
	}
	return rows
}
