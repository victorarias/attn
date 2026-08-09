package store

import (
	"testing"
	"time"
)

// seedApps writes the registry a live profile would hold after a few applies:
// two apps, one of them rolled back onto an older version, plus invocations
// stamped inside one second so ordering is actually exercised.
func seedApps(t *testing.T, s *Store, now time.Time) (older, newer AppVersion) {
	t.Helper()

	older, created, err := s.CommitAppVersion(AppVersion{
		AppName:      "approval-gate",
		ContentHash:  "sha256:1111",
		Declaration:  `{"name":"approval-gate","subscribe":[{"events":["delegation.*"]}]}`,
		ArtifactPath: "apps/approval-gate/1111/bundle.js",
	}, now)
	if err != nil {
		t.Fatalf("commit older version: %v", err)
	}
	if !created {
		t.Fatal("commit older version reported reuse, want a new row")
	}
	newer, created, err = s.CommitAppVersion(AppVersion{
		AppName:      "approval-gate",
		ContentHash:  "sha256:2222",
		Declaration:  `{"name":"approval-gate","subscribe":[{"events":["delegation.*","session.state.changed"]}]}`,
		ArtifactPath: "apps/approval-gate/2222/bundle.js",
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("commit newer version: %v", err)
	}
	if !created {
		t.Fatal("commit newer version reported reuse, want a new row")
	}
	if _, _, err := s.CommitAppVersion(AppVersion{
		AppName:      "standup-digest",
		ContentHash:  "sha256:3333",
		Declaration:  `{"name":"standup-digest","subscribe":[{"events":["ticket.*"]}]}`,
		ArtifactPath: "apps/standup-digest/3333/bundle.js",
	}, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("commit other app version: %v", err)
	}
	return older, newer
}

func TestApps_CommitVersionPointsTheAppAtIt(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, newer := seedApps(t, s, now)

	app, ok, err := s.GetApp("approval-gate")
	if err != nil || !ok {
		t.Fatalf("get app: %v ok=%t", err, ok)
	}
	if app.CurrentVersionID != newer.ID {
		t.Fatalf("current version = %d, want %d", app.CurrentVersionID, newer.ID)
	}
	if app.CreatedAt.IsZero() || app.UpdatedAt.Before(app.CreatedAt) {
		t.Fatalf("stamps: created=%v updated=%v", app.CreatedAt, app.UpdatedAt)
	}

	versions, err := s.ListAppVersions("approval-gate")
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 2 || versions[0].ID != newer.ID {
		t.Fatalf("versions = %+v, want newest first", versions)
	}
	if versions[0].Declaration == "" || versions[0].ArtifactPath == "" {
		t.Fatalf("version lost its snapshot: %+v", versions[0])
	}
}

// Byte-identical content is the same version, so a dev loop that rebuilds the
// same output leaves one row and the pointer lands back on it.
func TestApps_IdenticalContentReusesTheVersionRow(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	older, _ := seedApps(t, s, now)

	again, created, err := s.CommitAppVersion(AppVersion{
		AppName:      "approval-gate",
		ContentHash:  older.ContentHash,
		Declaration:  `{"declaration":"rewritten, and ignored"}`,
		ArtifactPath: "apps/approval-gate/1111/rebuilt.js",
	}, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("re-commit: %v", err)
	}
	if created {
		t.Fatal("re-committing identical content reported a new row, want reuse")
	}
	if again.ID != older.ID {
		t.Fatalf("re-apply minted version %d, want the existing %d", again.ID, older.ID)
	}
	// The row is immutable: what came back is what was stored the first time,
	// not the snapshot this call carried.
	if again.Declaration != older.Declaration || again.ArtifactPath != older.ArtifactPath {
		t.Fatalf("re-apply rewrote the version row: %+v", again)
	}
	if n, err := s.CountAppVersions("approval-gate"); err != nil || n != 2 {
		t.Fatalf("versions = %d (%v), want 2", n, err)
	}
	app, _, err := s.GetApp("approval-gate")
	if err != nil {
		t.Fatalf("get app: %v", err)
	}
	if app.CurrentVersionID != older.ID {
		t.Fatalf("current version = %d, want the reused %d", app.CurrentVersionID, older.ID)
	}
}

func TestApps_RollbackMovesThePointerOnly(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	older, newer := seedApps(t, s, now)

	if err := s.SetAppCurrentVersion("approval-gate", older.ID, now.Add(time.Hour)); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	app, _, err := s.GetApp("approval-gate")
	if err != nil {
		t.Fatalf("get app: %v", err)
	}
	if app.CurrentVersionID != older.ID {
		t.Fatalf("current version = %d, want %d", app.CurrentVersionID, older.ID)
	}
	if n, err := s.CountAppVersions("approval-gate"); err != nil || n != 2 {
		t.Fatalf("rollback changed the history: %d versions (%v)", n, err)
	}

	// A version belonging to another app is refused rather than leaving one app
	// pointing into another's history.
	otherVersions, err := s.ListAppVersions("standup-digest")
	if err != nil || len(otherVersions) != 1 {
		t.Fatalf("other app versions: %v %+v", err, otherVersions)
	}
	if err := s.SetAppCurrentVersion("approval-gate", otherVersions[0].ID, now); err == nil {
		t.Fatal("rollback onto another app's version was accepted")
	}
	if err := s.SetAppCurrentVersion("approval-gate", newer.ID+9999, now); err == nil {
		t.Fatal("rollback onto a version that does not exist was accepted")
	}
}

// Removal takes the registry row and nothing else: versions and invocations are
// history, and `attn app remove` says so by counting what it kept.
func TestApps_DeleteKeepsVersionsAndInvocations(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, newer := seedApps(t, s, now)

	if _, err := s.AppendAppInvocation(AppInvocation{
		AppName: "approval-gate", VersionID: newer.ID, EventSeq: 4201,
		EventName: "delegation.requested", EventSubject: "del-7", Handler: "delegation.*",
		Status: "ok", Duration: 12 * time.Millisecond, StartedAt: now,
	}); err != nil {
		t.Fatalf("append invocation: %v", err)
	}

	existed, err := s.DeleteApp("approval-gate")
	if err != nil || !existed {
		t.Fatalf("delete: %v existed=%t", err, existed)
	}
	if _, ok, err := s.GetApp("approval-gate"); err != nil || ok {
		t.Fatalf("app survived removal: ok=%t err=%v", ok, err)
	}
	if n, err := s.CountAppVersions("approval-gate"); err != nil || n != 2 {
		t.Fatalf("versions after removal = %d (%v), want 2", n, err)
	}
	if n, err := s.CountAppInvocations("approval-gate"); err != nil || n != 1 {
		t.Fatalf("invocations after removal = %d (%v), want 1", n, err)
	}
	if existed, err := s.DeleteApp("approval-gate"); err != nil || existed {
		t.Fatalf("second delete reported existed=%t (%v)", existed, err)
	}
}

// Invocations come back newest first even when several land inside one second:
// started_at is stored fixed-width, so text order is time order.
func TestApps_InvocationsListNewestFirstWithinOneSecond(t *testing.T) {
	s := New()
	base := time.Date(2026, 8, 9, 10, 30, 0, 0, time.UTC)
	_, newer := seedApps(t, s, base)

	for i, offset := range []time.Duration{0, 250 * time.Millisecond, 900 * time.Millisecond} {
		status, failure := "ok", ""
		if i == 2 {
			status, failure = "error", "TypeError: cannot read property 'id' of undefined"
		}
		if _, err := s.AppendAppInvocation(AppInvocation{
			AppName: "approval-gate", VersionID: newer.ID, EventSeq: int64(100 + i),
			EventName: "delegation.requested", EventSubject: "del-" + string(rune('a'+i)),
			Handler: "delegation.*", Status: status, Error: failure,
			Duration: time.Duration(i+1) * 7 * time.Millisecond, StartedAt: base.Add(offset),
		}); err != nil {
			t.Fatalf("append invocation %d: %v", i, err)
		}
	}
	// Another app's invocations must not leak into this app's log.
	if _, err := s.AppendAppInvocation(AppInvocation{
		AppName: "standup-digest", VersionID: 3, EventSeq: 999, Status: "ok", StartedAt: base,
	}); err != nil {
		t.Fatalf("append other app invocation: %v", err)
	}

	got, err := s.ListAppInvocations("approval-gate", 10)
	if err != nil {
		t.Fatalf("list invocations: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("invocations = %d, want 3", len(got))
	}
	if got[0].EventSeq != 102 || got[2].EventSeq != 100 {
		t.Fatalf("order = %d,%d,%d, want 102,101,100", got[0].EventSeq, got[1].EventSeq, got[2].EventSeq)
	}
	if got[0].Status != "error" || got[0].Error == "" {
		t.Fatalf("failure detail lost: %+v", got[0])
	}
	if got[0].Duration != 21*time.Millisecond {
		t.Fatalf("duration = %v, want 21ms", got[0].Duration)
	}
	if !got[0].StartedAt.Equal(base.Add(900 * time.Millisecond)) {
		t.Fatalf("started_at = %v, want %v", got[0].StartedAt, base.Add(900*time.Millisecond))
	}
	if limited, err := s.ListAppInvocations("approval-gate", 2); err != nil || len(limited) != 2 {
		t.Fatalf("limit ignored: %d (%v)", len(limited), err)
	}
}

func TestApps_ListAndSaveApp(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, newer := seedApps(t, s, now)

	// A registry row can exist before any version has been applied.
	if err := s.SaveApp("half-installed", now); err != nil {
		t.Fatalf("save app: %v", err)
	}
	apps, err := s.ListApps()
	if err != nil {
		t.Fatalf("list apps: %v", err)
	}
	if len(apps) != 3 || apps[0].Name != "approval-gate" || apps[1].Name != "half-installed" {
		t.Fatalf("apps = %+v, want three by name", apps)
	}
	if apps[1].CurrentVersionID != 0 {
		t.Fatalf("an app with no version reported version %d", apps[1].CurrentVersionID)
	}

	// Saving again touches the row without moving the pointer.
	if err := s.SaveApp("approval-gate", now.Add(time.Hour)); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	app, _, err := s.GetApp("approval-gate")
	if err != nil {
		t.Fatalf("get app: %v", err)
	}
	if app.CurrentVersionID != newer.ID {
		t.Fatalf("save moved the version pointer to %d", app.CurrentVersionID)
	}
	if !app.UpdatedAt.After(app.CreatedAt) {
		t.Fatalf("save did not touch updated_at: created=%v updated=%v", app.CreatedAt, app.UpdatedAt)
	}

	if _, ok, err := s.GetApp("never-installed"); err != nil || ok {
		t.Fatalf("unknown app reported ok=%t (%v)", ok, err)
	}
}

func TestApps_CommitVersionRefusesAnIncompleteRow(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	if _, _, err := s.CommitAppVersion(AppVersion{ContentHash: "sha256:1"}, now); err == nil {
		t.Fatal("a version with no app name was accepted")
	}
	if _, _, err := s.CommitAppVersion(AppVersion{AppName: "x"}, now); err == nil {
		t.Fatal("a version with no content hash was accepted")
	}
	if apps, err := s.ListApps(); err != nil || len(apps) != 0 {
		t.Fatalf("a refused commit left rows behind: %+v (%v)", apps, err)
	}
}
