package daemon

import (
	"bytes"
	"encoding/json"
	"sync"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
)

// The daemon's side of the event bus: the fact vocabulary and the projection
// table that turns facts into what WebSocket clients see.
// See docs/plans/2026-08-01-ext-a1-event-bus.md.
//
// Every fact carries a subject; a subject-less "the list changed" fact is the
// snapshot invalidation this design exists to avoid. The hub runs matching
// projections inline on the publishing goroutine.
//
// A projection writes to the wire and does nothing else: it must not mutate
// state and must not publish — the bus holds its publish lock across the inline
// fan-out, so a nested publish deadlocks.
//
// Byte streams (PTY output/desync, attach, tile content), fs bursts, and the
// remote relay stay off the bus; TestWireTrafficComesFromProjections enforces
// the enumerated exception list.

// Fact names are dotted `domain.verb`. `ext.<extension>.*` is reserved for facts
// published by extensions.
const (
	// Session facts; subject is the session id. Registered (first appearance) and
	// reregistered (re-announcement) produce different wire events.
	FactSessionRegistered   = "session.registered"
	FactSessionReregistered = "session.reregistered"
	FactSessionStateChanged = "session.state.changed"
	FactSessionRenamed      = "session.renamed"
	FactSessionUnregistered = "session.unregistered"
	FactSessionTodosChanged = "session.todos.changed"
	// FactSessionAssistantWindowChanged: the canonical annotatable window for
	// this session changed. It is a pure invalidation and compactable by subject.
	FactSessionAssistantWindowChanged = "session.assistant_window.changed"
	FactSessionRespawned              = "session.respawned"
	FactSessionPTYResized             = "session.pty.resized"
	// FactSessionTerminated: a session going away in a bulk operation. Not
	// FactSessionUnregistered — these paths only ever re-push the list.
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
	// FactSessionPinChanged: pinned out of the queue or released back. Distinct
	// from FactWorkspacePinChanged, which moves a whole workspace.
	FactSessionPinChanged = "session.pin.changed"
	// FactSessionCapChanged: context-window cap pin set or cleared.
	FactSessionCapChanged = "session.cap.changed"
	// FactSessionActivityChanged: the generator wrote a new activity line for
	// this session. Subject-only — the projection re-reads the session, which is
	// correct because the store was written before the publish and the bus fans
	// out inline.
	FactSessionActivityChanged = "session.activity.changed"
	// FactSessionCostChanged: durable token usage changed, or a price override
	// repriced it. The projection derives the current USD value onto the session.
	FactSessionCostChanged = "session.cost.changed"
	// FactSessionConversationChanged: the provider-owned conversation hosted by
	// this stable attn session changed. The store already carries the new binding;
	// the small payload is only the exact live transcript path reported by the
	// SessionStart hook.
	FactSessionConversationChanged = "session.conversation.changed"

	// FactWorktreeSessionsRemoved: deleting this worktree took its sessions with
	// it. Subject is the worktree path.
	FactWorktreeSessionsRemoved = "worktree.sessions.removed"

	// FactEndpointSessionsChanged: a remote endpoint's session set changed;
	// subject is the endpoint id.
	FactEndpointSessionsChanged = "endpoint.sessions.changed"

	// Workspace facts; subject is the workspace id. Eight project to one wire event
	// but stay separate facts — collapsing them re-creates the diffing problem.
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

	// PR facts; subject is the PR id. The three set facts come from diffing the
	// list around a bulk refresh, which replaces the whole set.
	FactPRAppeared    = "pr.appeared"
	FactPRUpdated     = "pr.updated"
	FactPRDisappeared = "pr.disappeared"
	// The rest name a single PR the user or the daemon acted on directly.
	FactPRMuteChanged    = "pr.mute.changed"
	FactPRVisited        = "pr.visited"
	FactPRHeatChanged    = "pr.heat.changed"
	FactPRDetailsChanged = "pr.details.changed"

	// Worktree facts. Subject is the worktree path, except the reconcile, whose
	// subject is the main repo (the resulting list is per repo).
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
	// FactPluginDriverRegistered changes which agents are available (settings),
	// so it is the one plugin fact that does not re-push the plugin list.
	FactPluginDriverRegistered = "plugin.driver.registered"

	// FactSettingChanged: subject is the setting key.
	FactSettingChanged = "setting.changed"
	// FactBackupWritten: subject is the backup file path; clients learn of it
	// through settings (db.last_backup_at).
	FactBackupWritten = "backup.written"
	// FactTailscaleServeChanged: subject is the profile (one serve per daemon,
	// one daemon per profile).
	FactTailscaleServeChanged = "tailscale.serve.changed"

	// Notification facts; subject is the notification id.
	FactNotificationCreated = "notification.created"
	FactNotificationRead    = "notification.read"

	// FactAutoModeDenied: a pi session refused a tool call under auto mode.
	// Subject is the session it happened in — the entity a denial is about. The
	// denial itself is already in the store when this is published, and the
	// notification that surfaces it with it, which is why the projection is the
	// notification push and there is no second notification.created fact.
	FactAutoModeDenied = "automode.denied"

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
	// FactTicketChanged is the fallback when the call site cannot name a sharper
	// fact; still about one ticket, never a list invalidation.
	FactTicketChanged = "ticket.changed"

	// FactDocumentChanged: subject is the address (namespace/collection/id).
	// Appended inside the write's transaction (CommitDocumentWrite), so its seq is
	// the write's position. No wireProjections entry: its consumer is the
	// live-query fan-out in documents.go, not WebSocket clients.
	FactDocumentChanged = "document.changed"
	// FactDocumentCollectionRemoved: a collection was undefined, taking every
	// document under it. Subject is the collection — a removal has no document
	// to name.
	FactDocumentCollectionRemoved = "document.collection.removed"
	// FactDocumentCollectionRedeclared: subject is the collection. A redeclare that
	// drops a queried field must end the subscriptions using it. No wire entry.
	FactDocumentCollectionRedeclared = "document.collection.redeclared"

	// FactGardenPlanted: a seed exists. Subject is the seed id — every garden
	// fact names its seed, which is what lets a future sync engine be nothing but
	// a durable consumer with a cursor that re-reads the document. Published
	// after the write it describes has committed, so a consumer that reads it
	// always finds the seed.
	FactGardenPlanted = "garden.planted"
	// FactGardenBodyEdited: a seed's living markdown document changed. The
	// garden snapshot carries the new body and revision so open readers can
	// re-anchor immediately without waiting on a detail refetch.
	FactGardenBodyEdited = "garden.body_edited"
	// One fact per lifecycle move, rather than one `garden.changed`: the subject
	// says which seed and the name says what happened to it, which is what a
	// change feed and a future nudge both read. All of them project the same way
	// — the panel re-renders a list — but a consumer that cares only about
	// harvests must not have to diff documents to find them.
	FactGardenTended    = "garden.tended"
	FactGardenParked    = "garden.parked"
	FactGardenHarvested = "garden.harvested"
	FactGardenWithered  = "garden.withered"
	FactGardenReplanted = "garden.replanted"
	// FactGardenNoted: a note was appended to a seed's log. Subject is the
	// seed, not the note — the log is the seed's memory of itself, and the
	// entity anybody reads is the seed.
	FactGardenNoted = "garden.noted"
	// FactGardenLinked/FactGardenUnlinked: an edge was added to or removed from
	// a seed. Subject is the seed the edge is stored on, which is the document
	// that changed; the seed at the other end is read from that document.
	FactGardenLinked   = "garden.linked"
	FactGardenUnlinked = "garden.unlinked"

	// Crew facts; subject is the member id. The roster is what a client draws —
	// every member, awake or asleep — so all three project the same whole-list
	// push, and the name says which of the three things moved: a home became a
	// member, a member's day started or ended, or its registry fields changed.
	FactCrewRegistered = "crew.registered"
	FactCrewBound      = "crew.bound"
	FactCrewReleased   = "crew.released"
	FactCrewUpdated    = "crew.updated"

	// App registry facts; subject is the app's name.
	//
	// Neither has an entry in wireProjections, and that is not an omission: the
	// app registry has no UI surface, so these produce no WebSocket traffic. They
	// exist because the runtime has to hear about a state change it did not make
	// — an app disabled from the CLI, or by the auto-disable clock, has to reach
	// the loop that dispatches its handlers, and the log is how one part of the
	// daemon tells another something happened.
	//
	// FactAppEnabledChanged: the app's bus consumer bit was flipped. The payload
	// carries which way, so a consumer of this fact does not have to read back a
	// bit that may already have moved again.
	FactAppEnabledChanged = "app.enabled.changed"
	// FactAppRemoved: the app was uninstalled — consumer stopped and deleted,
	// registry row gone. It carries a payload rather than only a subject because
	// the entity it describes no longer exists to be read.
	FactAppRemoved = "app.removed"
	// FactAppVersionChanged: the app now points at a different version. One fact
	// for both ways of getting there — an apply and a rollback are the same
	// pointer move, and a runtime that reloads on one must reload on the other.
	// The payload names the version moved to and the one moved from, so a
	// consumer that has to drain the outgoing version's handlers knows which one
	// that is without racing the pointer it would otherwise read back.
	FactAppVersionChanged = "app.version.changed"
	// FactAppRuntimeChanged: the shared app runtime's supervision state moved —
	// started, connected, backing off, parked, stopped. Subject is the runtime's
	// child name rather than an app's, because the entity that moved is the one
	// process every app shares. It carries no payload: the state is the
	// supervisor's and a reader asks it (`attn app runtime status`) rather than
	// trusting a copy that was true when the fact was written.
	FactAppRuntimeChanged = "app.runtime.changed"
)

// CompactableFacts are the fact classes retention may reduce to one row per
// subject — invalidations where only the newest carries information. Document
// facts re-read the store; the assistant-window fact reads the current watcher
// snapshot. Historical session and ticket facts are deliberately absent.
//
// The three loudest classes in a real log are session.state.changed (74%),
// pr.updated (17%) and plugin.health.changed. All three are subject-only with a
// nil payload and project to a store re-read, so on delivery semantics alone
// they qualify: a consumer that missed four of five is not missing anything the
// fifth does not carry. They stay out anyway, because compaction would delete
// the created_at history `attn bus status` computes producer rates from —
// exactly the evidence that catches a producer flapping. Compaction bounds the
// log by the data it describes; these classes are the ones whose write rate is
// itself the signal. Bound them by fixing the producer, not by hiding the rows.
var CompactableFacts = []string{FactDocumentChanged, FactDocumentCollectionRemoved, FactDocumentCollectionRedeclared, FactSessionAssistantWindowChanged}

// projection maps facts to the wire traffic they produce.
type projection struct {
	filter bus.Filter
	apply  func(*Daemon, bus.Event)
}

// wireProjections is the whole fact -> WebSocket mapping. Built on first use,
// not package init: projections publish further facts and publishing reads the
// table — a cycle the compiler rejects in a package-level initializer.
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
				// A new session can reactivate a stored tender with the same id.
				d.projectGardenSeeds()
			},
		},
		{
			// A pin changes only what the session says about itself.
			filter: bus.Filter{FactSessionPinChanged, FactSessionCapChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionStateChanged(ev.Subject) },
		},
		{
			// An activity line has no event of its own: it rides on the session
			// snapshot, so it re-pushes that session alone.
			filter: bus.Filter{FactSessionActivityChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionStateChanged(ev.Subject) },
		},
		{
			filter: bus.Filter{FactSessionCostChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionStateChanged(ev.Subject) },
		},
		{
			filter: bus.Filter{FactSessionConversationChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionStateChanged(ev.Subject) },
		},
		{
			// Neither recomputes the workspace, unlike FactSessionStateChanged.
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
			filter: bus.Filter{FactSessionAssistantWindowChanged},
			apply: func(d *Daemon, ev bus.Event) {
				d.wsHub.BroadcastValue(&protocol.SessionMessagesChangedMessage{
					Event: protocol.EventSessionMessagesChanged, SessionID: ev.Subject,
				})
			},
		},
		{
			filter: bus.Filter{FactSessionUnregistered},
			apply: func(d *Daemon, ev bus.Event) {
				d.projectSessionUnregistered(ev)
				// Session liveness is part of garden truth: it decides whether a
				// stored tender still holds and therefore where feedback routes.
				d.projectGardenSeeds()
			},
		},
		{
			filter: bus.Filter{FactSessionRespawned},
			apply: func(d *Daemon, ev bus.Event) {
				d.wsHub.Broadcast(&protocol.WebSocketEvent{
					Event: protocol.EventRuntimeRespawned,
					ID:    protocol.Ptr(ev.Subject),
				})
				// Keep the computed tender hold in lockstep with session rebirth.
				d.projectGardenSeeds()
			},
		},
		{
			filter: bus.Filter{FactSessionPTYResized},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionPTYResized(ev) },
		},
		{
			// Facts whose only client-visible effect is one session-list push.
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
			// Every ticket fact re-pushes the board — the wire shape clients expect.
			filter: bus.Filter{"ticket.*"},
			apply:  func(d *Daemon, _ bus.Event) { d.projectTicketsUpdated() },
		},
		{
			// Every garden fact re-pushes the garden; the panel renders a list.
			filter: bus.Filter{"garden.*"},
			apply:  func(d *Daemon, _ bus.Event) { d.projectGardenSeeds() },
		},
		{
			// Every crew fact re-pushes the roster; the sidebar renders a list.
			filter: bus.Filter{"crew.*"},
			apply:  func(d *Daemon, _ bus.Event) { d.projectCrewRoster() },
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
			// All change which agents are available, so re-push settings — with no
			// changed key, because no setting was set.
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
			filter: bus.Filter{FactAutoModeDenied},
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

// ensureEventBus constructs the bus and registers the hub as an ephemeral
// consumer. Idempotent, and run at construction: a daemon that publishes without
// having started (tests) must still project.
func (d *Daemon) ensureEventBus() {
	if d.eventBus != nil {
		return
	}
	var backing bus.Store
	if d.store != nil {
		backing = d.newSQLBusStore()
	}
	d.eventBus = bus.New(bus.Options{
		Store:       backing,
		Log:         d.logf,
		Compactable: CompactableFacts,
		PinAlarmAge: d.busPinAlarmAge(),
	})
	d.busUnsubscribe = d.eventBus.Subscribe(bus.All, d.projectToClients)
	d.subscribeDocumentFacts()
	d.subscribeAgentConversationFacts()
}

// startEventBus begins durable delivery; runs early in Start, before any
// subsystem that publishes.
func (d *Daemon) startEventBus() error {
	d.ensureEventBus()
	return d.eventBus.Start()
}

func (d *Daemon) stopEventBus() {
	d.unsubscribeDocumentFacts()
	d.unsubscribeAgentConversationFacts()
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

// publishFact is the producer half. Nil-safe: a bus-less test Daemon runs its
// projections directly, marshalling the payload exactly as Publish would.
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

// coalesceSnapshots runs fn with snapshot (whole-list) projections deferred and
// de-duplicated by key, so a bulk operation does not put one full list per entity
// on the wire. Per-entity projections still fire inline. The window is
// process-wide: an unrelated goroutine's snapshot defers to the flush too.
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

// projectSnapshot runs a whole-list re-push, or defers it to the end of the
// enclosing coalesceSnapshots window. Every list re-push goes through this.
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
	snapshotGarden      = "garden_seeds_updated"
	snapshotCrew        = "crew_updated"
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

// wireEqual reports whether two values reach clients as the same JSON —
// "changed" means the client would see something different.
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
