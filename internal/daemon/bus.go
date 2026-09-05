package daemon

import (
	"bytes"
	"encoding/json"
	"sync"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
)

// A projection writes to the wire and does nothing else: the bus holds its publish lock
// across the inline fan-out, so a nested publish deadlocks.

const (
	FactSessionRegistered             = "session.registered"
	FactSessionReregistered           = "session.reregistered"
	FactSessionStateChanged           = "session.state.changed"
	FactSessionModelRequestStarted    = "session.model_request.started"
	FactSessionRenamed                = "session.renamed"
	FactSessionUnregistered           = "session.unregistered"
	FactSessionClosed                 = "session.closed"
	FactSessionTodosChanged           = "session.todos.changed"
	FactSessionAssistantWindowChanged = "session.assistant_window.changed"
	FactSessionRespawned              = "session.respawned"
	FactSessionPTYResized             = "session.pty.resized"
	FactSessionTerminated             = "session.terminated"
	FactSessionBranchChanged          = "session.branch.changed"
	FactSessionChiefRoleChanged       = "session.chief_role.changed"
	FactSessionReconciled             = "session.reconciled"
	FactSessionPTYExited              = "session.pty.exited"
	FactSessionWorkspaceChanged       = "session.workspace.changed"
	FactSessionPinChanged             = "session.pin.changed"
	FactSessionCapChanged             = "session.cap.changed"
	FactSessionActivityChanged        = "session.activity.changed"
	FactSessionCostChanged            = "session.cost.changed"
	FactSessionTerminalBuildChanged   = "session.terminal_build.changed"
	FactSessionConversationChanged    = "session.conversation.changed"
	FactSessionPullRequestChanged     = "session.pull_request.changed"

	FactWorktreeSessionsRemoved = "worktree.sessions.removed"

	FactEndpointSessionsChanged = "endpoint.sessions.changed"

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

	FactPRAppeared       = "pr.appeared"
	FactPRUpdated        = "pr.updated"
	FactPRDisappeared    = "pr.disappeared"
	FactPRMuteChanged    = "pr.mute.changed"
	FactPRVisited        = "pr.visited"
	FactPRHeatChanged    = "pr.heat.changed"
	FactPRDetailsChanged = "pr.details.changed"

	FactWorktreeCreated        = "worktree.created"
	FactWorktreeDeleted        = "worktree.deleted"
	FactWorktreeListReconciled = "worktree.list.reconciled"

	FactGitOperationStarted  = "git.operation.started"
	FactGitOperationFinished = "git.operation.finished"

	FactRateLimited = "ratelimit.hit"

	FactGitHubHostAdded   = "github.host.added"
	FactGitHubHostRemoved = "github.host.removed"

	FactRepoMuteChanged   = "repo.mute.changed"
	FactAuthorMuteChanged = "author.mute.changed"

	FactEndpointAdded         = "endpoint.added"
	FactEndpointRemoved       = "endpoint.removed"
	FactEndpointChanged       = "endpoint.changed"
	FactEndpointStatusChanged = "endpoint.status.changed"

	FactPluginInstalled        = "plugin.installed"
	FactPluginUninstalled      = "plugin.uninstalled"
	FactPluginPriorityChanged  = "plugin.priority.changed"
	FactPluginConnected        = "plugin.connected"
	FactPluginDisconnected     = "plugin.disconnected"
	FactPluginHealthChanged    = "plugin.health.changed"
	FactPluginDriverRegistered = "plugin.driver.registered"

	FactSettingChanged        = "setting.changed"
	FactBackupWritten         = "backup.written"
	FactTailscaleServeChanged = "tailscale.serve.changed"

	FactNotificationCreated = "notification.created"
	FactNotificationRead    = "notification.read"

	FactAutoModeDenied = "automode.denied"

	FactAutoModeConfigChanged = "automode.config.changed"

	FactAutomationChanged  = "automation.changed"
	FactWorkflowRunUpdated = "workflow.run.updated"
	FactTaskChanged        = "task.changed"

	FactNotebookFileChanged = "notebook.file.changed"

	FactPresentationAdded   = "presentation.added"
	FactPresentationUpdated = "presentation.updated"

	FactTicketCreated       = "ticket.created"
	FactTicketStatusChanged = "ticket.status_changed"
	FactTicketCommented     = "ticket.commented"
	FactTicketAssigned      = "ticket.assigned"
	FactTicketAttached      = "ticket.attached"
	FactTicketChanged       = "ticket.changed"

	FactDocumentChanged              = "document.changed"
	FactDocumentCollectionRemoved    = "document.collection.removed"
	FactDocumentCollectionRedeclared = "document.collection.redeclared"

	FactGardenPlanted               = "garden.planted"
	FactGardenBodyEdited            = "garden.body_edited"
	FactGardenResumeIdentityChanged = "garden.resume_identity_changed"
	FactGardenTended                = "garden.tended"
	FactGardenParked                = "garden.parked"
	FactGardenHarvested             = "garden.harvested"
	FactGardenWithered              = "garden.withered"
	FactGardenReplanted             = "garden.replanted"
	FactGardenNoted                 = "garden.noted"
	FactGardenArtifactChanged       = "garden.artifact.changed"
	FactGardenLinked                = "garden.linked"
	FactGardenUnlinked              = "garden.unlinked"
	FactGardenReviewChanged         = "garden.review.changed"
	FactGardenHarvestWhenChanged    = "garden.harvest_when.changed"

	FactCrewRegistered = "crew.registered"
	FactCrewBound      = "crew.bound"
	FactCrewReleased   = "crew.released"
	FactCrewUpdated    = "crew.updated"

	FactAppEnabledChanged = "app.enabled.changed"
	FactAppRemoved        = "app.removed"
	FactAppVersionChanged = "app.version.changed"
	FactAppRuntimeChanged = "app.runtime.changed"
)

var CompactableFacts = []string{FactDocumentChanged, FactDocumentCollectionRemoved, FactDocumentCollectionRedeclared, FactSessionAssistantWindowChanged}

type projection struct {
	filter bus.Filter
	apply  func(*Daemon, bus.Event)
}

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
				d.projectGardenSeeds()
			},
		},
		{
			filter: bus.Filter{FactSessionPinChanged, FactSessionCapChanged, FactSessionModelRequestStarted},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionStateChanged(ev.Subject) },
		},
		{
			filter: bus.Filter{FactSessionActivityChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionStateChanged(ev.Subject) },
		},
		{
			filter: bus.Filter{FactSessionCostChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionStateChanged(ev.Subject) },
		},
		{
			filter: bus.Filter{FactSessionTerminalBuildChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionStateChanged(ev.Subject) },
		},
		{
			filter: bus.Filter{FactSessionConversationChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionStateChanged(ev.Subject) },
		},
		{
			filter: bus.Filter{FactSessionPullRequestChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionStateChanged(ev.Subject) },
		},
		{
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
				d.projectGardenSeeds()
			},
		},
		{
			filter: bus.Filter{FactSessionPTYResized},
			apply:  func(d *Daemon, ev bus.Event) { d.projectSessionPTYResized(ev) },
		},
		{
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
			filter: bus.Filter{
				FactGardenPlanted, FactGardenBodyEdited, FactGardenResumeIdentityChanged,
				FactGardenTended, FactGardenParked, FactGardenHarvested, FactGardenWithered,
				FactGardenReplanted, FactGardenNoted, FactGardenArtifactChanged,
				FactGardenLinked, FactGardenUnlinked, FactGardenHarvestWhenChanged,
			},
			apply: func(d *Daemon, _ bus.Event) { d.projectGardenSeeds() },
		},
		{
			filter: bus.Filter{FactGardenReviewChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectGardenReview(ev.Subject) },
		},
		{
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
			filter: bus.Filter{FactAutoModeConfigChanged},
			apply:  func(d *Daemon, _ bus.Event) { d.projectAutoModeStateChanged() },
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
			apply: func(d *Daemon, ev bus.Event) {
				d.projectTasksChanged()
				d.projectGardenReviewJob(ev.Subject)
			},
		},
		{
			filter: bus.Filter{FactNotebookFileChanged},
			apply:  func(d *Daemon, ev bus.Event) { d.projectNotebookChanged(ev) },
		},
		{
			filter: bus.Filter{FactPresentationAdded, FactPresentationUpdated},
			apply:  func(d *Daemon, ev bus.Event) { d.projectPresentation(ev) },
		},
		{
			filter: bus.Filter{FactAppVersionChanged, FactAppEnabledChanged, FactAppRemoved},
			apply:  func(d *Daemon, _ bus.Event) { d.projectAppsUpdated() },
		},
	}
}

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
		Retention:   bus.RetentionFromEnv(d.logf),
		PinAlarmAge: d.busPinAlarmAge(),
	})
	d.busUnsubscribe = d.eventBus.Subscribe(bus.All, d.projectToClients)
	d.subscribeDocumentFacts()
	d.subscribeAgentConversationFacts()
	d.subscribeSessionPullRequestFacts()
}

func (d *Daemon) startEventBus() error {
	d.ensureEventBus()
	return d.eventBus.Start()
}

func (d *Daemon) stopEventBus() {
	d.unsubscribeDocumentFacts()
	d.unsubscribeAgentConversationFacts()
	d.unsubscribeSessionPullRequestFacts()
	if d.busUnsubscribe != nil {
		d.busUnsubscribe()
		d.busUnsubscribe = nil
	}
	if d.eventBus != nil {
		d.eventBus.Stop()
	}
}

func (d *Daemon) projectToClients(ev bus.Event) {
	for _, p := range wireProjections() {
		if p.filter.Matches(ev.Name) {
			p.apply(d, ev)
		}
	}
}

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

func decodeFact[T any](d *Daemon, ev bus.Event) (T, bool) {
	var out T
	if err := ev.Decode(&out); err != nil {
		d.logf("bus: decoding %s payload for %s: %v", ev.Name, ev.Subject, err)
		return out, false
	}
	return out, true
}

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

const (
	snapshotSessions    = "sessions_updated"
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
	snapshotApps        = "apps_updated"
	snapshotAutoMode    = "automode_state_changed"
)

const AutoModeConfigSubject = "config"

// wireEqual reports whether two values reach clients as the same JSON —
func wireEqual(a, b any) bool {
	rawA, errA := json.Marshal(a)
	rawB, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return bytes.Equal(rawA, rawB)
}

func (d *Daemon) BusStatus() (bus.Status, error) {
	if d.eventBus == nil {
		return bus.Status{}, nil
	}
	return d.eventBus.Status()
}
