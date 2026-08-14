package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// Applying and rolling back: the two commands that move an app's version
// pointer, and the only writes in the whole apply pipeline.
//
// The build happens in the caller — `attn app apply` runs the developer's bun
// and attn's pinned TypeScript, and hands over a finished artifact. That split
// is deliberate: the toolchain lives on the developer's PATH, not the daemon's
// (a daemon launched by the macOS app inherits a minimal PATH and would not find
// a version-managed bun), and the errors that matter most here — a compiler's
// file and line — belong in the terminal the author is looking at.
//
// What the daemon keeps is everything that has to be atomic or observed: the
// version row, the pointer, and the fact. It also re-derives the hash from what
// is actually on disk, so the registry never names content it has not seen.

// appVersionChanged is FactAppVersionChanged's payload.
//
// Previous is carried rather than left to be read back because the consumer that
// cares — the runtime, draining the outgoing version's handlers — would
// otherwise have to read a pointer that has already moved.
type appVersionChanged struct {
	Name        string `json:"name"`
	VersionID   int64  `json:"version_id"`
	PreviousID  int64  `json:"previous_version_id,omitempty"`
	ContentHash string `json:"content_hash"`
	// Reason is "apply" or "rollback". The move is the same either way; a
	// consumer that logs or notifies wants to say which happened.
	Reason string `json:"reason"`
}

const (
	appVersionReasonApply    = "apply"
	appVersionReasonRollback = "rollback"
)

func (d *Daemon) handleAppApply(conn net.Conn, msg *protocol.AppApplyMessage) {
	name := strings.TrimSpace(msg.Name)
	if err := apps.ValidateName(name); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if d.store == nil {
		d.sendError(conn, "no database")
		return
	}
	hash := strings.TrimSpace(msg.ContentHash)
	if err := validateContentHash(hash); err != nil {
		d.sendError(conn, fmt.Sprintf("app apply %s: %v", name, err))
		return
	}
	declaration := msg.Declaration
	if err := validateDeclaration(name, declaration); err != nil {
		d.sendError(conn, fmt.Sprintf("app apply %s: %v", name, err))
		return
	}

	// The artifact's location is derived from the app and the hash, never taken
	// from the caller: there is exactly one place a version's bundle can be, and
	// deriving it is what makes that true of the daemon as well as the builder.
	path := appbuild.ArtifactPath(d.appsDir, name, hash)
	bundle, err := os.ReadFile(path)
	if err != nil {
		d.sendError(conn, fmt.Sprintf(
			"app apply %s: no built artifact at %s (%v); the build places it there before asking the daemon to record it, so this apply was not built by this attn's data directory (%s)",
			name, path, err, d.appsDir))
		return
	}
	// A version is its handler bundle *and* one module per declared view, so the
	// check reads all of them. The declaration is the only description of the
	// version the daemon holds, which is what names the views to look for.
	viewNames, err := appbuild.DeclaredViewNames(declaration)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("app apply %s: %v", name, err))
		return
	}
	views, err := appbuild.ReadViewArtifacts(d.appsDir, name, hash, viewNames)
	if err != nil {
		d.sendError(conn, fmt.Sprintf(
			"app apply %s: %v; the build places every declared view beside the bundle before asking the daemon to record it, so this apply was not built by this attn's data directory (%s)",
			name, err, d.appsDir))
		return
	}
	if actual := appbuild.VersionHash(declaration, bundle, views); actual != hash {
		d.sendError(conn, fmt.Sprintf(
			"app apply %s: the artifacts at %s hash to %s, not the %s this apply claims; nothing was recorded",
			name, appbuild.ArtifactDir(d.appsDir, name, hash), actual, hash))
		return
	}

	previous, err := d.currentAppVersion(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("app apply %s: %v", name, err))
		return
	}
	version, created, err := d.store.CommitAppVersion(store.AppVersion{
		AppName:      name,
		ContentHash:  hash,
		Declaration:  declaration,
		ArtifactPath: path,
	}, time.Now())
	if err != nil {
		d.sendError(conn, fmt.Sprintf("app apply %s: recording the version: %v", name, err))
		return
	}
	if source := strings.TrimSpace(protocol.Deref(msg.SourcePath)); source != "" {
		d.logf("app apply %s: version %d (%s) from %s", name, version.ID, appbuild.ShortHash(hash), source)
	}
	// Subscriptions and collections both come from the manifest, so both can
	// differ from the version before this one. Re-pointing here rather than off
	// the fact keeps it synchronous with the apply: the reply the author reads
	// means the new version is already the one that will run.
	d.syncAppRuntimeForVersion(name)
	d.publishAppVersionChanged(name, version, previous, appVersionReasonApply)

	result := protocol.AppApplyResult{
		Name:           name,
		VersionID:      int(version.ID),
		ContentHash:    version.ContentHash,
		ArtifactPath:   version.ArtifactPath,
		VersionCreated: created,
	}
	if previous != 0 {
		result.PreviousVersionID = protocol.Ptr(int(previous))
	}
	d.sendDocResponse(conn, protocol.Response{Ok: true, AppApplyResult: &result})
}

// handleAppRollback moves an app onto a version it already has. It builds
// nothing and reads no source: a version is an immutable row and an artifact
// still on disk, which is the whole reason rollback is instant and cannot fail
// halfway.
func (d *Daemon) handleAppRollback(conn net.Conn, msg *protocol.AppRollbackMessage) {
	name := strings.TrimSpace(msg.Name)
	if err := apps.ValidateName(name); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if d.store == nil {
		d.sendError(conn, "no database")
		return
	}
	app, ok, err := d.store.GetApp(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading app %q: %v", name, err))
		return
	}
	if !ok {
		d.sendError(conn, d.unknownAppError("rollback", name))
		return
	}
	versions, err := d.store.ListAppVersions(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading the versions of app %q: %v", name, err))
		return
	}
	target, err := pickRollbackTarget(name, app, versions, msg.VersionID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	// The two rollbacks move the pointer differently on purpose. A named version
	// starts serving history again from where it lands; a bare rollback walks the
	// existing history down one step, which is what lets the next one walk
	// further rather than come back here.
	if msg.VersionID == nil {
		err = d.store.StepAppVersionBack(name, target.ID, time.Now())
	} else {
		err = d.store.SetAppCurrentVersion(name, target.ID, time.Now())
	}
	if err != nil {
		d.sendError(conn, fmt.Sprintf("app rollback %s: %v", name, err))
		return
	}
	d.syncAppRuntimeForVersion(name)
	d.publishAppVersionChanged(name, target, app.CurrentVersionID, appVersionReasonRollback)

	result := protocol.AppRollbackResult{
		Name:         name,
		VersionID:    int(target.ID),
		ContentHash:  target.ContentHash,
		ArtifactPath: target.ArtifactPath,
	}
	if app.CurrentVersionID != 0 {
		result.PreviousVersionID = protocol.Ptr(int(app.CurrentVersionID))
	}
	d.sendDocResponse(conn, protocol.Response{Ok: true, AppRollbackResult: &result})
}

// pickRollbackTarget resolves the version to roll onto, and refuses in the
// reader's terms: a version that is not this app's is refused by listing the
// ones that are, and an app already on the version asked for is refused rather
// than answered with a success that changed nothing and a fact about a move that
// did not happen. Every refusal ends with the app's version list, so the caller
// can fix its next call from the error alone.
//
// versions arrives newest-id-first, as ListAppVersions returns it.
func pickRollbackTarget(name string, app store.App, versions []store.AppVersion, requested *int) (store.AppVersion, error) {
	if len(versions) == 0 {
		return store.AppVersion{}, fmt.Errorf("app rollback %s: it has no versions to roll back to; `attn app apply <path>` builds its first", name)
	}
	if requested != nil {
		id := int64(*requested)
		for _, v := range versions {
			if v.ID == id {
				if id == app.CurrentVersionID {
					return store.AppVersion{}, fmt.Errorf("app rollback %s: it is already on version %d; %s", name, id, versionsSentence(versions, app.CurrentVersionID))
				}
				return v, nil
			}
		}
		return store.AppVersion{}, fmt.Errorf("app rollback %s: version %d is not a version of this app; %s", name, id, versionsSentence(versions, app.CurrentVersionID))
	}
	// No version named: one step back along the app's serving history, which the
	// registry extends at every pointer move. The numerically previous id is a
	// different question and answers it wrong exactly when rollback matters — an
	// app that went good, broken, fixed has the broken one below its pointer, and
	// that is the version an operator is running from.
	//
	// Because walking moves along the chain rather than rewriting one pointer,
	// each further bare rollback goes one step further back, and the step below
	// the one the app stands on is always a version that really did serve before
	// it.
	if app.CurrentVersionID == 0 {
		return store.AppVersion{}, fmt.Errorf("app rollback %s: it is not on any version, so there is no back to go to; name one of them. %s",
			name, versionsSentence(versions, 0))
	}
	if app.PreviousServingVersionID == 0 {
		return store.AppVersion{}, fmt.Errorf("app rollback %s: version %d is the oldest version in its serving history, so there is no further back to go; name the version to roll onto. %s",
			name, app.CurrentVersionID, versionsSentence(versions, app.CurrentVersionID))
	}
	for _, v := range versions {
		if v.ID == app.PreviousServingVersionID {
			return v, nil
		}
	}
	return store.AppVersion{}, fmt.Errorf("app rollback %s: version %d is recorded as serving before version %d but is not among this app's versions; name the version to roll onto. %s",
		name, app.PreviousServingVersionID, app.CurrentVersionID, versionsSentence(versions, app.CurrentVersionID))
}

// versionsSentence lists what an app actually has, marking the current one. It
// is appended to every rollback refusal because the next thing the reader needs
// is the id they should have asked for.
func versionsSentence(versions []store.AppVersion, current int64) string {
	parts := make([]string, 0, len(versions))
	for _, v := range versions {
		label := fmt.Sprintf("%d (%s)", v.ID, appbuild.ShortHash(v.ContentHash))
		if v.ID == current {
			label += " — current"
		}
		parts = append(parts, label)
	}
	return "its versions, newest first: " + strings.Join(parts, ", ")
}

// publishAppVersionChanged announces a pointer that actually moved. A re-apply
// that lands on the version already current moves nothing, and a fact about a
// change that did not happen would have the runtime drain and reload for no
// reason.
func (d *Daemon) publishAppVersionChanged(name string, version store.AppVersion, previous int64, reason string) {
	if version.ID == previous {
		return
	}
	d.publishFact(FactAppVersionChanged, name, appVersionChanged{
		Name:        name,
		VersionID:   version.ID,
		PreviousID:  previous,
		ContentHash: version.ContentHash,
		Reason:      reason,
	})
}

func (d *Daemon) currentAppVersion(name string) (int64, error) {
	app, ok, err := d.store.GetApp(name)
	if err != nil {
		return 0, fmt.Errorf("reading app %q: %w", name, err)
	}
	if !ok {
		return 0, nil
	}
	return app.CurrentVersionID, nil
}

// validateContentHash refuses anything that is not a sha256 in lowercase hex.
// The hash is a directory name and a uniqueness key, and a caller-shaped string
// in either place is worth one cheap check.
func validateContentHash(hash string) error {
	const want = 64
	if len(hash) != want {
		return fmt.Errorf("content hash %q is %d characters; a version hash is %d lowercase hex characters", hash, len(hash), want)
	}
	for _, r := range hash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return fmt.Errorf("content hash %q contains %q; a version hash is %d lowercase hex characters", hash, r, want)
		}
	}
	return nil
}

// validateDeclaration checks the frozen snapshot is JSON for the app being
// applied. The name inside it is what the runtime will read back, so a snapshot
// naming a different app would give a version an identity its row denies.
func validateDeclaration(name, declaration string) error {
	if strings.TrimSpace(declaration) == "" {
		return fmt.Errorf("the declaration snapshot is empty; a version records what its manifest said")
	}
	var probe struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(declaration), &probe); err != nil {
		return fmt.Errorf("the declaration snapshot is not JSON: %w", err)
	}
	if probe.Name != name {
		return fmt.Errorf("the declaration snapshot names app %q, not %q", probe.Name, name)
	}
	return nil
}
