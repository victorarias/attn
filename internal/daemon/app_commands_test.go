package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
)

// A view acting. What these pin: the envelope reaches the app's own handler with
// the payload untouched, the answer comes back to the caller that asked, every
// refusal says what to do about it, and a command that fails costs the app
// nothing beyond that click.

func commandManifest(commands ...appbuild.Command) appbuild.Manifest {
	return appbuild.Manifest{
		Views:    []appbuild.View{tileView("approvals", "Pending approvals")},
		Commands: commands,
	}
}

// appCommandCaller is one WebSocket client invoking commands, decoded into the
// one envelope this surface answers with.
type appCommandCaller struct {
	client *wsClient
}

func newAppCommandCaller() *appCommandCaller {
	return &appCommandCaller{client: &wsClient{send: make(chan outboundMessage, 16)}}
}

func (c *appCommandCaller) invoke(t *testing.T, d *Daemon, app, command, payload string) protocol.AppCommandResultMessage {
	t.Helper()
	msg := &protocol.AppCommandMessage{
		Cmd:       protocol.CmdAppCommand,
		RequestID: "req-1",
		App:       app,
		Command:   command,
	}
	if payload != "" {
		msg.Payload = protocol.Ptr(payload)
	}
	d.handleAppCommand(c.client, msg)
	return c.result(t)
}

func (c *appCommandCaller) result(t *testing.T) protocol.AppCommandResultMessage {
	t.Helper()
	select {
	case msg := <-c.client.send:
		var out protocol.AppCommandResultMessage
		if err := json.Unmarshal(msg.payload, &out); err != nil {
			t.Fatalf("decode the answer: %v", err)
		}
		if out.Event != protocol.EventAppCommandResult {
			t.Fatalf("the daemon answered with %q", out.Event)
		}
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon never answered the command")
		return protocol.AppCommandResultMessage{}
	}
}

func mustFail(t *testing.T, result protocol.AppCommandResultMessage, wants ...string) {
	t.Helper()
	if result.Success {
		t.Fatalf("the command succeeded, want a refusal: %+v", result)
	}
	message := protocol.Deref(result.Error)
	for _, want := range wants {
		if !strings.Contains(message, want) {
			t.Errorf("the refusal does not say %q: %s", want, message)
		}
	}
}

// The whole path: a declared command reaches the handler bound to it, carrying
// the caller's payload byte for byte, and what the handler returned reaches the
// caller.
func TestAppCommandRunsTheHandlerAndAnswersTheCaller(t *testing.T) {
	d := newAppDaemon(t)
	version := installApp(t, d, "reviewer", commandManifest(appbuild.Command{Name: "approve"}))
	runtime := startFakeAppRuntime(t, d, nil)
	runtime.command = func(_ *fakeAppRuntime, req appCommandRequest) (json.RawMessage, error) {
		return json.RawMessage(`{"approved":"tk-1"}`), nil
	}

	result := newAppCommandCaller().invoke(t, d, "reviewer", "approve", `{"id":"tk-1"}`)

	if !result.Success {
		t.Fatalf("the command failed: %s", protocol.Deref(result.Error))
	}
	if protocol.Deref(result.Payload) != `{"approved":"tk-1"}` {
		t.Fatalf("the handler's answer did not reach the caller: %v", result.Payload)
	}
	log := runtime.commandLog()
	if len(log) != 1 {
		t.Fatalf("the sidecar ran %d commands", len(log))
	}
	got := log[0]
	if got.Handler != apps.CommandHandlerKey("approve") {
		t.Errorf("handler key is %q, want the command: prefix", got.Handler)
	}
	if got.App != "reviewer" || got.VersionID != version.ID {
		t.Errorf("the dispatch names %s version %d, want reviewer version %d", got.App, got.VersionID, version.ID)
	}
	if string(got.Payload) != `{"id":"tk-1"}` {
		t.Errorf("payload reached the handler as %s", got.Payload)
	}

	rows := invocationsOf(t, d, "reviewer")
	if len(rows) != 1 {
		t.Fatalf("invocations = %+v, want the one command", rows)
	}
	if rows[0].Status != appInvocationStatusOK || rows[0].Handler != apps.CommandHandlerKey("approve") {
		t.Errorf("invocation = %+v", rows[0])
	}
	if rows[0].EventName != appCommandEvent || rows[0].EventSubject != "approve" {
		t.Errorf("a command's invocation must name itself, not borrow a fact: %+v", rows[0])
	}
}

// A command that takes no argument is a command, and "returned nothing" has to
// survive as nothing rather than as a null the view has to know about.
func TestAppCommandWithNoPayloadAndNoAnswerSucceeds(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "reviewer", commandManifest(appbuild.Command{Name: "refresh"}))
	startFakeAppRuntime(t, d, nil)

	result := newAppCommandCaller().invoke(t, d, "reviewer", "refresh", "")

	if !result.Success {
		t.Fatalf("the command failed: %s", protocol.Deref(result.Error))
	}
	if result.Payload != nil {
		t.Fatalf("a handler that returned nothing carried %v", *result.Payload)
	}
}

// The serving version's declaration is the contract, exactly as it is for
// views: a rollback takes a command away with it, and the refusal says which
// version is answering and what it does declare.
func TestAppCommandIsRefusedByTheServingVersionsDeclaration(t *testing.T) {
	d := newAppDaemon(t)
	first := installApp(t, d, "reviewer", commandManifest(appbuild.Command{Name: "approve"}))
	installApp(t, d, "reviewer", commandManifest(
		appbuild.Command{Name: "approve"}, appbuild.Command{Name: "reject"}))
	startFakeAppRuntime(t, d, nil)
	caller := newAppCommandCaller()

	if result := caller.invoke(t, d, "reviewer", "reject", ""); !result.Success {
		t.Fatalf("the newly declared command failed: %s", protocol.Deref(result.Error))
	}
	if err := d.store.SetAppCurrentVersion("reviewer", first.ID, first.CreatedAt); err != nil {
		t.Fatalf("roll back: %v", err)
	}

	mustFail(t, caller.invoke(t, d, "reviewer", "reject", ""), "reject", "approve", "reviewer")
}

func TestAppCommandRefusesAnAppThatIsDisabledOrMissing(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "reviewer", commandManifest(appbuild.Command{Name: "approve"}))
	startFakeAppRuntime(t, d, nil)
	caller := newAppCommandCaller()

	mustFail(t, caller.invoke(t, d, "ghost", "approve", ""), "ghost", "attn app apply")

	if resp := appSetEnabled(t, d, "reviewer", false); !resp.Ok {
		t.Fatalf("disable reviewer: %v", protocol.Deref(resp.Error))
	}
	mustFail(t, caller.invoke(t, d, "reviewer", "approve", ""), "disabled", "attn app enable reviewer")
}

// A limit someone can hit is a limit they must see: the refusal names the limit,
// its value and the ask, and says where larger data belongs.
func TestAppCommandRefusesAPayloadOverTheLimit(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "reviewer", commandManifest(appbuild.Command{Name: "approve"}))
	startFakeAppRuntime(t, d, nil)

	huge := `{"note":"` + strings.Repeat("x", appCommandPayloadLimit) + `"}`
	result := newAppCommandCaller().invoke(t, d, "reviewer", "approve", huge)

	mustFail(t, result, "approve", "reviewer", "262144", "document")
}

func TestAppCommandRefusesAPayloadThatIsNotJSON(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "reviewer", commandManifest(appbuild.Command{Name: "approve"}))
	startFakeAppRuntime(t, d, nil)

	mustFail(t, newAppCommandCaller().invoke(t, d, "reviewer", "approve", "{not json"), "JSON")
}

// A handler that throws is the app's fault and the caller's answer — recorded,
// reported in the handler's own words, and nothing more.
func TestAppCommandCarriesAThrownHandlerBackToTheCaller(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "reviewer", commandManifest(appbuild.Command{Name: "approve"}))
	runtime := startFakeAppRuntime(t, d, nil)
	runtime.command = func(*fakeAppRuntime, appCommandRequest) (json.RawMessage, error) {
		return nil, errTest("Error: approve needs an id")
	}

	mustFail(t, newAppCommandCaller().invoke(t, d, "reviewer", "approve", ""), "approve needs an id")

	rows := invocationsOf(t, d, "reviewer")
	if len(rows) != 1 || rows[0].Status != appInvocationStatusError {
		t.Fatalf("invocations = %+v, want one recorded failure", rows)
	}
}

// A handler that never returns is abandoned, and the refusal is one an agent can
// act on: the command, the app, and the limit that was reached.
func TestAppCommandAbandonsAHandlerThatNeverReturns(t *testing.T) {
	d := newAppDaemon(t)
	// The shipped tripwires are 60s and 2s; waiting them out would prove nothing
	// extra and cost a minute.
	d.appDispatchWait = 300 * time.Millisecond
	d.appPingWait = 50 * time.Millisecond
	installApp(t, d, "reviewer", commandManifest(appbuild.Command{Name: "approve"}))
	runtime := startFakeAppRuntime(t, d, nil)
	runtime.command = func(f *fakeAppRuntime, _ appCommandRequest) (json.RawMessage, error) {
		f.freezeLoop()
		select {}
	}

	result := newAppCommandCaller().invoke(t, d, "reviewer", "approve", "")

	mustFail(t, result, "approve", "reviewer", "300ms")
	// The clock that disables an app exists for a consumer pinning the durable
	// log. A command pins nothing, so a docked tile must not be able to switch a
	// healthy app off by clicking.
	if status := appStatus(t, d, "reviewer"); status.AppStatusResult.Stall != nil {
		t.Fatalf("a failed command advanced the stall clock: %+v", status.AppStatusResult.Stall)
	}
}

// Nothing to answer is worse than an error: a request with no id could never
// reach the caller, so it is refused where the caller can still see it.
func TestAppCommandWithoutARequestIDIsRefusedOnTheSpot(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "reviewer", commandManifest(appbuild.Command{Name: "approve"}))
	caller := newAppCommandCaller()

	d.handleAppCommand(caller.client, &protocol.AppCommandMessage{
		Cmd: protocol.CmdAppCommand, App: "reviewer", Command: "approve",
	})

	select {
	case msg := <-caller.client.send:
		var out map[string]any
		if err := json.Unmarshal(msg.payload, &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out["event"] != protocol.EventCommandError {
			t.Fatalf("answered with %v, want an error event", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a command with no request id was answered with silence")
	}
}

// `attn app status` is the only way to see that a command exists without
// invoking it, and what it shows is the serving version's declaration.
func TestAppStatusCarriesTheServingVersionsCommands(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "reviewer", commandManifest(
		appbuild.Command{Name: "approve", Description: "Approve the request."},
		appbuild.Command{Name: "reject"},
	))

	resp := appStatus(t, d, "reviewer")
	if !resp.Ok {
		t.Fatalf("app status: %v", protocol.Deref(resp.Error))
	}
	commands := resp.AppStatusResult.App.Commands
	if len(commands) != 2 {
		t.Fatalf("commands = %+v, want both", commands)
	}
	if commands[0].Name != "approve" || protocol.Deref(commands[0].Description) != "Approve the request." {
		t.Errorf("first command = %+v", commands[0])
	}
	if commands[1].Description != nil {
		t.Errorf("a command with no description carried one: %+v", commands[1])
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
