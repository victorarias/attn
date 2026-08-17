package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type appReconcileOwedError struct {
	reason appReconcileReason
}

func (e *appReconcileOwedError) Error() string {
	return fmt.Sprintf(
		"reconcile_owed: app collection rebuild is owed through bus seq %d (%s); commands remain refused until reconcile succeeds",
		e.reason.ThroughSeq, strings.Join(e.reason.Causes, ", "))
}

// A view acting: `app_command` in, `app_command_result` back.
//
// A view that can only read is a way in with no way out. What makes this a
// small addition rather than a second runtime is that it ends where a
// subscription ends — one key of the app's default export, dispatched into the
// shared sidecar, recorded in the same invocation log, bounded by the same
// timeout. The envelope is generic: attn carries an app, a command name and a
// payload it never looks inside.
//
// Two rules the reader should not have to infer.
//
// The serving version's declaration is the contract. Which commands an app
// answers comes from the version it is serving, never from the manifest on
// disk — after a rollback those differ, and the running code is the honest
// answer.
//
// A failing command does not advance the auto-disable clock. That clock exists
// because an app stuck redelivering one bus event pins the durable log's
// retention floor for every other consumer. A command pins nothing: it is a
// person clicking a button, and letting that switch the app off would hand any
// docked tile a way to disable an app whose handlers are healthy. It is the
// same reasoning that keeps a caught render crash off the clock.
//
// Design: docs/plans/2026-08-13-ext-a5-ui-host-and-app-sdk.md, "Protocol
// envelopes".

// appCommandEvent is the event name a command invocation is recorded under. A
// command has no fact behind it, so it names itself rather than borrowing a bus
// fact's name and claiming a seq it does not have.
const appCommandEvent = "app.command"

// appCommandPayloadLimit bounds what one command may carry in either
// direction.
//
// The tripwire is the socket and the invocation log, not the payload: a command
// is an action ("approve this, with this note"), not a data transfer, and the
// document store is what an app uses to move anything larger. 256KB is ~8x the
// largest thing a view could plausibly hand a handler — the crash reporter's
// own limit for a whole component stack is 32KB — so an app has to be using
// this as a pipe to feel it.
const appCommandPayloadLimit = 256 * 1024

// handleAppCommand runs one command and answers the client that asked.
//
// It answers off the message pump because a dispatch may take as long as
// appDispatchTimeout, and this client's other commands — including the
// doc_unsubscribe of a tile the user just closed — are processed in order
// behind it.
func (d *Daemon) handleAppCommand(client *wsClient, msg *protocol.AppCommandMessage) {
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		// There is nothing to answer: the caller minted no id, so no promise on the
		// other side is waiting for a result carrying one.
		d.sendCommandError(client, protocol.CmdAppCommand,
			"app_command requires a request_id: the result is correlated by it, and one without an id could never reach the caller")
		return
	}
	go func() {
		payload, err := d.runAppCommand(msg)
		d.sendToClient(client, appCommandResult(requestID, payload, err))
	}()
}

func appCommandResult(requestID string, payload json.RawMessage, err error) protocol.AppCommandResultMessage {
	result := protocol.AppCommandResultMessage{
		Event:     protocol.EventAppCommandResult,
		RequestID: requestID,
		Success:   err == nil,
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		return result
	}
	if len(payload) > 0 {
		result.Payload = protocol.Ptr(string(payload))
	}
	return result
}

// runAppCommand resolves the command against the serving version and dispatches
// it. The returned error is what the caller reads in the tile, so every refusal
// says what to do about it.
func (d *Daemon) runAppCommand(msg *protocol.AppCommandMessage) (json.RawMessage, error) {
	name := strings.TrimSpace(msg.App)
	if err := apps.ValidateName(name); err != nil {
		return nil, err
	}
	command := strings.TrimSpace(msg.Command)
	if err := apps.ValidateCommandName(command); err != nil {
		return nil, err
	}
	payload := json.RawMessage(strings.TrimSpace(protocol.Deref(msg.Payload)))
	if len(payload) > appCommandPayloadLimit {
		return nil, fmt.Errorf(
			"the payload for %s/%s is %d bytes, over the %d-byte limit for one command; a command is an action, and anything larger belongs in a document the handler reads",
			name, command, len(payload), appCommandPayloadLimit)
	}
	if len(payload) > 0 && !json.Valid(payload) {
		return nil, fmt.Errorf("the payload for %s/%s is not valid JSON", name, command)
	}
	if d.store == nil {
		return nil, fmt.Errorf("this daemon has no store, so it runs no apps")
	}
	lane := d.appLane(name)
	lane.Lock()
	defer lane.Unlock()

	plan, err := d.planAppCommand(name, command)
	if err != nil {
		return nil, err
	}

	started := d.appNow()
	ctx, cancel := context.WithTimeout(context.Background(), d.appDispatchBudget())
	defer cancel()
	result, dispatchErr := d.dispatchAppCommand(ctx, plan, payload)
	took := d.appNow().Sub(started)

	invocation := store.AppInvocation{
		AppName:      name,
		VersionID:    plan.versionID,
		Kind:         store.AppInvocationKindCommand,
		EventName:    appCommandEvent,
		EventSubject: command,
		Handler:      plan.label,
		Duration:     took,
		StartedAt:    started,
	}
	switch {
	case dispatchErr != nil:
		// Both classes are recorded — a reader looking at an app that did nothing
		// deserves to see why — and neither is held against the app's stall clock.
		// The runtime being down is not the app's fault, and a handler that threw
		// on a click is not a stall.
		if errors.Is(dispatchErr, context.DeadlineExceeded) {
			dispatchErr = fmt.Errorf(
				"the handler for command %q of app %s did not return within %s, so attn abandoned it; a handler awaits attn's own APIs, which always settle — an await on anything else needs its own timeout",
				command, name, d.appDispatchBudget())
			invocation.Status = appInvocationStatusError
		} else {
			invocation.Status = appInvocationStatusRuntimeError
		}
		invocation.Error = dispatchErr.Error()
		d.recordAppInvocation(invocation)
		return nil, dispatchErr

	case !result.OK:
		invocation.Status = appInvocationStatusError
		invocation.Error = result.Error
		d.recordAppInvocation(invocation)
		return nil, fmt.Errorf("%s threw running command %q: %s", name, command, firstLine(result.Error))

	case len(result.Payload) > appCommandPayloadLimit:
		// The same limit, kept on the way back: a handler answering with a
		// megabyte would otherwise reach the tile unbounded, which is the
		// direction nobody checks until a view is slow and nothing says why.
		invocation.Status = appInvocationStatusError
		invocation.Error = fmt.Sprintf("the answer is %d bytes, over the %d-byte limit", len(result.Payload), appCommandPayloadLimit)
		d.recordAppInvocation(invocation)
		return nil, fmt.Errorf(
			"the handler for command %q of app %s answered with %d bytes, over the %d-byte limit for one command; a command is an action, and anything larger belongs in a document the view reads",
			command, name, len(result.Payload), appCommandPayloadLimit)

	default:
		invocation.Status = appInvocationStatusOK
		d.recordAppInvocation(invocation)
		return result.Payload, nil
	}
}

// planAppCommand answers whether this app can run this command right now, and
// with what.
func (d *Daemon) planAppCommand(name, command string) (*appDispatchPlan, error) {
	_, ok, err := d.store.GetApp(name)
	if err != nil {
		return nil, fmt.Errorf("reading app %q: %w", name, err)
	}
	if !ok {
		return nil, fmt.Errorf("no app named %s is installed; `attn app apply <path>` installs one", name)
	}
	consumer, found, err := d.store.GetBusConsumer(apps.ConsumerName(name))
	if err != nil {
		return nil, fmt.Errorf("reading the bus consumer of app %q: %w", name, err)
	}
	if !found || !consumer.Enabled {
		return nil, fmt.Errorf("%s is disabled, so it runs nothing; `attn app enable %s` turns it back on", name, name)
	}

	manifest, version, err := d.appDeclaration(name)
	if err != nil {
		return nil, err
	}
	if version.ID == 0 {
		return nil, fmt.Errorf("%s has no version serving, so there is no code to run; `attn app apply <path>` builds one", name)
	}
	claim, err := d.appReconcileClaim(name)
	if err != nil {
		return nil, fmt.Errorf("reading reconciliation owed by app %q: %w", name, err)
	}
	if len(claim.Requests) != 0 {
		return nil, &appReconcileOwedError{reason: foldAppReconcileReason(version.ID, claim)}
	}
	declared := manifest.CommandNames()
	if !containsString(declared, command) {
		// The version, not the manifest on disk: after a rollback the two differ,
		// and the running code is what the caller is actually talking to.
		return nil, fmt.Errorf(
			"the version of %s serving now (%d) declares no command %q; it declares %s. Add a [[commands]] block and `attn app apply`, or roll back to a version that has it",
			name, version.ID, command, commandList(declared))
	}

	plan := &appDispatchPlan{
		app:       name,
		namespace: apps.Namespace(name),
		versionID: version.ID,
		artifact:  version.ArtifactPath,
		handler:   command,
		label:     apps.CommandLabel(command),
	}
	for _, collection := range manifest.Collections {
		plan.collections = append(plan.collections, collection.Name)
	}
	return plan, nil
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func commandList(commands []string) string {
	if len(commands) == 0 {
		return "none"
	}
	return strings.Join(commands, ", ")
}

// dispatchAppCommand sends one command run to the sidecar. It is
// dispatchToAppRuntime's shape — the same in-flight record scoping the
// handler's document access, the same wait for a cold start, the same
// attribution when the shared event loop is frozen — differing only in what
// travels and what comes back.
func (d *Daemon) dispatchAppCommand(ctx context.Context, plan *appDispatchPlan, payload json.RawMessage) (appCommandDispatchResult, error) {
	runtime, err := d.awaitAppRuntime(ctx)
	if err != nil {
		return appCommandDispatchResult{}, err
	}

	dispatch := &appDispatch{
		app:         plan.app,
		namespace:   plan.namespace,
		versionID:   plan.versionID,
		collections: make(map[string]struct{}, len(plan.collections)),
	}
	for _, collection := range plan.collections {
		dispatch.collections[collection] = struct{}{}
	}
	d.registerAppDispatch(dispatch)
	// Released whatever happens, including the abandoned-timeout path: an id left
	// behind would let a handler that finally woke up write documents from
	// outside any invocation.
	defer d.releaseAppDispatch(dispatch.id)

	request := appCommandRequest{
		Dispatch:    dispatch.id,
		App:         plan.app,
		VersionID:   plan.versionID,
		Artifact:    plan.artifact,
		Handler:     plan.handler,
		Collections: plan.collections,
		Payload:     payload,
	}
	if request.Collections == nil {
		request.Collections = []string{}
	}

	result, err := runtime.command(ctx, request)
	if err != nil {
		if ctx.Err() != nil {
			// Our own deadline: something in the sidecar is stuck, and which app is
			// holding the loop is the host's to say. The answer is the same one a bus
			// delivery gets — this dispatch is still in the in-flight set for it to
			// be right.
			return appCommandDispatchResult{}, d.attributeWedgedDispatch(context.Background(), runtime, plan.app)
		}
		// The transport failed — the socket died mid-call, or the process did.
		return appCommandDispatchResult{}, runtimeFailure("%v", err)
	}
	return result, nil
}

// appDeclaredCommands reads a version's commands out of its frozen declaration,
// for the CLI's `commands:` line. A declaration that will not parse costs the
// reader that line, not the app.
func appDeclaredCommands(declaration string, logf func(string, ...any)) []protocol.AppCommandInfo {
	var snapshot struct {
		Commands []appbuild.Command `json:"commands"`
	}
	if err := json.Unmarshal([]byte(declaration), &snapshot); err != nil {
		if logf != nil {
			logf("apps: reading the commands of a stored declaration: %v", err)
		}
		return nil
	}
	out := make([]protocol.AppCommandInfo, 0, len(snapshot.Commands))
	for _, c := range snapshot.Commands {
		info := protocol.AppCommandInfo{Name: c.Name}
		if c.Description != "" {
			info.Description = protocol.Ptr(c.Description)
		}
		out = append(out, info)
	}
	return out
}
