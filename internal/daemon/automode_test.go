package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// Auto mode's two transports and the line between them. What these pin is the
// asymmetry: everything the CLI can reach only records an intention, and the
// verbs that change what a session runs under answer the app alone.

func automodeShow(t *testing.T, d *Daemon) *protocol.AutoModeShowResult {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleAutoModeShow(c, &protocol.AutoModeShowMessage{Cmd: protocol.CmdAutoModeShow})
	})
	if !resp.Ok {
		t.Fatalf("automode show: %v", protocol.Deref(resp.Error))
	}
	return resp.AutomodeShowResult
}

func automodePropose(t *testing.T, d *Daemon, kind, target, value string) protocol.Response {
	t.Helper()
	msg := &protocol.AutoModeProposeMessage{Cmd: protocol.CmdAutoModePropose, Kind: kind, Value: value}
	if target != "" {
		msg.Target = protocol.Ptr(target)
	}
	return docCall(t, func(c net.Conn) { d.handleAutoModePropose(c, msg) })
}

func TestAutoModeShowAnswersDefaultsOnAFreshProfile(t *testing.T) {
	d := newDaemonForTest(t)
	result := automodeShow(t, d)
	if result.Config.ClassifierModel != automode.DefaultClassifierModel {
		t.Errorf("classifier model = %q", result.Config.ClassifierModel)
	}
	// The lists must marshal as [] rather than null: a client rendering a
	// settings section should not have to tell "no entries" from "no answer".
	if result.Config.Allow == nil || result.Config.Environment == nil || result.Config.HardDeny == nil {
		t.Fatalf("a config list came back nil: %+v", result.Config)
	}
	if len(result.Proposals) != 0 {
		t.Fatalf("a fresh profile has %d proposals", len(result.Proposals))
	}
}

// The whole security design in one test: the CLI's allow verb is inert.
func TestAutoModeAllowOnlyProposes(t *testing.T) {
	d := newDaemonForTest(t)
	resp := automodePropose(t, d, automode.KindAllow, "", "git push origin*")
	if !resp.Ok {
		t.Fatalf("propose: %v", protocol.Deref(resp.Error))
	}
	if resp.AutomodeProposeResult.Proposal.State != automode.StatePending {
		t.Errorf("proposal state = %q", resp.AutomodeProposeResult.Proposal.State)
	}
	after := automodeShow(t, d)
	if len(after.Config.Allow) != 0 {
		t.Fatalf("effective allow list changed to %v", after.Config.Allow)
	}
	if len(after.Proposals) != 1 {
		t.Fatalf("proposals = %d, want the one just recorded", len(after.Proposals))
	}
}

func TestAutoModeProposeRefusesABroadAllowByName(t *testing.T) {
	d := newDaemonForTest(t)
	resp := automodePropose(t, d, automode.KindAllow, "", "*")
	if resp.Ok {
		t.Fatal("a broad allow proposal was accepted")
	}
	if !strings.Contains(protocol.Deref(resp.Error), "broad allow pattern") {
		t.Fatalf("refusal does not name the limit: %q", protocol.Deref(resp.Error))
	}
	if len(automodeShow(t, d).Proposals) != 0 {
		t.Fatal("the refused proposal reached the review list")
	}
}

func TestAutoModeEnvironmentAddAndRemove(t *testing.T) {
	d := newDaemonForTest(t)
	for _, text := range []string{"pushing to origin is fine", "never touch prod"} {
		resp := docCall(t, func(c net.Conn) {
			d.handleAutoModeEnvAdd(c, &protocol.AutoModeEnvAddMessage{Cmd: protocol.CmdAutoModeEnvAdd, Text: text})
		})
		if !resp.Ok {
			t.Fatalf("env add: %v", protocol.Deref(resp.Error))
		}
	}
	resp := docCall(t, func(c net.Conn) {
		d.handleAutoModeEnvRemove(c, &protocol.AutoModeEnvRemoveMessage{Cmd: protocol.CmdAutoModeEnvRemove, Index: 0})
	})
	if !resp.Ok {
		t.Fatalf("env remove: %v", protocol.Deref(resp.Error))
	}
	if got := resp.AutomodeEnvResult.Environment; len(got) != 1 || got[0] != "never touch prod" {
		t.Fatalf("environment = %v", got)
	}

	// An out-of-range index names the limit and the ask rather than silently
	// removing nothing.
	resp = docCall(t, func(c net.Conn) {
		d.handleAutoModeEnvRemove(c, &protocol.AutoModeEnvRemoveMessage{Cmd: protocol.CmdAutoModeEnvRemove, Index: 7})
	})
	if resp.Ok {
		t.Fatal("removing entry 7 of 1 succeeded")
	}
	if msg := protocol.Deref(resp.Error); !strings.Contains(msg, "7") || !strings.Contains(msg, "1 environment entries") {
		t.Fatalf("refusal does not carry the limit and the ask: %q", msg)
	}
}

// The unix-socket read `attn automode denials` answers with: newest first, and
// carrying the rule that decided so the feed says who refused the call.
func TestAutoModeDenialsReadsWhatSessionsReported(t *testing.T) {
	d := newDaemonForTest(t)
	resp := docCall(t, func(c net.Conn) {
		d.handleAutoModeDenials(c, &protocol.AutoModeDenialsMessage{Cmd: protocol.CmdAutoModeDenials})
	})
	if !resp.Ok {
		t.Fatalf("denials: %v", protocol.Deref(resp.Error))
	}
	if len(resp.AutomodeDenialsResult.Denials) != 0 {
		t.Fatalf("a machine that denied nothing has denials: %+v", resp.AutomodeDenialsResult.Denials)
	}

	for _, action := range []string{"bash: curl https://one.example", "write /etc/hosts"} {
		if _, _, err := d.store.RecordAutoModeDenial(store.AutoModeDenial{
			SessionID: "pi-1", Tool: "bash", Signature: action,
			Reason: "outside the envelope", Rule: "classifier-2a",
		}, time.Now()); err != nil {
			t.Fatalf("record denial: %v", err)
		}
	}
	resp = docCall(t, func(c net.Conn) {
		d.handleAutoModeDenials(c, &protocol.AutoModeDenialsMessage{Cmd: protocol.CmdAutoModeDenials})
	})
	listed := resp.AutomodeDenialsResult.Denials
	if len(listed) != 2 {
		t.Fatalf("denials = %+v, want both", listed)
	}
	if listed[0].Signature != "write /etc/hosts" {
		t.Errorf("newest denial = %q", listed[0].Signature)
	}
	if listed[0].Rule != "classifier-2a" || listed[0].SessionID != "pi-1" || listed[0].CreatedAt == "" {
		t.Errorf("denial = %+v, want session, rule and time", listed[0])
	}
}

func TestAutoModePromoteFromTheAppPutsItInForce(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	resp := automodePropose(t, d, automode.KindAllow, "", "git push origin*")
	if !resp.Ok {
		t.Fatalf("propose: %v", protocol.Deref(resp.Error))
	}
	id := resp.AutomodeProposeResult.Proposal.ID

	client := busTestClient()
	d.handleAutoModePromote(client, &protocol.AutoModePromoteMessage{ID: id, RequestID: "r1"})
	var promoted protocol.AutoModePromoteResultMessage
	nextBusMessage(t, client, &promoted)
	if !promoted.Success {
		t.Fatalf("promote failed: %q", protocol.Deref(promoted.Error))
	}
	if promoted.Config == nil || len(promoted.Config.Allow) != 1 {
		t.Fatalf("promoted config = %+v", promoted.Config)
	}
	if got := automodeShow(t, d); len(got.Config.Allow) != 1 || len(got.Proposals) != 0 {
		t.Fatalf("show after promote: allow=%v pending=%d", got.Config.Allow, len(got.Proposals))
	}
}

func TestAutoModeDiscardFromTheAppClosesWithoutApplying(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	resp := automodePropose(t, d, automode.KindModel, automode.TargetClassifier, "opencode-go/other-model")
	if !resp.Ok {
		t.Fatalf("propose: %v", protocol.Deref(resp.Error))
	}
	client := busTestClient()
	d.handleAutoModeDiscard(client, &protocol.AutoModeDiscardMessage{
		ID: resp.AutomodeProposeResult.Proposal.ID, RequestID: "r1",
	})
	var discarded protocol.AutoModeDiscardResultMessage
	nextBusMessage(t, client, &discarded)
	if !discarded.Success {
		t.Fatalf("discard failed: %q", protocol.Deref(discarded.Error))
	}
	got := automodeShow(t, d)
	if got.Config.ClassifierModel != automode.DefaultClassifierModel {
		t.Errorf("classifier model = %q after a discard", got.Config.ClassifierModel)
	}
	if len(got.Proposals) != 0 {
		t.Errorf("discarded proposal is still pending")
	}
}

func TestAutoModePromoteRefusesAnUnknownProposal(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	client := busTestClient()
	d.handleAutoModePromote(client, &protocol.AutoModePromoteMessage{ID: 404, RequestID: "r1"})
	var result protocol.AutoModePromoteResultMessage
	nextBusMessage(t, client, &result)
	if result.Success {
		t.Fatal("promoting a proposal that does not exist succeeded")
	}
	if !strings.Contains(protocol.Deref(result.Error), "404") {
		t.Fatalf("refusal does not name the ask: %q", protocol.Deref(result.Error))
	}
}

// Promotion has no unix-socket half, and that absence is the trust boundary: an
// agent reaches this socket, a human reaches the app. A future change that
// registers a case for it fails here.
func TestPromotionIsNotReachableOverTheUnixSocket(t *testing.T) {
	d := newDaemonForTest(t)
	for _, cmd := range []string{protocol.CmdAutoModePromote, protocol.CmdAutoModeDiscard} {
		client, server := net.Pipe()
		go func() {
			d.handleConnection(server)
		}()
		payload := `{"cmd":"` + cmd + `","id":1,"request_id":"r1"}`
		if _, err := client.Write([]byte(payload)); err != nil {
			t.Fatalf("write %s: %v", cmd, err)
		}
		var resp protocol.Response
		if err := json.NewDecoder(client).Decode(&resp); err != nil {
			t.Fatalf("decode %s response: %v", cmd, err)
		}
		client.Close()
		if resp.Ok {
			t.Fatalf("%s was answered over the unix socket", cmd)
		}
		if got := protocol.Deref(resp.Error); !strings.Contains(got, "unknown command") {
			t.Fatalf("%s was refused for the wrong reason: %q", cmd, got)
		}
	}
}

// The launch surface needs the promoted default to show what a session would
// get, and the settings snapshot is how it gets there — read-only: the config
// is written through the automode verbs, never through set_setting.
func TestSettingsSnapshotCarriesTheAutoModeDefault(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	if got := d.settingsWithAgentAvailability()[SettingAutoModeEnabledDefault]; got != "true" {
		t.Errorf("%s = %q on a fresh database, want the shipped default", SettingAutoModeEnabledDefault, got)
	}
	if _, err := d.store.SetAutoModeEnabledDefault(false, time.Now().UTC()); err != nil {
		t.Fatalf("set default: %v", err)
	}
	if got := d.settingsWithAgentAvailability()[SettingAutoModeEnabledDefault]; got != "false" {
		t.Errorf("%s = %q after turning it off", SettingAutoModeEnabledDefault, got)
	}
	if err := d.validateSetting(SettingAutoModeEnabledDefault, "true"); err == nil {
		t.Error("set_setting accepted the daemon-computed auto mode default")
	}
}

// The denial wire, daemon end: a driver reports one refused call and it becomes
// a row, a notification, and one automode.denied fact naming its session.
func TestAutoModeDenialFromADriverBecomesARowANotificationAndAFact(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"state_reporting": true, "auto_mode": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "pi-denial", Label: "envelope work", Agent: "pi", Directory: t.TempDir(),
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	if !d.store.BeginAgentDriverRun("pi-denial", "pi-plugin", "run-1") {
		t.Fatal("failed to begin the test plugin run")
	}

	sendPluginMethod(t, client, 3, "session.report_automode_denial", pluginReportAutoModeDenialParams{
		SessionID: "pi-denial",
		RunID:     "run-1",
		Tool:      "bash",
		Action:    "bash: curl https://example.com",
		Reason:    "the user never asked to reach that host",
		Rule:      "classifier-2a",
		At:        "2026-08-17T10:00:00Z",
	})

	denials, err := d.store.ListAutoModeDenials(10)
	if err != nil {
		t.Fatalf("list denials: %v", err)
	}
	if len(denials) != 1 {
		t.Fatalf("denials = %d, want the one that was reported", len(denials))
	}
	got := denials[0]
	if got.SessionID != "pi-denial" || got.Tool != "bash" || got.Rule != "classifier-2a" {
		t.Errorf("denial row = %+v", got)
	}
	if got.Signature != "bash: curl https://example.com" {
		t.Errorf("signature = %q, want the blocked call", got.Signature)
	}
	if !got.CreatedAt.Equal(time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("created_at = %s, want the time the session refused it", got.CreatedAt)
	}

	notes, err := d.store.ListNotifications()
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("notifications = %d, want the denial's", len(notes))
	}
	note := notes[0]
	if note.Kind != notificationKindAutoModeDenied || note.SourceID != "pi-denial" {
		t.Errorf("notification = %+v", note)
	}
	if !strings.Contains(note.Title, "envelope work") {
		t.Errorf("title does not name the session: %q", note.Title)
	}
	if !strings.Contains(note.Body, "curl https://example.com") {
		t.Errorf("body does not say what was blocked: %q", note.Body)
	}
	if !strings.Contains(note.Detail, "never asked to reach that host") ||
		!strings.Contains(note.Detail, "classifier-2a") {
		t.Errorf("detail does not carry the reason and who decided: %q", note.Detail)
	}

	published := docFacts(t, d, FactAutoModeDenied)
	if len(published) != 1 || published[0].Subject != "pi-denial" {
		t.Fatalf("automode.denied facts = %+v, want one naming the session", published)
	}
}

// A denial reported against a run this plugin does not own changes nothing —
// the denials feed is what the user trusts to say what their sessions did.
func TestAutoModeDenialFromAnUnownedRunIsRefused(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"auto_mode": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "pi-denial", Label: "pi", Agent: "pi", Directory: t.TempDir(),
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	if !d.store.BeginAgentDriverRun("pi-denial", "pi-plugin", "run-1") {
		t.Fatal("failed to begin the test plugin run")
	}

	response := sendPluginMethodResponse(t, client, 3, "session.report_automode_denial",
		pluginReportAutoModeDenialParams{SessionID: "pi-denial", RunID: "run-other", Action: "bash: git push --force"})
	if response.Error == nil {
		t.Fatal("a denial for a run the plugin does not own was accepted")
	}

	response = sendPluginMethodResponse(t, client, 4, "session.report_automode_denial",
		pluginReportAutoModeDenialParams{SessionID: "pi-denial", RunID: "run-1", Action: "   "})
	if response.Error == nil {
		t.Fatal("a denial with no action named was accepted")
	}

	denials, err := d.store.ListAutoModeDenials(10)
	if err != nil {
		t.Fatalf("list denials: %v", err)
	}
	if len(denials) != 0 {
		t.Fatalf("refused denials reached the log: %+v", denials)
	}
}
