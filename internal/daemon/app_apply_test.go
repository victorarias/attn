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

	// From the oldest version, the default rollback has nowhere to go and says so
	// rather than rolling forward onto a newer one.
	resp = appRollback(t, d, "approval-gate", 0)
	if resp.Ok {
		t.Fatal("rollback from the oldest version reported success")
	}
	msg := protocol.Deref(resp.Error)
	if !strings.Contains(msg, "oldest") || !strings.Contains(msg, fmt.Sprint(third.VersionID)) {
		t.Errorf("error does not explain and list the versions: %s", msg)
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
