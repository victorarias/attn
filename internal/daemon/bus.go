package daemon

import (
	"bytes"
	"encoding/json"
	"sync"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
)

// The daemon's side of the event bus: the fact vocabulary, the lifecycle, and the
// projection table that turns facts into what WebSocket clients see.
//
// The migration pattern for A2 is the two halves below.
//
// PRODUCER. A `broadcastXxx` method stops touching the hub and publishes a fact
// instead. The fact must carry a subject — the entity it is about — because a
// subject-less "the list changed" fact is the snapshot invalidation this design
// exists to avoid. When the existing method already receives the entity id, it
// becomes a one-line publish and no call site changes (see
// broadcastSessionStateChanged). When it does not — broadcastTicketsUpdated took
// no arguments because it re-pushed the whole board — the call sites are the ones
// that know which fact occurred, and they are what changes.
//
// PROJECTION. The old broadcaster body moves into a `projectXxx` method and is
// registered in wireProjections. The hub subscribes ephemerally to every fact and
// runs the matching projections inline on the publishing goroutine, so a fact
// still produces exactly the same wire traffic, in the same order, as the direct
// call it replaced.
//
// A projection writes to the wire and does nothing else. It must not mutate
// state and it must not publish: the bus holds its publish lock across the
// inline fan-out, so a fact published from inside a projection deadlocks. When
// the old broadcaster body did something beyond pushing bytes — recomputing a
// workspace's rolled-up status, say — that part stays on the producer side,
// where it is a fact of its own.
//
// WHAT DOES NOT MOVE. Every state change goes through the bus. The exceptions
// are enumerated, and the list is enforced by TestWireTrafficComesFromProjections
// (bus_wire_boundary_test.go), which fails on a new broadcaster that skips the
// bus:
//
//   - Byte streams. PTY output, PTY desync, attach results, and workspace tile
//     content are high volume and carry no entity worth subscribing to. Attach
//     and tile traffic is also routed by a per-client predicate
//     (SendRawTextToMatchingClients), which pub/sub cannot express.
//   - Filesystem change bursts (broadcastFsChanged), coalesced per watcher
//     rather than per file.
//   - The remote relay (broadcastRawWSMessage). Those events were already
//     published as facts on the remote daemon's own bus; re-publishing them
//     here would duplicate them.
//
// See docs/plans/2026-08-01-ext-a1-event-bus.md.

// Fact names are dotted `domain.verb`. `ext.<extension>.*` is reserved for facts
// published by extensions.
const (
	// Session facts; subject is the session id.
	//
	// Registered and reregistered are separate facts rather than one fact with a
	// flag, because they are separate things: a session appearing for the first
	// time and a live session being re-announced by a reconnecting client. They
	// project to different wire events, which is how the daemon has always
	// treated them.
	FactSessionRegistered   = "session.registered"
	FactSessionReregistered = "session.reregistered"
	FactSessionStateChanged = "session.state.changed"
	FactSessionRenamed      = "session.renamed"
	FactSessionUnregistered = "session.unregistered"
	FactSessionTodosChanged = "session.todos.changed"
	FactSessionRespawned    = "session.respawned"
	FactSessionPTYResized   = "session.pty.resized"
	// FactSessionTerminated is a session going away as part of a bulk operation
	// (clear-all, a worktree deletion taking its sessions with it). It is not
	// FactSessionUnregistered: unregistering announces the individual session to
	// clients, while these paths have only ever re-pushed the list.
	FactSessionTerminated = "session.terminated"
	// FactSessionBranchChanged: the branch monitor saw this session's checkout move.
	FactSessionBranchChanged = "session.branch.changed"
	// FactSessionChiefRoleChanged: this session took or lost the chief-of-staff role.
	FactSessionChiefRoleChanged = "session.chief_role.changed"
	// FactSessionReconciled: startup/recovery reconciliation changed this session.
	FactSessionReconciled = "session.reconciled"
	// FactSessionPTYExited: this session's PTY process exited.
	FactSessionPTYExited = "session.pty.exited"
	// FactSessionWorkspaceChanged: this session moved to another workspace.
	FactSessionWorkspaceChanged = "session.workspace.changed"
	// FactSessionPinChanged: this session was pinned out of the queue, or
	// released back into it. Distinct from FactWorkspacePinChanged, which moves
	// a whole workspace and its siblings.
	FactSessionPinChanged = "session.pin.changed"

	// FactWorktreeSessionsRemoved: deleting this worktree took its sessions with
	// it. Subject is the worktree path.
	FactWorktreeSessionsRemoved = "worktree.sessions.removed"

	// FactEndpointSessionsChanged: a remote endpoint's session set changed.
	// Subject is the endpoint id — the entity the hub manager knows about, and
	// the one an extension watching a remote fleet would filter on.
	FactEndpointSessionsChanged = "endpoint.sessions.changed"

	// Workspace facts; subject is the workspace id.
	//
	// Eight of these project to the same wire event. They are still eight facts:
	// "the workspace was muted" and "a session joined it" are different things to
	// subscribe to, and only the producer knows which happened. Collapsing them
	// into one workspace.changed would hand every consumer the diffing problem
	// the bus exists to remove.
	FactWorkspaceRegistered         = "workspace.registered"
	FactWorkspaceReregistered       = "workspace.reregistered"
	FactWorkspaceRenamed            = "workspace.renamed"
	FactWorkspaceStatusChanged      = "workspace.status.changed"
	FactWorkspaceMuteChanged        = "workspace.mute.changed"
	FactWorkspacePinChanged         = "workspace.pin.changed"
	FactWorkspaceRankChanged        = "workspace.rank.changed"
	FactWorkspaceSessionAssociated  = "workspace.session.associated"
	FactWorkspaceSessionDissociated = "workspace.session.dissociated"
	FactWorkspaceUnregistered       = "workspace.unregistered"
	FactWorkspaceLayoutChanged      = "workspace.layout.changed"
	FactWorkspaceLayoutRepublished  = "workspace.layout.republished"
	FactWorkspaceContextChanged     = "workspace.context.changed"

	// PR facts; subject is the PR id.
	//
	// The three set facts come from diffing the PR list around a bulk refresh.
	// The daemon replaces the whole set on every poll, so the diff is what
	// recovers the facts: without it a poll could only say "the list changed",
	// which is the invalidation this design removes.
	FactPRAppeared    = "pr.appeared"
	FactPRUpdated     = "pr.updated"
	FactPRDisappeared = "pr.disappeared"
	// The rest name a single PR the user or the daemon acted on directly.
	FactPRMuteChanged    = "pr.mute.changed"
	FactPRVisited        = "pr.visited"
	FactPRHeatChanged    = "pr.heat.changed"
	FactPRDetailsChanged = "pr.details.changed"

	// Worktree facts. Subject is the worktree path, except the reconcile, whose
	// subject is the main repo: reading git prunes worktrees that are gone and
	// adopts ones created outside attn, and the resulting list is per repo.
	FactWorktreeCreated        = "worktree.created"
	FactWorktreeDeleted        = "worktree.deleted"
	FactWorktreeListReconciled = "worktree.list.reconciled"

	// Git operation facts; subject is the operation id.
	FactGitOperationStarted  = "git.operation.started"
	FactGitOperationFinished = "git.operation.finished"

	// FactRateLimited: subject is the rate-limited resource ("core", "search").
	FactRateLimited = "ratelimit.hit"

	// GitHub host facts; subject is the host.
	FactGitHubHostAdded   = "github.host.added"
	FactGitHubHostRemoved = "github.host.removed"

	// FactRepoMuteChanged: subject is the repo, `owner/name`.
	FactRepoMuteChanged = "repo.mute.changed"
	// FactAuthorMuteChanged: subject is the author's login.
	FactAuthorMuteChanged = "author.mute.changed"

	// Endpoint facts; subject is the endpoint id.
	FactEndpointAdded         = "endpoint.added"
	FactEndpointRemoved       = "endpoint.removed"
	FactEndpointChanged       = "endpoint.changed"
	FactEndpointStatusChanged = "endpoint.status.changed"

	// Plugin facts; subject is the plugin name.
	FactPluginInstalled       = "plugin.installed"
	FactPluginUninstalled     = "plugin.uninstalled"
	FactPluginPriorityChanged = "plugin.priority.changed"
	FactPluginConnected       = "plugin.connected"
	FactPluginDisconnected    = "plugin.disconnected"
	FactPluginHealthChanged   = "plugin.health.changed"
	// FactPluginDriverRegistered changes which agents are available, which is
	// part of settings, so it is the one plugin fact that does not re-push the
	// plugin list.
	FactPluginDriverRegistered = "plugin.driver.registered"

	// FactSettingChanged: subject is the setting key.
	FactSettingChanged = "setting.changed"
	// FactBackupWritten: subject is the backup file path. Clients learn about it
	// through settings, which carry db.last_backup_at.
	FactBackupWritten = "backup.written"
	// FactTailscaleServeChanged: subject is the profile. There is one tailscale
	// serve per daemon and one daemon per profile, so the profile is its id.
	FactTailscaleServeChanged = "tailscale.serve.changed"

	// Notification facts; subject is the notification id.
	FactNotificationCreated = "notification.created"
	FactNotificationRead    = "notification.read"

	// FactAutomationChanged: subject is the automation definition id.
	FactAutomationChanged = "automation.changed"
	// FactWorkflowRunUpdated: subject is the workflow run id.
	FactWorkflowRunUpdated = "workflow.run.updated"
	// FactTaskChanged: subject is the background task id.
	FactTaskChanged = "task.changed"

	// FactNotebookFileChanged: subject is the notebook-relative path.
	FactNotebookFileChanged = "notebook.file.changed"

	// Presentation facts; subject is the presentation id.
	FactPresentationAdded   = "presentation.added"
	FactPresentationUpdated = "presentation.updated"

	// Ticket facts; subject is the ticket id.
	FactTicketCreated       = "ticket.created"
	FactTicketStatusChanged = "ticket.status_changed"
	FactTicketCommented     = "ticket.commented"
	FactTicketAssigned      = "ticket.assigned"
	FactTicketAttached      = "ticket.attached"
	// FactTicketChanged is the fallback for a mutation whose call site cannot
	// name a sharper fact. It is still about one ticket — that is the line
	// between a fact and an invalidation — but a sharper name is preferred
	// wherever the producer knows one.
	FactTicketChanged = "ticket.changed"

	// FactDocumentChanged: a document store record was written or removed.
	// Subject is the document's address (namespace/collection/id); the payload
	// carries the address in parts plus whether it was a removal.
	//
	// It is appended by the store, inside the transaction that made the change,
	// and announced afterwards — see CommitDocumentWrite. That is what makes the
	// seq it lands at usable as the write's position.
	//
	// This fact has no entry in wireProjections, and that is not an omission: it
	// produces no WebSocket traffic. Its consumer is the live-query fan-out in
	// documents.go, an ephemeral subscriber beside the hub that delivers result
	// sets to IPC callers over their own connections.
	FactDocumentChanged = "document.changed"
	// FactDocumentCollectionRemoved: a document collection was undefined, taking
	// every document under it. Subject is the collection (namespace/collection),
	// because that is the entity that went — a removal has no document to name,
	// and saying so with an empty document id would leave every future consumer
	// special-casing an address that points at nothing.
	FactDocumentCollectionRemoved = "document.collection.removed"
)

// compactableFacts are the fact classes the retention pass may reduce to one
// row per subject. Both document facts qualify for the same reason: they are
// invalidations about a subject whose state lives in the store, so only the
// newest carries information. Session and ticket facts are deliberately absent
// — they keep the age window's behavior unchanged.
var compactableFacts = []string{FactDocumentChanged, FactDocumentCollectionRemoved}

// projection maps facts to the wire traffic they produce.
type projection struct {
	filter bus.Filter
	apply  func(*Daemon, bus.Event)
}

// wireProjections is the whole fact -> WebSocket mapping. A2 grows this table as
// each broadcaster migrates.
//
// Built once on first use rather than at package init: the table's closures call
// projections, projections publish further facts (a session state change
// recomputes its workspace), and publishing reads the table — a cycle the
// compiler rejects in a package-level initializer even though it is fine at
// runtime.
var (
	wireProjectionsOnce  sync.Once
	wireProjectionsTable []projection
)

func wireProjections() []projection {
	wireProjectionsOnce.Do(func() { wireProjectionsTable = buildWireProjections() })
	return wireProjectionsTable
}

func buildWireProjections() []projection {
	return []projection{
		{
			filter: bus.Filter{FactSessionStateChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionStateChanged(ev.Subject) },
		},
		{
			filter: bus.Filter{FactSessionRegistered},
			apply: func(d *Daemon, ev bus.Event) {
				d.projectSessionEvent(protocol.EventSessionRegistered, ev.Subject)
			},
		},
		{
			// A pin changes what the session says about itself (turn_owed and
			// pinned_at), and nothing about its workspace, so it re-pushes the one
			// session rather than recomputing the group around it.
			filter: bus.Filter{FactSessionPinChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionStateChanged(ev.Subject) },
		},
		{
			// A re-announced session and a renamed one both reach clients as a state
			// change carrying the session; neither recomputes the workspace, which is
			// what separates them from FactSessionStateChanged.
			filter: bus.Filter{FactSessionReregistered, FactSessionRenamed},
			apply: func(d *Daemon, ev bus.Event) {
				d.projectSessionEvent(protocol.EventSessionStateChanged, ev.Subject)
			},
		},
		{
			filter: bus.Filter{FactSessionTodosChanged},
			apply: func(d *Daemon, ev bus.Event) {
				d.projectSessionEvent(protocol.EventSessionTodosUpdated, ev.Subject)
			},
		},
		{
			filter: bus.Filter{FactSessionUnregistered},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionUnregistered(ev) },
		},
		{
			filter: bus.Filter{FactSessionRespawned},
			apply: func(d *Daemon, ev bus.Event) {
				d.wsHub.Broadcast(&protocol.WebSocketEvent{
					Event: protocol.EventRuntimeRespawned,
					ID:    protocol.Ptr(ev.Subject),
				})
			},
		},
		{
			filter: bus.Filter{FactSessionPTYResized},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionPTYResized(ev) },
		},
		{
			// Everything whose only client-visible effect is "the session list moved".
			// Each is a real fact about one session; the wire sees one list push.
			filter: bus.Filter{
				FactSessionTerminated,
				FactSessionBranchChanged,
				FactSessionChiefRoleChanged,
				FactSessionReconciled,
				FactWorktreeSessionsRemoved,
				FactEndpointSessionsChanged,
			},
			apply: func(d *Daemon, _ bus.Event) { d.projectSessionsUpdated() },
		},
		{
			filter: bus.Filter{FactWorkspaceRegistered},
			apply: func(d *Daemon, ev bus.Event) {
				d.projectWorkspaceEvent(protocol.EventWorkspaceRegistered, ev.Subject)
			},
		},
		{
			// Eight distinct facts, one wire event: clients have always been told
			// "this workspace changed, here it is" whatever the reason.
			filter: bus.Filter{
				FactWorkspaceReregistered,
				FactWorkspaceRenamed,
				FactWorkspaceStatusChanged,
				FactWorkspaceMuteChanged,
				FactWorkspacePinChanged,
				FactWorkspaceRankChanged,
				FactWorkspaceSessionAssociated,
				FactWorkspaceSessionDissociated,
			},
			apply: func(d *Daemon, ev bus.Event) {
				d.projectWorkspaceEvent(protocol.EventWorkspaceStateChanged, ev.Subject)
			},
		},
		{
			filter: bus.Filter{FactWorkspaceUnregistered},
			apply:  func(d *Daemon, ev bus.Event) { d.projectWorkspaceUnregistered(ev) },
		},
		{
			filter: bus.Filter{FactWorkspaceLayoutChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectWorkspaceLayoutChanged(ev) },
		},
		{
			filter: bus.Filter{FactWorkspaceLayoutRepublished},
			apply:  func(d *Daemon, ev bus.Event) { d.projectWorkspaceLayoutRepublished(ev.Subject) },
		},
		{
			filter: bus.Filter{FactWorkspaceContextChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectWorkspaceContextChanged(ev) },
		},
		{
			// Every ticket fact re-pushes the board. The board push is the wire
			// shape clients already expect; the facts are what a consumer can
			// actually reason about.
			filter: bus.Filter{"ticket.*"},
			apply:  func(d *Daemon, _ bus.Event) { d.projectTicketsUpdated() },
		},
		{
			filter: bus.Filter{"pr.*"},
			apply:  func(d *Daemon, _ bus.Event) { d.projectPRsUpdated() },
		},
		{
			filter: bus.Filter{FactRepoMuteChanged},
			apply:  func(d *Daemon, _ bus.Event) { d.projectRepoStatesUpdated() },
		},
		{
			filter: bus.Filter{FactAuthorMuteChanged},
			apply:  func(d *Daemon, _ bus.Event) { d.projectAuthorStatesUpdated() },
		},
		{
			filter: bus.Filter{FactSessionPTYExited},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionPTYExited(ev) },
		},
		{
			filter: bus.Filter{FactSessionWorkspaceChanged},
			apply: func(d *Daemon, ev bus.Event) {
				d.projectSessionEvent(protocol.EventSessionStateChanged, ev.Subject)
			},
		},
		{
			filter: bus.Filter{FactWorktreeCreated},
			apply:  func(d *Daemon, ev bus.Event) { d.projectWorktreeCreated(ev) },
		},
		{
			filter: bus.Filter{FactWorktreeDeleted},
			apply:  func(d *Daemon, ev bus.Event) { d.projectWorktreeDeleted(ev) },
		},
		{
			filter: bus.Filter{FactWorktreeListReconciled},
			apply:  func(d *Daemon, ev bus.Event) { d.projectWorktreesUpdated(ev) },
		},
		{
			filter: bus.Filter{FactGitOperationStarted, FactGitOperationFinished},
			apply:  func(d *Daemon, ev bus.Event) { d.projectGitOperation(ev) },
		},
		{
			filter: bus.Filter{FactRateLimited},
			apply:  func(d *Daemon, ev bus.Event) { d.projectRateLimited(ev) },
		},
		{
			filter: bus.Filter{"github.host.*"},
			apply:  func(d *Daemon, _ bus.Event) { d.projectGitHubHostsUpdated() },
		},
		{
			filter: bus.Filter{FactEndpointAdded, FactEndpointRemoved, FactEndpointChanged},
			apply:  func(d *Daemon, _ bus.Event) { d.projectEndpointsUpdated() },
		},
		{
			filter: bus.Filter{FactEndpointStatusChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectEndpointStatusChanged(ev) },
		},
		{
			filter: bus.Filter{
				FactPluginInstalled, FactPluginUninstalled, FactPluginPriorityChanged,
				FactPluginConnected, FactPluginDisconnected, FactPluginHealthChanged,
			},
			apply: func(d *Daemon, _ bus.Event) { d.projectPluginsUpdated() },
		},
		{
			// A plugin going away and a driver registering both change which agents
			// are available, so both re-push settings — without a changed key,
			// because no setting was set.
			filter: bus.Filter{FactPluginDisconnected, FactPluginDriverRegistered,
				FactBackupWritten, FactTailscaleServeChanged},
			apply: func(d *Daemon, _ bus.Event) { d.projectSettingsUpdated("") },
		},
		{
			filter: bus.Filter{FactSettingChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSettingsUpdated(ev.Subject) },
		},
		{
			filter: bus.Filter{"notification.*"},
			apply:  func(d *Daemon, _ bus.Event) { d.projectNotificationsUpdated() },
		},
		{
			filter: bus.Filter{FactAutomationChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectAutomationsChanged(ev.Subject) },
		},
		{
			filter: bus.Filter{FactWorkflowRunUpdated},
			apply:  func(d *Daemon, ev bus.Event) { d.projectWorkflowRunUpdated(ev) },
		},
		{
			filter: bus.Filter{FactTaskChanged},
			apply:  func(d *Daemon, _ bus.Event) { d.projectTasksChanged() },
		},
		{
			filter: bus.Filter{FactNotebookFileChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectNotebookChanged(ev) },
		},
		{
			filter: bus.Filter{FactPresentationAdded, FactPresentationUpdated},
			apply:  func(d *Daemon, ev bus.Event) { d.projectPresentation(ev) },
		},
	}
}

// ensureEventBus constructs the bus over the profile store and registers the hub
// as an ephemeral consumer. It is idempotent and runs at construction rather than
// at Start, because the hub subscription is what makes a published fact reach
// clients: a daemon that publishes without having started (every test that
// exercises a broadcaster directly) must still project.
func (d *Daemon) ensureEventBus() {
	if d.eventBus != nil {
		return
	}
	var backing bus.Store
	if d.store != nil {
		backing = d.newSQLBusStore()
	}
	d.eventBus = bus.New(bus.Options{Store: backing, Log: d.logf, Compactable: compactableFacts})
	d.busUnsubscribe = d.eventBus.Subscribe(bus.All, d.projectToClients)
	d.subscribeDocumentFacts()
}

// startEventBus begins durable delivery. It runs early in Start, before any
// subsystem that publishes.
func (d *Daemon) startEventBus() error {
	d.ensureEventBus()
	return d.eventBus.Start()
}

func (d *Daemon) stopEventBus() {
	d.unsubscribeDocumentFacts()
	if d.busUnsubscribe != nil {
		d.busUnsubscribe()
		d.busUnsubscribe = nil
	}
	if d.eventBus != nil {
		d.eventBus.Stop()
	}
}

// projectToClients is the hub's ephemeral handler.
func (d *Daemon) projectToClients(ev bus.Event) {
	for _, p := range wireProjections() {
		if p.filter.Matches(ev.Name) {
			p.apply(d, ev)
		}
	}
}

// publishFact is the producer half. It is nil-safe: a Daemon assembled in a test
// without a bus still runs its projections directly, so migrating a broadcaster
// does not silently disconnect the behavior every existing test asserts on.
//
// The bus-less path marshals the payload exactly as Publish would, because a
// projection that reads its payload must behave the same either way — otherwise
// a fact whose data lives in the payload rather than in the store would project
// an empty message in every test that skips the bus.
func (d *Daemon) publishFact(name, subject string, payload any) {
	if d == nil {
		return
	}
	if d.eventBus == nil {
		ev := bus.Event{Name: name, Subject: subject}
		if payload != nil {
			raw, err := json.Marshal(payload)
			if err != nil {
				d.logf("bus: marshaling payload for %s (%s): %v", name, subject, err)
				return
			}
			ev.Payload = raw
		}
		d.projectToClients(ev)
		return
	}
	if _, err := d.eventBus.Publish(name, subject, payload); err != nil {
		d.logf("bus: publishing %s (%s): %v", name, subject, err)
	}
}

// decodeFact reads a fact's payload, reporting a decode failure rather than
// projecting a half-built message from it.
func decodeFact[T any](d *Daemon, ev bus.Event) (T, bool) {
	var out T
	if err := ev.Decode(&out); err != nil {
		d.logf("bus: decoding %s payload for %s: %v", ev.Name, ev.Subject, err)
		return out, false
	}
	return out, true
}

// Snapshot projections re-push a whole list — every session, every ticket. A
// bulk operation publishes one fact per entity, because that is what makes the
// log worth subscribing to, but the wire must not carry one full list per
// entity: clearing twenty sessions was one message before the bus and has to
// stay one message after it.
//
// coalesceSnapshots runs fn with snapshot projections deferred and de-duplicated
// by key, then flushes each one once, in first-request order. Per-entity
// projections (the ones that name the entity on the wire) are unaffected and
// still fire inline, in publish order.
//
// The window is process-wide rather than goroutine-scoped, so a snapshot pushed
// by an unrelated goroutine during a bulk operation is deferred to the flush
// too. That is a few microseconds of delay for one message, and it is still
// delivered exactly once, in order relative to the batch.
func (d *Daemon) coalesceSnapshots(fn func()) {
	d.snapshotMu.Lock()
	d.snapshotDepth++
	d.snapshotMu.Unlock()

	defer func() {
		d.snapshotMu.Lock()
		d.snapshotDepth--
		if d.snapshotDepth > 0 {
			d.snapshotMu.Unlock()
			return
		}
		pending := d.pendingSnapshots
		order := d.pendingSnapshotOrder
		d.pendingSnapshots = nil
		d.pendingSnapshotOrder = nil
		d.snapshotMu.Unlock()

		for _, key := range order {
			pending[key]()
		}
	}()

	fn()
}

// projectSnapshot runs a whole-list re-push, or records it for the end of the
// enclosing coalesceSnapshots window. Every projection that re-pushes a list
// goes through this rather than broadcasting directly.
func (d *Daemon) projectSnapshot(key string, push func()) {
	d.snapshotMu.Lock()
	if d.snapshotDepth == 0 {
		d.snapshotMu.Unlock()
		push()
		return
	}
	if d.pendingSnapshots == nil {
		d.pendingSnapshots = map[string]func(){}
	}
	if _, seen := d.pendingSnapshots[key]; !seen {
		d.pendingSnapshotOrder = append(d.pendingSnapshotOrder, key)
	}
	d.pendingSnapshots[key] = push
	d.snapshotMu.Unlock()
}

// Snapshot keys. One per whole-list wire message.
const (
	snapshotSessions    = "sessions_updated"
	snapshotTickets     = "tickets_updated"
	snapshotPRs         = "prs_updated"
	snapshotRepos       = "repos_updated"
	snapshotAuthors     = "authors_updated"
	snapshotGHHosts     = "github_hosts_updated"
	snapshotEndpoints   = "endpoints_updated"
	snapshotPlugins     = "plugins_updated"
	snapshotSettings    = "settings_updated"
	snapshotNotifs      = "notifications_updated"
	snapshotAutomations = "automations_changed"
	snapshotTasks       = "tasks_changed"
)

// wireEqual reports whether two values reach clients as the same JSON. It is the
// equality a diff-driven producer wants: "changed" should mean the client would
// see something different, not that some unexported or unserialized field moved.
func wireEqual(a, b any) bool {
	rawA, errA := json.Marshal(a)
	rawB, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return bytes.Equal(rawA, rawB)
}

// BusStatus snapshots the bus for operator inspection.
func (d *Daemon) BusStatus() (bus.Status, error) {
	if d.eventBus == nil {
		return bus.Status{}, nil
	}
	return d.eventBus.Status()
}
