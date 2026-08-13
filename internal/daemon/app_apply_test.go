package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/protocol"
)

// The daemon's half of an apply: record the version, move the pointer, publish
// the fact — and refuse anything it cannot verify, having changed nothing.
//
// The build itself is the caller's and is covered in internal/appbuild. What is
// pinned here is the seam between them: the daemon derives the artifact's
// location and re-hashes what it finds, so a version row can never name content
// the daemon has not read.

// appApplyDaemon is a daemon with its own artifact store, so one test's
// content-addressed directories cannot be another's.
func appApplyDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newDaemonForTest(t)
	d.appsDir = t.TempDir()
	return d
}

// stageArtifact writes a built bundle where the daemon will look for it, and
// returns the hash that names it.
func stageArtifact(t *testing.T, d *Daemon, name, declaration, bundle string) string {
	t.Helper()
	hash := appbuild.VersionHash(declaration, []byte(bundle))
	path := appbuild.ArtifactPath(d.appsDir, name, hash)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(bundle), 0o644); err != nil {
		t.Fatal(err)
	}
	return hash
}

func declarationFor(name, note string) string {
	return fmt.Sprintf(`{"name":%q,"attn_app_api":1,"entrypoint":"src/index.ts","note":%q}`, name, note)
}

func appApply(t *testing.T, d *Daemon, name, hash, declaration string) protocol.Response {
	t.Helper()
	return docCall(t, func(c net.Conn) {
		d.handleAppApply(c, &protocol.AppApplyMessage{
			Cmd: protocol.CmdAppApply, Name: name, ContentHash: hash, Declaration: declaration,
		})
	})
}

func appRollback(t *testing.T, d *Daemon, name string, versionID int) protocol.Response {
	t.Helper()
	msg := &protocol.AppRollbackMessage{Cmd: protocol.CmdAppRollback, Name: name}
	if versionID > 0 {
		msg.VersionID = protocol.Ptr(versionID)
	}
	return docCall(t, func(c net.Conn) { d.handleAppRollback(c, msg) })
}

// applyOK stages a bundle and applies it, failing the test on refusal.
func applyOK(t *testing.T, d *Daemon, name, declaration, bundle string) *protocol.AppApplyResult {
	t.Helper()
	hash := stageArtifact(t, d, name, declaration, bundle)
	resp := appApply(t, d, name, hash, declaration)
	if !resp.Ok {
		t.Fatalf("app apply %s: %v", name, protocol.Deref(resp.Error))
	}
	return resp.AppApplyResult
}

func versionChangedFacts(t *testing.T, d *Daemon) []appVersionChanged {
	t.Helper()
	var out []appVersionChanged
	for _, e := range appFacts(t, d, FactAppVersionChanged) {
		var payload appVersionChanged
		if len(e.Payload) > 0 {
			if err := json.Unmarshal([]byte(e.Payload), &payload); err != nil {
				t.Fatalf("decoding %s payload: %v", FactAppVersionChanged, err)
			}
		}
		if e.Subject != payload.Name {
			t.Fatalf("fact subject %q does not name the app %q", e.Subject, payload.Name)
		}
		out = append(out, payload)
	}
	return out
}

func TestAppApplyRecordsTheVersionAndPublishesTheFact(t *testing.T) {
	d := appApplyDaemon(t)
	declaration := declarationFor("approval-gate", "first")

	result := applyOK(t, d, "approval-gate", declaration, "export default {}\n")

	if !result.VersionCreated || result.VersionID == 0 {
		t.Fatalf("result = %+v, want a newly created version", result)
	}
	if result.PreviousVersionID != nil {
		t.Errorf("a first apply reported a previous version %d", *result.PreviousVersionID)
	}
	app, ok, err := d.store.GetApp("approval-gate")
	if err != nil || !ok {
		t.Fatalf("get app: %v ok=%t", err, ok)
	}
	if app.CurrentVersionID != int64(result.VersionID) {
		t.Fatalf("pointer = %d, want %d", app.CurrentVersionID, result.VersionID)
	}
	version, ok, err := d.store.GetAppVersion(app.CurrentVersionID)
	if err != nil || !ok {
		t.Fatalf("get version: %v ok=%t", err, ok)
	}
	if version.Declaration != declaration {
		t.Errorf("frozen declaration = %q, want the manifest's snapshot", version.Declaration)
	}
	if version.ArtifactPath != appbuild.ArtifactPath(d.appsDir, "approval-gate", result.ContentHash) {
		t.Errorf("artifact path = %q, want the derived one", version.ArtifactPath)
	}

	facts := versionChangedFacts(t, d)
	if len(facts) != 1 {
		t.Fatalf("facts = %d, want exactly one %s", len(facts), FactAppVersionChanged)
	}
	if facts[0].VersionID != int64(result.VersionID) || facts[0].PreviousID != 0 || facts[0].Reason != appVersionReasonApply {
		t.Fatalf("fact = %+v", facts[0])
	}
}

// Byte-identical content is the same version. The row count is the assertion
// that matters: a dev loop rebuilding identical output must not grow history.
func TestAppApplyByteIdenticalReusesTheVersionRow(t *testing.T) {
	d := appApplyDaemon(t)
	declaration := declarationFor("approval-gate", "first")

	first := applyOK(t, d, "approval-gate", declaration, "export default {}\n")
	second := applyOK(t, d, "approval-gate", declaration, "export default {}\n")

	if second.VersionID != first.VersionID {
		t.Fatalf("re-apply minted version %d, want the existing %d", second.VersionID, first.VersionID)
	}
	if second.VersionCreated {
		t.Error("re-apply reported a new row")
	}
	count, err := d.store.CountAppVersions("approval-gate")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("versions = %d, want 1", count)
	}
	// The pointer did not move, so there is nothing to tell the runtime about:
	// a fact here would have it drain and reload for no reason.
	if facts := versionChangedFacts(t, d); len(facts) != 1 {
		t.Fatalf("facts = %d, want the one from the first apply", len(facts))
	}
}

func TestAppApplyChangedContentMintsAVersionAndFlips(t *testing.T) {
	d := appApplyDaemon(t)

	first := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "first"), "export default {}\n")
	second := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "second"), "export default { changed: true }\n")

	if second.VersionID == first.VersionID || !second.VersionCreated {
		t.Fatalf("second apply = %+v, want a new version", second)
	}
	if second.PreviousVersionID == nil || *second.PreviousVersionID != first.VersionID {
		t.Fatalf("previous = %v, want %d", second.PreviousVersionID, first.VersionID)
	}
	facts := versionChangedFacts(t, d)
	if len(facts) != 2 {
		t.Fatalf("facts = %d, want two", len(facts))
	}
	if facts[1].PreviousID != int64(first.VersionID) || facts[1].VersionID != int64(second.VersionID) {
		t.Fatalf("fact = %+v", facts[1])
	}
}

// A hash that does not describe the bytes on disk is refused, and nothing is
// recorded: a version row that names content the daemon never read would make
// the whole content-addressed scheme decorative.
func TestAppApplyRefusesAnArtifactThatDoesNotMatchItsHash(t *testing.T) {
	d := appApplyDaemon(t)
	declaration := declarationFor("approval-gate", "first")
	hash := stageArtifact(t, d, "approval-gate", declaration, "export default {}\n")

	// Same hash, different bytes underneath it.
	if err := os.WriteFile(appbuild.ArtifactPath(d.appsDir, "approval-gate", hash), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp := appApply(t, d, "approval-gate", hash, declaration)
	if resp.Ok {
		t.Fatal("apply accepted an artifact that does not hash to its name")
	}
	msg := protocol.Deref(resp.Error)
	if !strings.Contains(msg, hash) || !strings.Contains(msg, "nothing was recorded") {
		t.Errorf("error does not name the claimed hash and what it did: %s", msg)
	}
	if count, err := d.store.CountAppVersions("approval-gate"); err != nil || count != 0 {
		t.Fatalf("versions = %d (%v), want none", count, err)
	}
}

func TestAppApplyRefusesAMissingArtifact(t *testing.T) {
	d := appApplyDaemon(t)
	declaration := declarationFor("approval-gate", "first")
	hash := appbuild.VersionHash(declaration, []byte("never written"))

	resp := appApply(t, d, "approval-gate", hash, declaration)
	if resp.Ok {
		t.Fatal("apply accepted a version with no artifact")
	}
	if msg := protocol.Deref(resp.Error); !strings.Contains(msg, d.appsDir) {
		t.Errorf("error does not name where it looked: %s", msg)
	}
}

func TestAppApplyRefusesADeclarationNamingAnotherApp(t *testing.T) {
	d := appApplyDaemon(t)
	declaration := declarationFor("standup-digest", "first")
	hash := stageArtifact(t, d, "approval-gate", declaration, "export default {}\n")

	resp := appApply(t, d, "approval-gate", hash, declaration)
	if resp.Ok {
		t.Fatal("apply accepted a declaration for a different app")
	}
	if msg := protocol.Deref(resp.Error); !strings.Contains(msg, "standup-digest") {
		t.Errorf("error does not name the mismatch: %s", msg)
	}
}

func TestAppApplyRefusesAHashThatIsNotOne(t *testing.T) {
	d := appApplyDaemon(t)
	resp := appApply(t, d, "approval-gate", "not-a-hash", declarationFor("approval-gate", "x"))
	if resp.Ok {
		t.Fatal("apply accepted a malformed content hash")
	}
	if msg := protocol.Deref(resp.Error); !strings.Contains(msg, "hex") {
		t.Errorf("error does not name the rule: %s", msg)
	}
}

func TestAppRollbackMovesToThePreviousVersion(t *testing.T) {
	d := appApplyDaemon(t)
	first := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "first"), "export default {}\n")
	second := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "second"), "export default { two: true }\n")

	resp := appRollback(t, d, "approval-gate", 0)
	if !resp.Ok {
		t.Fatalf("rollback: %v", protocol.Deref(resp.Error))
	}
	result := resp.AppRollbackResult
	if result.VersionID != first.VersionID {
		t.Fatalf("rolled back to %d, want %d", result.VersionID, first.VersionID)
	}
	if result.PreviousVersionID == nil || *result.PreviousVersionID != second.VersionID {
		t.Fatalf("previous = %v, want %d", result.PreviousVersionID, second.VersionID)
	}
	app, _, err := d.store.GetApp("approval-gate")
	if err != nil {
		t.Fatal(err)
	}
	if app.CurrentVersionID != int64(first.VersionID) {
		t.Fatalf("pointer = %d, want %d", app.CurrentVersionID, first.VersionID)
	}
	facts := versionChangedFacts(t, d)
	last := facts[len(facts)-1]
	if last.Reason != appVersionReasonRollback || last.VersionID != int64(first.VersionID) || last.PreviousID != int64(second.VersionID) {
		t.Fatalf("fact = %+v", last)
	}
}

func TestAppRollbackToANamedVersion(t *testing.T) {
	d := appApplyDaemon(t)
	first := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "first"), "export default {}\n")
	applyOK(t, d, "approval-gate", declarationFor("approval-gate", "second"), "export default { two: true }\n")
	third := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "third"), "export default { three: true }\n")

	resp := appRollback(t, d, "approval-gate", first.VersionID)
	if !resp.Ok {
		t.Fatalf("rollback: %v", protocol.Deref(resp.Error))
	}
	if resp.AppRollbackResult.VersionID != first.VersionID {
		t.Fatalf("rolled back to %d, want %d", resp.AppRollbackResult.VersionID, first.VersionID)
	}

	// An explicit rollback is a pointer move like any other, so it records what it
	// replaced: bare rollback now goes back to the version that was serving a
	// moment ago, even though that means moving to a higher id.
	resp = appRollback(t, d, "approval-gate", 0)
	if !resp.Ok {
		t.Fatalf("rollback: %v", protocol.Deref(resp.Error))
	}
	if resp.AppRollbackResult.VersionID != third.VersionID {
		t.Fatalf("bare rollback landed on %d, want the version that was serving, %d",
			resp.AppRollbackResult.VersionID, third.VersionID)
	}
}

// The bug this pointer exists for: an operator who rolled a broken version off
// and then applied a fix has the broken version sitting one id below the
// pointer. Bare rollback must return to what was running, not to what broke.
func TestAppRollbackSkipsAVersionThatWasRolledOff(t *testing.T) {
	d := appApplyDaemon(t)
	good := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "good"), "export default {}\n")
	broken := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "broken"), "export default { broken: true }\n")
	if resp := appRollback(t, d, "approval-gate", good.VersionID); !resp.Ok {
		t.Fatalf("rollback off the broken version: %v", protocol.Deref(resp.Error))
	}
	fixed := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "fixed"), "export default { fixed: true }\n")

	resp := appRollback(t, d, "approval-gate", 0)
	if !resp.Ok {
		t.Fatalf("rollback: %v", protocol.Deref(resp.Error))
	}
	switch got := resp.AppRollbackResult.VersionID; got {
	case good.VersionID:
	case broken.VersionID:
		t.Fatalf("bare rollback landed on the version that was rolled off (%d)", broken.VersionID)
	default:
		t.Fatalf("bare rollback landed on %d, want %d", got, good.VersionID)
	}
	if p := resp.AppRollbackResult.PreviousVersionID; p == nil || *p != fixed.VersionID {
		t.Fatalf("previous = %v, want %d", p, fixed.VersionID)
	}
}

// Each bare rollback walks one step further back, and the oldest version on the
// history is the bottom: rolling back again refuses and lists the versions
// rather than wrapping around onto something the operator already rejected.
func TestAppRollbackWalksOneStepFurtherBackEachTime(t *testing.T) {
	d := appApplyDaemon(t)
	first := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "first"), "export default {}\n")
	second := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "second"), "export default { two: true }\n")
	applyOK(t, d, "approval-gate", declarationFor("approval-gate", "third"), "export default { three: true }\n")

	for i, want := range []int{second.VersionID, first.VersionID} {
		resp := appRollback(t, d, "approval-gate", 0)
		if !resp.Ok {
			t.Fatalf("rollback %d: %v", i+1, protocol.Deref(resp.Error))
		}
		if got := resp.AppRollbackResult.VersionID; got != want {
			t.Fatalf("rollback %d landed on %d, want %d", i+1, got, want)
		}
	}
	resp := appRollback(t, d, "approval-gate", 0)
	if resp.Ok {
		t.Fatalf("a third rollback moved to %d, want the bottom of the history to refuse",
			resp.AppRollbackResult.VersionID)
	}
	msg := protocol.Deref(resp.Error)
	if !strings.Contains(msg, "oldest version in its serving history") {
		t.Errorf("error does not name the situation: %s", msg)
	}
	if !strings.Contains(msg, fmt.Sprint(second.VersionID)) || !strings.Contains(msg, "current") {
		t.Errorf("error does not list the versions: %s", msg)
	}
}

// Applying starts the history again from where the walk stopped, so the way back
// from a fix is what was actually running when it was applied — not the versions
// the walk already went past.
func TestAppApplyRestartsTheRollbackWalk(t *testing.T) {
	d := appApplyDaemon(t)
	first := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "first"), "export default {}\n")
	applyOK(t, d, "approval-gate", declarationFor("approval-gate", "second"), "export default { two: true }\n")
	third := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "third"), "export default { three: true }\n")

	for i := 0; i < 2; i++ {
		if resp := appRollback(t, d, "approval-gate", 0); !resp.Ok {
			t.Fatalf("rollback %d: %v", i+1, protocol.Deref(resp.Error))
		}
	}
	fixed := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "fixed"), "export default { fixed: true }\n")

	resp := appRollback(t, d, "approval-gate", 0)
	if !resp.Ok {
		t.Fatalf("rollback off the fix: %v", protocol.Deref(resp.Error))
	}
	switch got := resp.AppRollbackResult.VersionID; got {
	case first.VersionID:
	case third.VersionID:
		t.Fatalf("rolling back off the fix landed on %d, a version the walk had already gone past", third.VersionID)
	default:
		t.Fatalf("rolling back off the fix landed on %d, want the version it was applied over, %d", got, first.VersionID)
	}
	if p := resp.AppRollbackResult.PreviousVersionID; p == nil || *p != fixed.VersionID {
		t.Fatalf("previous = %v, want %d", p, fixed.VersionID)
	}
	// One step is all the restarted history has: the versions below belong to the
	// walk that was left behind.
	if resp := appRollback(t, d, "approval-gate", 0); resp.Ok {
		t.Fatalf("a second rollback off the fix moved to %d, want the bottom to refuse",
			resp.AppRollbackResult.VersionID)
	}
}

// An app on its first version has nothing below it on its serving history, and
// the numerically previous id is not a substitute. The refusal has to name the
// situation and list the versions, because that is all the caller gets.
func TestAppRollbackAtTheBottomOfTheHistoryRefusesLoudly(t *testing.T) {
	d := appApplyDaemon(t)
	only := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "first"), "export default {}\n")

	resp := appRollback(t, d, "approval-gate", 0)
	if resp.Ok {
		t.Fatal("rollback at the bottom of the serving history reported success")
	}
	msg := protocol.Deref(resp.Error)
	if !strings.Contains(msg, "oldest version in its serving history") {
		t.Errorf("error does not name the situation: %s", msg)
	}
	if !strings.Contains(msg, fmt.Sprint(only.VersionID)) || !strings.Contains(msg, "current") {
		t.Errorf("error does not list the versions: %s", msg)
	}
}

// Re-applying byte-identical content moves nothing, so it must not overwrite the
// predecessor with the current version — that would strand an app on its newest
// version with nowhere to roll back to after an idle dev loop.
func TestAppRollbackSurvivesAReapplyOfTheCurrentVersion(t *testing.T) {
	d := appApplyDaemon(t)
	first := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "first"), "export default {}\n")
	applyOK(t, d, "approval-gate", declarationFor("approval-gate", "second"), "export default { two: true }\n")
	applyOK(t, d, "approval-gate", declarationFor("approval-gate", "second"), "export default { two: true }\n")

	resp := appRollback(t, d, "approval-gate", 0)
	if !resp.Ok {
		t.Fatalf("rollback: %v", protocol.Deref(resp.Error))
	}
	if got := resp.AppRollbackResult.VersionID; got != first.VersionID {
		t.Fatalf("rolled back to %d, want %d", got, first.VersionID)
	}
}

func TestAppRollbackRefusesAVersionThatIsNotTheApps(t *testing.T) {
	d := appApplyDaemon(t)
	mine := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "first"), "export default {}\n")
	theirs := applyOK(t, d, "standup-digest", declarationFor("standup-digest", "first"), "export default {}\n")

	resp := appRollback(t, d, "approval-gate", theirs.VersionID)
	if resp.Ok {
		t.Fatal("rollback accepted another app's version")
	}
	msg := protocol.Deref(resp.Error)
	// Naming what does exist is the point: the reader's next move is picking one.
	if !strings.Contains(msg, fmt.Sprint(mine.VersionID)) || !strings.Contains(msg, "current") {
		t.Errorf("error does not list this app's versions: %s", msg)
	}

	if resp := appRollback(t, d, "approval-gate", 9999); resp.Ok {
		t.Fatal("rollback accepted a version id that does not exist")
	}
}

func TestAppRollbackRefusesTheVersionItIsAlreadyOn(t *testing.T) {
	d := appApplyDaemon(t)
	applyOK(t, d, "approval-gate", declarationFor("approval-gate", "first"), "export default {}\n")
	current := applyOK(t, d, "approval-gate", declarationFor("approval-gate", "second"), "export default { two: true }\n")

	before := len(versionChangedFacts(t, d))
	resp := appRollback(t, d, "approval-gate", current.VersionID)
	if resp.Ok {
		t.Fatal("rollback onto the current version reported success")
	}
	if msg := protocol.Deref(resp.Error); !strings.Contains(msg, "already on version") {
		t.Errorf("error = %s", msg)
	}
	if after := len(versionChangedFacts(t, d)); after != before {
		t.Fatalf("a refused rollback published a fact (%d then %d)", before, after)
	}
}

func TestAppRollbackOnAnUnknownAppNamesWhatExists(t *testing.T) {
	d := appApplyDaemon(t)
	applyOK(t, d, "approval-gate", declarationFor("approval-gate", "first"), "export default {}\n")

	resp := appRollback(t, d, "standup-digest", 0)
	if resp.Ok {
		t.Fatal("rollback accepted an unknown app")
	}
	if msg := protocol.Deref(resp.Error); !strings.Contains(msg, "approval-gate") {
		t.Errorf("error does not name the apps that are registered: %s", msg)
	}
}
