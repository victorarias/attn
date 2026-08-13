package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The app registry's IPC surface. What these pin is mostly what the handlers
// refuse to invent: an app with no consumer has no enabled state, an unknown
// name is answered with what does exist, and removing an app keeps everything
// that is history or data.

// seedApp writes the rows a real profile holds after one apply: a registry row
// pointing at a version, and the app's own bus consumer sitting behind a log
// that has moved on.
func seedApp(t *testing.T, d *Daemon, name string, enabled bool) store.AppVersion {
	t.Helper()
	now := time.Now().UTC()
	version, _, err := d.store.CommitAppVersion(store.AppVersion{
		AppName:      name,
		ContentHash:  "sha256:" + name,
		Declaration:  `{"name":"` + name + `","subscribe":[{"events":["ticket.*"]}]}`,
		ArtifactPath: "apps/" + name + "/bundle.js",
	}, now)
	if err != nil {
		t.Fatalf("seed version for %s: %v", name, err)
	}
	seedAppConsumer(t, d, name, enabled, 0)
	return version
}

func seedAppConsumer(t *testing.T, d *Daemon, name string, enabled bool, cursor int64) {
	t.Helper()
	if err := d.store.SaveBusConsumer(store.BusConsumer{
		Name:    apps.ConsumerName(name),
		Cursor:  cursor,
		Filter:  "ticket.*",
		Enabled: enabled,
	}, time.Now()); err != nil {
		t.Fatalf("seed consumer for %s: %v", name, err)
	}
	// SaveBusConsumer deliberately never rewrites an existing row's cursor or
	// enabled bit — re-registering at startup must neither rewind a consumer nor
	// resurrect one the operator killed — so the seed sets both explicitly.
	if _, err := d.store.SetBusConsumerEnabled(apps.ConsumerName(name), enabled, time.Now()); err != nil {
		t.Fatalf("seed consumer bit for %s: %v", name, err)
	}
	if err := d.store.SetBusConsumerCursor(apps.ConsumerName(name), cursor, time.Now()); err != nil {
		t.Fatalf("seed consumer cursor for %s: %v", name, err)
	}
}

func appFacts(t *testing.T, d *Daemon, name string) []store.BusEvent {
	t.Helper()
	var out []store.BusEvent
	for _, e := range factsOf(t, d) {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

func appList(t *testing.T, d *Daemon) *protocol.AppListResult {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleAppList(c, &protocol.AppListMessage{Cmd: protocol.CmdAppList})
	})
	if !resp.Ok {
		t.Fatalf("app list: %v", protocol.Deref(resp.Error))
	}
	return resp.AppListResult
}

func appStatus(t *testing.T, d *Daemon, name string) protocol.Response {
	t.Helper()
	return docCall(t, func(c net.Conn) {
		d.handleAppStatus(c, &protocol.AppStatusMessage{Cmd: protocol.CmdAppStatus, Name: name})
	})
}

func appSetEnabled(t *testing.T, d *Daemon, name string, enabled bool) protocol.Response {
	t.Helper()
	return docCall(t, func(c net.Conn) {
		d.handleAppSetEnabled(c, &protocol.AppSetEnabledMessage{
			Cmd: protocol.CmdAppSetEnabled, Name: name, Enabled: enabled,
		})
	})
}

func appRemove(t *testing.T, d *Daemon, name string) protocol.Response {
	t.Helper()
	return docCall(t, func(c net.Conn) {
		d.handleAppRemove(c, &protocol.AppRemoveMessage{Cmd: protocol.CmdAppRemove, Name: name})
	})
}

func TestAppListReportsVersionAndConsumer(t *testing.T) {
	d := newDaemonForTest(t)
	if got := appList(t, d); len(got.Apps) != 0 {
		t.Fatalf("a fresh profile listed %d app(s)", len(got.Apps))
	}

	version := seedApp(t, d, "approval-gate", true)
	// Two facts on the log with the consumer parked at 1: lag is what the app
	// has yet to be delivered, which is the number an operator acts on.
	for i := 0; i < 2; i++ {
		if _, err := d.store.AppendBusEvent(store.BusEvent{Name: "ticket.created", Subject: "t-1"}, time.Now()); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}
	seedAppConsumer(t, d, "approval-gate", true, 1)

	got := appList(t, d)
	if len(got.Apps) != 1 {
		t.Fatalf("apps = %d, want 1", len(got.Apps))
	}
	app := got.Apps[0]
	if app.Name != "approval-gate" {
		t.Fatalf("name = %q", app.Name)
	}
	if app.CurrentVersion == nil || app.CurrentVersion.ID != int(version.ID) {
		t.Fatalf("current version = %+v, want %d", app.CurrentVersion, version.ID)
	}
	if app.CurrentVersion.ContentHash != version.ContentHash {
		t.Fatalf("content hash = %q, want %q", app.CurrentVersion.ContentHash, version.ContentHash)
	}
	if app.Consumer == nil {
		t.Fatal("app reported no consumer")
	}
	if !app.Consumer.Enabled || app.Consumer.Name != "app:approval-gate" {
		t.Fatalf("consumer = %+v", app.Consumer)
	}
	if app.Consumer.Cursor != 1 || app.Consumer.Lag != 1 {
		t.Fatalf("cursor/lag = %d/%d, want 1/1", app.Consumer.Cursor, app.Consumer.Lag)
	}
}

// An app whose runtime has never registered a consumer is a real state, and the
// answer says so rather than defaulting it to enabled or to disabled.
func TestAppWithNoConsumerReportsNoneAndCannotBeFlipped(t *testing.T) {
	d := newDaemonForTest(t)
	if err := d.store.SaveApp("half-installed", time.Now()); err != nil {
		t.Fatalf("save app: %v", err)
	}

	listed := appList(t, d)
	if len(listed.Apps) != 1 || listed.Apps[0].Consumer != nil {
		t.Fatalf("apps = %+v, want one with no consumer", listed.Apps)
	}
	if listed.Apps[0].CurrentVersion != nil {
		t.Fatalf("an app with no applied version reported %+v", listed.Apps[0].CurrentVersion)
	}

	for _, enabled := range []bool{true, false} {
		resp := appSetEnabled(t, d, "half-installed", enabled)
		if resp.Ok {
			t.Fatalf("flipping an app with no consumer succeeded (enabled=%t)", enabled)
		}
		msg := protocol.Deref(resp.Error)
		// The error names the consumer that is missing and where to look — an
		// agent reading it can act without reading the code.
		if !strings.Contains(msg, "app:half-installed") || !strings.Contains(msg, "attn app status") {
			t.Fatalf("error does not say what is missing or what to run: %q", msg)
		}
	}
	if facts := appFacts(t, d, FactAppEnabledChanged); len(facts) != 0 {
		t.Fatalf("a refused flip published %d fact(s)", len(facts))
	}
}

func TestAppEnableDisableFlipsTheConsumerBitAndPublishes(t *testing.T) {
	d := newDaemonForTest(t)
	seedApp(t, d, "approval-gate", true)

	resp := appSetEnabled(t, d, "approval-gate", false)
	if !resp.Ok {
		t.Fatalf("disable: %v", protocol.Deref(resp.Error))
	}
	if resp.AppSetEnabledResult.Consumer != "app:approval-gate" || resp.AppSetEnabledResult.Enabled {
		t.Fatalf("disable result = %+v", resp.AppSetEnabledResult)
	}
	consumer, ok, err := d.store.GetBusConsumer("app:approval-gate")
	if err != nil || !ok {
		t.Fatalf("consumer: %v ok=%t", err, ok)
	}
	if consumer.Enabled {
		t.Fatal("disable did not flip the consumer bit")
	}

	if resp := appSetEnabled(t, d, "approval-gate", true); !resp.Ok {
		t.Fatalf("enable: %v", protocol.Deref(resp.Error))
	}
	if consumer, _, _ := d.store.GetBusConsumer("app:approval-gate"); !consumer.Enabled {
		t.Fatal("enable did not flip the consumer bit back")
	}

	facts := appFacts(t, d, FactAppEnabledChanged)
	if len(facts) != 2 {
		t.Fatalf("published %d enabled facts, want 2", len(facts))
	}
	if facts[0].Subject != "approval-gate" {
		t.Fatalf("subject = %q, want the app name", facts[0].Subject)
	}
	// The payload carries which way it went: a consumer of this fact must not
	// have to read back a bit that may already have moved again.
	var payload appEnabledChanged
	if err := json.Unmarshal([]byte(facts[0].Payload), &payload); err != nil {
		t.Fatalf("decoding payload %q: %v", facts[0].Payload, err)
	}
	if payload.Enabled || payload.Consumer != "app:approval-gate" || payload.Name != "approval-gate" {
		t.Fatalf("payload = %+v, want the disable", payload)
	}
}

func TestAppRemoveKeepsHistoryAndDocuments(t *testing.T) {
	d := newDaemonForTest(t)
	version := seedApp(t, d, "approval-gate", true)
	if _, err := d.store.AppendAppInvocation(store.AppInvocation{
		AppName: "approval-gate", VersionID: version.ID, EventSeq: 12,
		EventName: "ticket.created", Status: "ok", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("append invocation: %v", err)
	}

	resp := appRemove(t, d, "approval-gate")
	if !resp.Ok {
		t.Fatalf("remove: %v", protocol.Deref(resp.Error))
	}
	result := resp.AppRemoveResult
	if !result.ConsumerRemoved || result.VersionsKept != 1 || result.InvocationsKept != 1 {
		t.Fatalf("remove result = %+v", result)
	}
	if result.NamespaceKept != "app/approval-gate" {
		t.Fatalf("namespace = %q", result.NamespaceKept)
	}
	if _, ok, err := d.store.GetApp("approval-gate"); err != nil || ok {
		t.Fatalf("registry row survived: ok=%t err=%v", ok, err)
	}
	// The consumer row going is what releases the retention floor; an orphaned
	// enabled row would pin the whole event log against trimming.
	if _, ok, err := d.store.GetBusConsumer("app:approval-gate"); err != nil || ok {
		t.Fatalf("consumer row survived: ok=%t err=%v", ok, err)
	}
	if n, err := d.store.CountAppVersions("approval-gate"); err != nil || n != 1 {
		t.Fatalf("versions after removal = %d (%v)", n, err)
	}
	facts := appFacts(t, d, FactAppRemoved)
	if len(facts) != 1 || facts[0].Subject != "approval-gate" {
		t.Fatalf("removal facts = %+v", facts)
	}
	var payload appRemoved
	if err := json.Unmarshal([]byte(facts[0].Payload), &payload); err != nil {
		t.Fatalf("decoding payload: %v", err)
	}
	if payload.Namespace != "app/approval-gate" || payload.Consumer != "app:approval-gate" {
		t.Fatalf("payload = %+v", payload)
	}
}

// A stray consumer with no registry row is exactly the state that needs a way
// out: it is an enabled registration nobody serves, holding the log's retention
// floor down.
func TestAppRemoveCleansAConsumerWithNoRegistryRow(t *testing.T) {
	d := newDaemonForTest(t)
	seedAppConsumer(t, d, "orphan", true, 0)

	resp := appRemove(t, d, "orphan")
	if !resp.Ok {
		t.Fatalf("remove: %v", protocol.Deref(resp.Error))
	}
	if !resp.AppRemoveResult.ConsumerRemoved {
		t.Fatal("remove reported no consumer removed")
	}
	if _, ok, _ := d.store.GetBusConsumer("app:orphan"); ok {
		t.Fatal("the stray consumer survived")
	}
}

func TestAppCommandsOnAnUnknownNameSayWhatExists(t *testing.T) {
	d := newDaemonForTest(t)
	seedApp(t, d, "approval-gate", true)

	for _, tc := range []struct {
		verb string
		resp protocol.Response
	}{
		{"status", appStatus(t, d, "never-installed")},
		{"enable", appSetEnabled(t, d, "never-installed", true)},
		{"remove", appRemove(t, d, "never-installed")},
	} {
		if tc.resp.Ok {
			t.Fatalf("app %s on an unknown name succeeded", tc.verb)
		}
		msg := protocol.Deref(tc.resp.Error)
		if !strings.Contains(msg, "never-installed") || !strings.Contains(msg, "approval-gate") {
			t.Fatalf("app %s error does not name the ask and what exists: %q", tc.verb, msg)
		}
	}
}

// A name whose registry row is gone but whose history survives is a different
// situation from a name that never existed, and the error says which.
func TestAppStatusOnARemovedAppPointsAtItsRemains(t *testing.T) {
	d := newDaemonForTest(t)
	seedApp(t, d, "approval-gate", true)
	if resp := appRemove(t, d, "approval-gate"); !resp.Ok {
		t.Fatalf("remove: %v", protocol.Deref(resp.Error))
	}

	resp := appStatus(t, d, "approval-gate")
	if resp.Ok {
		t.Fatal("status on a removed app succeeded")
	}
	msg := protocol.Deref(resp.Error)
	if !strings.Contains(msg, "1 version(s)") {
		t.Fatalf("error does not mention the surviving history: %q", msg)
	}
	if !strings.Contains(msg, "no apps are registered") {
		t.Fatalf("error does not say what is registered instead: %q", msg)
	}
}

func TestAppStatusReportsHistoryAndRecentInvocations(t *testing.T) {
	d := newDaemonForTest(t)
	version := seedApp(t, d, "approval-gate", false)
	base := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	for i, status := range []string{"ok", "error"} {
		failure := ""
		if status == "error" {
			failure = "TypeError: undefined is not a function"
		}
		if _, err := d.store.AppendAppInvocation(store.AppInvocation{
			AppName: "approval-gate", VersionID: version.ID, EventSeq: int64(10 + i),
			EventName: "ticket.created", EventSubject: "t-1", Handler: "ticket.*",
			Status: status, Error: failure, Duration: 5 * time.Millisecond,
			StartedAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("append invocation: %v", err)
		}
	}

	resp := appStatus(t, d, "approval-gate")
	if !resp.Ok {
		t.Fatalf("status: %v", protocol.Deref(resp.Error))
	}
	result := resp.AppStatusResult
	if result.Versions != 1 || result.Invocations != 2 {
		t.Fatalf("history = %d version(s), %d invocation(s)", result.Versions, result.Invocations)
	}
	if len(result.Recent) != 2 || result.Recent[0].Status != "error" {
		t.Fatalf("recent = %+v, want the newest first", result.Recent)
	}
	if result.Recent[0].VersionID != int(version.ID) || result.Recent[0].EventSeq != 11 {
		t.Fatalf("recent[0] = %+v", result.Recent[0])
	}
	if result.App.Consumer == nil || result.App.Consumer.Enabled {
		t.Fatalf("consumer = %+v, want a disabled one", result.App.Consumer)
	}
}

func TestAppCommandsRefuseAnInvalidName(t *testing.T) {
	d := newDaemonForTest(t)
	for _, resp := range []protocol.Response{
		appStatus(t, d, "Approval Gate"),
		appSetEnabled(t, d, "Approval Gate", true),
		appRemove(t, d, "Approval Gate"),
	} {
		if resp.Ok {
			t.Fatal("an invalid app name was accepted")
		}
		if !strings.Contains(protocol.Deref(resp.Error), "lowercase") {
			t.Fatalf("error does not say what a name may contain: %q", protocol.Deref(resp.Error))
		}
	}
}
