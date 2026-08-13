package store

import (
	"path/filepath"
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

// Every path that moves the pointer extends the serving chain, and a no-op move
// leaves it alone. This is what bare `attn app rollback` walks, so the writers
// have to agree.
func TestApps_PointerMovesExtendTheServingChain(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	older, newer := seedApps(t, s, now)

	previousOf := func(step string) int64 {
		t.Helper()
		app, ok, err := s.GetApp("approval-gate")
		if err != nil || !ok {
			t.Fatalf("%s: get app: %v ok=%t", step, err, ok)
		}
		return app.PreviousServingVersionID
	}

	// The first version had nothing before it; the second was pushed onto it.
	if got := previousOf("after two applies"); got != older.ID {
		t.Fatalf("one step back after the second apply = %d, want %d", got, older.ID)
	}

	// Re-applying the version already current moves nothing, so the step below
	// must survive — otherwise an idle dev loop erases the way back.
	if _, _, err := s.CommitAppVersion(AppVersion{
		AppName:      "approval-gate",
		ContentHash:  newer.ContentHash,
		Declaration:  "ignored",
		ArtifactPath: "ignored",
	}, now.Add(time.Hour)); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if got := previousOf("after a no-op re-apply"); got != older.ID {
		t.Fatalf("a no-op re-apply changed the step below to %d, want %d", got, older.ID)
	}

	// Naming a version is a pointer move like an apply: it lands on the older
	// version with the newer one now behind it, so the history reads forward.
	if err := s.SetAppCurrentVersion("approval-gate", older.ID, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := previousOf("after a named rollback"); got != newer.ID {
		t.Fatalf("one step back after the named rollback = %d, want %d", got, newer.ID)
	}

	// An app whose pointer has never moved off its first version has nothing
	// below it, and reports 0 rather than guessing at one.
	other, _, err := s.GetApp("standup-digest")
	if err != nil {
		t.Fatalf("get the single-version app: %v", err)
	}
	if other.PreviousServingVersionID != 0 {
		t.Fatalf("a first apply invented a step below: %d", other.PreviousServingVersionID)
	}
}

// The stack: each bare rollback walks one step further back, applying starts the
// history again from where it lands, and the bottom refuses rather than wrapping.
func TestApps_BareRollbackWalksTheChainDown(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	v := func(n int, at time.Duration) AppVersion {
		t.Helper()
		version, _, err := s.CommitAppVersion(AppVersion{
			AppName:      "approval-gate",
			ContentHash:  "sha256:" + string(rune('a'+n)),
			Declaration:  "{}",
			ArtifactPath: "bundle.js",
		}, now.Add(at))
		if err != nil {
			t.Fatalf("apply v%d: %v", n, err)
		}
		return version
	}
	standing := func(step string) (current, below int64) {
		t.Helper()
		app, ok, err := s.GetApp("approval-gate")
		if err != nil || !ok {
			t.Fatalf("%s: get app: %v ok=%t", step, err, ok)
		}
		return app.CurrentVersionID, app.PreviousServingVersionID
	}
	walk := func(step string, target int64) {
		t.Helper()
		if err := s.StepAppVersionBack("approval-gate", target, now.Add(time.Hour)); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}

	v1, v2, v3 := v(1, 0), v(2, time.Minute), v(3, 2*time.Minute)

	walk("first bare rollback", v2.ID)
	if current, below := standing("after one step"); current != v2.ID || below != v1.ID {
		t.Fatalf("after one step: on %d with %d below, want %d and %d", current, below, v2.ID, v1.ID)
	}
	walk("second bare rollback", v1.ID)
	if current, below := standing("after two steps"); current != v1.ID || below != 0 {
		t.Fatalf("after two steps: on %d with %d below, want %d and the bottom", current, below, v1.ID)
	}
	if err := s.StepAppVersionBack("approval-gate", v1.ID, now.Add(2*time.Hour)); err == nil {
		t.Fatal("walking past the oldest version was accepted")
	}
	if current, _ := standing("after the refused step"); current != v1.ID {
		t.Fatalf("a refused walk moved the pointer to %d", current)
	}

	// Applying while walked back starts the history from where the walk stopped:
	// the way back from the fix is what was actually running when it was applied,
	// not the versions the walk already rejected.
	v4 := v(4, 3*time.Minute)
	if current, below := standing("after applying on top of the walk"); current != v4.ID || below != v1.ID {
		t.Fatalf("after applying v4: on %d with %d below, want %d and %d", current, below, v4.ID, v1.ID)
	}
	walk("rollback off the fix", v1.ID)
	if current, below := standing("back off the fix"); current != v1.ID || below != 0 {
		t.Fatalf("off the fix: on %d with %d below, want %d and the bottom", current, below, v1.ID)
	}
	// v3 is still a version, still reachable by name — only the walk moved past it.
	versions, err := s.ListAppVersions("approval-gate")
	if err != nil || len(versions) != 4 {
		t.Fatalf("the walk changed the version list: %d (%v)", len(versions), err)
	}
	if err := s.SetAppCurrentVersion("approval-gate", v3.ID, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("naming the version the walk passed: %v", err)
	}
	if current, below := standing("after naming v3"); current != v3.ID || below != v1.ID {
		t.Fatalf("after naming v3: on %d with %d below, want %d and %d", current, below, v3.ID, v1.ID)
	}
}

// The history read is what `attn app status` shows: the version serving now
// first, then each version one bare rollback further back, and a total so a
// capped answer can say it was cut.
func TestApps_ServingHistoryReadsTheWalkFromTheTop(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	older, newer := seedApps(t, s, now)

	history, steps, err := s.ListAppServingHistory("approval-gate", 10)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if len(history) != 2 || history[0] != newer.ID || history[1] != older.ID {
		t.Fatalf("history = %v, want [%d %d]", history, newer.ID, older.ID)
	}
	if steps != 2 {
		t.Fatalf("steps = %d, want 2", steps)
	}

	// Walking does not shorten the history; it moves where the app stands on it,
	// so what is left below is what the next rollback can still reach.
	if err := s.StepAppVersionBack("approval-gate", older.ID, now.Add(time.Hour)); err != nil {
		t.Fatalf("walk: %v", err)
	}
	history, steps, err = s.ListAppServingHistory("approval-gate", 10)
	if err != nil {
		t.Fatalf("read history after the walk: %v", err)
	}
	if len(history) != 1 || history[0] != older.ID || steps != 1 {
		t.Fatalf("history after the walk = %v (%d steps), want just %d", history, steps, older.ID)
	}

	// The cap trims what is returned and never the total, so a longer walk is
	// visible rather than silently ending.
	if err := s.SetAppCurrentVersion("approval-gate", newer.ID, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("named rollback: %v", err)
	}
	history, steps, err = s.ListAppServingHistory("approval-gate", 1)
	if err != nil {
		t.Fatalf("read capped history: %v", err)
	}
	if len(history) != 1 || history[0] != newer.ID || steps != 2 {
		t.Fatalf("capped history = %v (%d steps), want one entry of two", history, steps)
	}

	// An app that has never served has no history at all.
	if err := s.SaveApp("half-installed", now); err != nil {
		t.Fatalf("save app: %v", err)
	}
	if history, steps, err := s.ListAppServingHistory("half-installed", 10); err != nil || len(history) != 0 || steps != 0 {
		t.Fatalf("unserved app history = %v (%d steps, %v)", history, steps, err)
	}
}

// A walk refuses when the step below is not the version the caller resolved and
// reported, rather than landing somewhere nobody was told about.
func TestApps_WalkRefusesAStaleTarget(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	older, newer := seedApps(t, s, now)

	if err := s.StepAppVersionBack("approval-gate", newer.ID, now.Add(time.Hour)); err == nil {
		t.Fatal("a walk onto a version that is not the step below was accepted")
	}
	app, _, err := s.GetApp("approval-gate")
	if err != nil {
		t.Fatalf("get app: %v", err)
	}
	if app.CurrentVersionID != newer.ID {
		t.Fatalf("a refused walk moved the pointer to %d", app.CurrentVersionID)
	}
	if err := s.StepAppVersionBack("approval-gate", older.ID, now.Add(time.Hour)); err != nil {
		t.Fatalf("walk onto the step below: %v", err)
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

// A profile that ran the shipped single-pointer rollback carries its recorded
// predecessor into the chain: the first bare rollback after the upgrade lands
// where it would have landed before, and the next one continues down instead of
// bouncing back. An app that never recorded one is carried with nothing below
// it rather than having a predecessor invented from version ids — the bug bare
// rollback exists to avoid.
func TestApps_MigrationCarriesTheRecordedPredecessorIntoTheChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	older, newer := seedApps(t, s, now)
	s.Close()

	// Rewind to the schema as it stood after 103 and before 105, with the app
	// rows carrying exactly what the shipped writer would have left: the newer
	// version serving, the older one recorded behind it, and the single-version
	// app with no predecessor at all.
	db, err := OpenDB(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	for _, stmt := range []string{
		"ALTER TABLE apps ADD COLUMN previous_version_id INTEGER",
		"ALTER TABLE apps DROP COLUMN serving_step_id",
		"DROP TABLE app_serving_steps",
		"DELETE FROM schema_migrations WHERE version >= 105",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("rewinding with %q: %v", stmt, err)
		}
	}
	if _, err := db.Exec(
		"UPDATE apps SET previous_version_id = ? WHERE name = 'approval-gate'", older.ID); err != nil {
		t.Fatalf("seeding the recorded predecessor: %v", err)
	}
	db.Close()

	s, err = NewWithDB(path)
	if err != nil {
		t.Fatalf("reopen after rewind: %v", err)
	}
	defer s.Close()
	app, ok, err := s.GetApp("approval-gate")
	if err != nil || !ok {
		t.Fatalf("get app after migration: %v ok=%t", err, ok)
	}
	if app.CurrentVersionID != newer.ID {
		t.Fatalf("the migration moved the current version to %d, want %d", app.CurrentVersionID, newer.ID)
	}
	if app.PreviousServingVersionID != older.ID {
		t.Fatalf("one step back after the migration = %d, want the recorded %d", app.PreviousServingVersionID, older.ID)
	}
	// The carried chain is two steps, so the walk works once and then reports the
	// bottom — it does not bounce back onto the version it just left.
	if err := s.StepAppVersionBack("approval-gate", older.ID, now.Add(time.Hour)); err != nil {
		t.Fatalf("walking the carried chain: %v", err)
	}
	if app, _, err := s.GetApp("approval-gate"); err != nil || app.PreviousServingVersionID != 0 {
		t.Fatalf("the carried chain has more below the oldest version: %+v (%v)", app, err)
	}
	if err := s.StepAppVersionBack("approval-gate", older.ID, now.Add(2*time.Hour)); err == nil {
		t.Fatal("walking past the bottom of a carried chain was accepted")
	}

	// An app with no recorded predecessor keeps none.
	other, _, err := s.GetApp("standup-digest")
	if err != nil {
		t.Fatalf("get the single-version app: %v", err)
	}
	if other.CurrentVersionID == 0 {
		t.Fatal("the migration lost the current version pointer")
	}
	if other.PreviousServingVersionID != 0 {
		t.Fatalf("the migration invented a predecessor: %d", other.PreviousServingVersionID)
	}
}
