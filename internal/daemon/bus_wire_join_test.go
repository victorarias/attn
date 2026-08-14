package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// The other half of the wire boundary. TestWireTrafficComesFromProjections says
// only a projection may write to the wire; this says every fact with a
// projection actually writes, and writes the events it is meant to.
//
// The defect that was invisible without it: a projection whose feature tests
// assert against a broadcast hook fires the hook, then sends. Delete the send
// and keep the hook and the whole package still passes — measured, by deleting
// the garden snapshot push and running `go test ./internal/daemon` green.
//
// So the observation point here is wsHub.wireTap, which sees the marshalled
// bytes of every send path, and never a hook.

// wireFixture is how one fact is made real: what it is about, what it carries,
// and the complete set of wire events publishing it must produce.
//
// Everything is optional except events. A fact whose projection re-pushes a
// whole list needs no subject and no payload at all, which is most of them.
type wireFixture struct {
	// events is every `event` name the publish must put on the wire, in any
	// order. Exact, not "at least": a second projection entry that matched the
	// same fact by accident would double-push, and that is a defect too.
	events []string
	// subject names the entity the fact is about. Defaults to a subject nothing
	// resolves, which is correct for a snapshot projection and wrong for one
	// that re-reads the entity — and the test says which when it fails.
	subject func(*wireWorld) string
	// payload is the fact's body, for the projections that decode one.
	payload func(*wireWorld) any
}

// wireFixtures is the whole fact -> wire-event contract, and the only place this
// test needs editing. A fact that gains a projection and no entry here fails by
// name; an entry whose fact no longer exists fails as stale.
var wireFixtures = map[string]wireFixture{
	// Sessions. Everything but the unregister re-reads the session the subject
	// names, so the seeded session is the subject.
	FactSessionStateChanged: {
		events:  []string{protocol.EventSessionStateChanged},
		subject: (*wireWorld).session,
	},
	FactSessionRegistered: {
		events:  []string{protocol.EventSessionRegistered},
		subject: (*wireWorld).session,
	},
	FactSessionReregistered: {
		events:  []string{protocol.EventSessionStateChanged},
		subject: (*wireWorld).session,
	},
	FactSessionRenamed: {
		events:  []string{protocol.EventSessionStateChanged},
		subject: (*wireWorld).session,
	},
	FactSessionPinChanged: {
		events:  []string{protocol.EventSessionStateChanged},
		subject: (*wireWorld).session,
	},
	FactSessionCapChanged: {
		events:  []string{protocol.EventSessionStateChanged},
		subject: (*wireWorld).session,
	},
	FactSessionActivityChanged: {
		events:  []string{protocol.EventSessionStateChanged},
		subject: (*wireWorld).session,
	},
	FactSessionConversationChanged: {
		events:  []string{protocol.EventSessionStateChanged},
		subject: (*wireWorld).session,
	},
	// The one session fact that does not re-push the session: the app is told
	// its annotatable window moved and re-reads it with session_messages_get.
	FactSessionAssistantWindowChanged: {
		events:  []string{protocol.EventSessionMessagesChanged},
		subject: (*wireWorld).session,
	},
	FactSessionWorkspaceChanged: {
		events:  []string{protocol.EventSessionStateChanged},
		subject: (*wireWorld).session,
	},
	FactSessionTodosChanged: {
		events:  []string{protocol.EventSessionTodosUpdated},
		subject: (*wireWorld).session,
	},
	FactSessionUnregistered: {
		// The session is gone by then, so it rides in the payload.
		events:  []string{protocol.EventSessionUnregistered},
		subject: (*wireWorld).session,
		payload: func(w *wireWorld) any { return w.d.sessionForBroadcast(w.d.store.Get(w.sessionID)) },
	},
	FactSessionRespawned: {
		events:  []string{protocol.EventRuntimeRespawned},
		subject: (*wireWorld).session,
	},
	FactSessionPTYResized: {
		events:  []string{protocol.EventPtyResized},
		subject: (*wireWorld).session,
		payload: func(*wireWorld) any { return ptyGeometry{Cols: 80, Rows: 24} },
	},
	FactSessionPTYExited: {
		events:  []string{protocol.EventSessionExited},
		subject: (*wireWorld).session,
		payload: func(*wireWorld) any { return ptyExit{ExitCode: 0} },
	},

	// The six facts whose only visible effect is one session-list push.
	FactSessionTerminated:       {events: []string{protocol.EventSessionsUpdated}},
	FactSessionBranchChanged:    {events: []string{protocol.EventSessionsUpdated}},
	FactSessionChiefRoleChanged: {events: []string{protocol.EventSessionsUpdated}},
	FactSessionReconciled:       {events: []string{protocol.EventSessionsUpdated}},
	FactWorktreeSessionsRemoved: {events: []string{protocol.EventSessionsUpdated}},
	FactEndpointSessionsChanged: {events: []string{protocol.EventSessionsUpdated}},

	// Workspaces. The registry is the authority, so the subject must be a
	// workspace it holds.
	FactWorkspaceRegistered: {
		events:  []string{protocol.EventWorkspaceRegistered},
		subject: (*wireWorld).workspace,
	},
	FactWorkspaceReregistered: {
		events:  []string{protocol.EventWorkspaceStateChanged},
		subject: (*wireWorld).workspace,
	},
	FactWorkspaceRenamed: {
		events:  []string{protocol.EventWorkspaceStateChanged},
		subject: (*wireWorld).workspace,
	},
	FactWorkspaceStatusChanged: {
		events:  []string{protocol.EventWorkspaceStateChanged},
		subject: (*wireWorld).workspace,
	},
	FactWorkspaceMuteChanged: {
		events:  []string{protocol.EventWorkspaceStateChanged},
		subject: (*wireWorld).workspace,
	},
	FactWorkspacePinChanged: {
		events:  []string{protocol.EventWorkspaceStateChanged},
		subject: (*wireWorld).workspace,
	},
	FactWorkspaceRankChanged: {
		events:  []string{protocol.EventWorkspaceStateChanged},
		subject: (*wireWorld).workspace,
	},
	FactWorkspaceSessionAssociated: {
		events:  []string{protocol.EventWorkspaceStateChanged},
		subject: (*wireWorld).workspace,
	},
	FactWorkspaceSessionDissociated: {
		events:  []string{protocol.EventWorkspaceStateChanged},
		subject: (*wireWorld).workspace,
	},
	FactWorkspaceUnregistered: {
		events:  []string{protocol.EventWorkspaceUnregistered},
		subject: (*wireWorld).workspace,
		payload: func(w *wireWorld) any {
			snapshot, _ := w.d.workspaces.snapshot(w.workspaceID)
			return snapshot
		},
	},
	FactWorkspaceLayoutChanged: {
		events:  []string{protocol.EventWorkspaceLayoutUpdated},
		subject: (*wireWorld).workspace,
		payload: func(w *wireWorld) any { return w.layout() },
	},
	FactWorkspaceLayoutRepublished: {
		// Nothing changed, so the projection reads the layout back itself.
		events:  []string{protocol.EventWorkspaceLayout},
		subject: (*wireWorld).workspace,
	},
	FactWorkspaceContextChanged: {
		events:  []string{protocol.EventWorkspaceContextChanged},
		subject: (*wireWorld).workspace,
		payload: func(w *wireWorld) any {
			return protocol.WorkspaceContextChangedMessage{
				Event:       protocol.EventWorkspaceContextChanged,
				WorkspaceID: w.workspaceID,
				Revision:    1,
			}
		},
	},

	// Tickets and the garden: every fact re-pushes the whole list.
	FactTicketCreated:       {events: []string{protocol.EventTicketsUpdated}},
	FactTicketStatusChanged: {events: []string{protocol.EventTicketsUpdated}},
	FactTicketCommented:     {events: []string{protocol.EventTicketsUpdated}},
	FactTicketAssigned:      {events: []string{protocol.EventTicketsUpdated}},
	FactTicketAttached:      {events: []string{protocol.EventTicketsUpdated}},
	FactTicketChanged:       {events: []string{protocol.EventTicketsUpdated}},
	FactGardenPlanted:       {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenTended:        {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenParked:        {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenHarvested:     {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenWithered:      {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenReplanted:     {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenNoted:         {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenLinked:        {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenUnlinked:      {events: []string{protocol.EventGardenSeedsUpdated}},

	// PRs and their mute lists.
	FactPRAppeared:       {events: []string{protocol.EventPRsUpdated}},
	FactPRUpdated:        {events: []string{protocol.EventPRsUpdated}},
	FactPRDisappeared:    {events: []string{protocol.EventPRsUpdated}},
	FactPRMuteChanged:    {events: []string{protocol.EventPRsUpdated}},
	FactPRVisited:        {events: []string{protocol.EventPRsUpdated}},
	FactPRHeatChanged:    {events: []string{protocol.EventPRsUpdated}},
	FactPRDetailsChanged: {events: []string{protocol.EventPRsUpdated}},
	FactRepoMuteChanged:  {events: []string{protocol.EventReposUpdated}},
	FactAuthorMuteChanged: {
		events: []string{protocol.EventAuthorsUpdated},
	},

	// Worktrees and git.
	FactWorktreeCreated: {
		events:  []string{protocol.EventWorktreeCreated},
		subject: (*wireWorld).worktree,
		payload: func(w *wireWorld) any { return protocol.Worktree{Path: w.worktreePath} },
	},
	FactWorktreeDeleted: {
		// No payload: the wire event has only ever carried the path, which is
		// the subject.
		events:  []string{protocol.EventWorktreeDeleted},
		subject: (*wireWorld).worktree,
	},
	FactWorktreeListReconciled: {
		events:  []string{protocol.EventWorktreesUpdated},
		subject: (*wireWorld).worktree,
		payload: func(w *wireWorld) any { return []protocol.Worktree{{Path: w.worktreePath}} },
	},
	FactGitOperationStarted: {
		events:  []string{protocol.EventGitOperationStarted},
		subject: func(*wireWorld) string { return "operation-1" },
		payload: func(*wireWorld) any { return gitOperationFixture("operation-1") },
	},
	FactGitOperationFinished: {
		events:  []string{protocol.EventGitOperationFinished},
		subject: func(*wireWorld) string { return "operation-1" },
		payload: func(*wireWorld) any { return gitOperationFixture("operation-1") },
	},

	// GitHub plumbing.
	FactRateLimited: {
		events:  []string{protocol.EventRateLimited},
		subject: func(*wireWorld) string { return "core" },
		payload: func(*wireWorld) any { return rateLimitWindow{ResetAt: "2026-08-12T00:00:00Z"} },
	},
	FactGitHubHostAdded:   {events: []string{protocol.EventGitHubHostsUpdated}},
	FactGitHubHostRemoved: {events: []string{protocol.EventGitHubHostsUpdated}},

	// Endpoints.
	FactEndpointAdded:   {events: []string{protocol.EventEndpointsUpdated}},
	FactEndpointRemoved: {events: []string{protocol.EventEndpointsUpdated}},
	FactEndpointChanged: {events: []string{protocol.EventEndpointsUpdated}},
	FactEndpointStatusChanged: {
		events:  []string{protocol.EventEndpointStatusChanged},
		payload: func(*wireWorld) any { return protocol.EndpointInfo{ID: "endpoint-1", Status: "connected"} },
	},

	// Plugins. Four of them re-push settings too, because they change which
	// agents are available; that second entry is part of the contract.
	FactPluginInstalled:        {events: []string{protocol.EventPluginsUpdated}},
	FactPluginUninstalled:      {events: []string{protocol.EventPluginsUpdated}},
	FactPluginPriorityChanged:  {events: []string{protocol.EventPluginsUpdated}},
	FactPluginConnected:        {events: []string{protocol.EventPluginsUpdated}},
	FactPluginHealthChanged:    {events: []string{protocol.EventPluginsUpdated}},
	FactPluginDisconnected:     {events: []string{protocol.EventPluginsUpdated, protocol.EventSettingsUpdated}},
	FactPluginDriverRegistered: {events: []string{protocol.EventSettingsUpdated}},
	FactBackupWritten:          {events: []string{protocol.EventSettingsUpdated}},
	FactTailscaleServeChanged:  {events: []string{protocol.EventSettingsUpdated}},
	FactSettingChanged:         {events: []string{protocol.EventSettingsUpdated}},

	// Everything else with a panel of its own.
	FactNotificationCreated: {events: []string{protocol.EventNotificationsUpdated}},
	FactNotificationRead:    {events: []string{protocol.EventNotificationsUpdated}},
	FactAutomationChanged:   {events: []string{protocol.EventAutomationsChanged}},
	FactTaskChanged:         {events: []string{protocol.EventTasksChanged}},
	FactNotebookFileChanged: {
		events:  []string{protocol.EventNotebookChanged},
		subject: func(*wireWorld) string { return "note.md" },
	},
	FactWorkflowRunUpdated: {
		events:  []string{protocol.EventWorkflowRunUpdated},
		subject: func(*wireWorld) string { return "run-1" },
		payload: func(*wireWorld) any {
			return &protocol.WorkflowRun{RunID: "run-1", Status: protocol.WorkflowRunStatusRunning}
		},
	},
	FactPresentationAdded: {
		events:  []string{protocol.EventPresentationAdded},
		subject: (*wireWorld).presentation,
	},
	FactPresentationUpdated: {
		events:  []string{protocol.EventPresentationUpdated},
		subject: (*wireWorld).presentation,
	},
	// All three re-push the whole registry: the frontend mounts app views from
	// that snapshot, so a version flip, an enable and a removal are the same
	// invalidation to it.
	FactAppVersionChanged: {
		events:  []string{protocol.EventAppsUpdated},
		subject: func(*wireWorld) string { return "wire-app" },
	},
	FactAppEnabledChanged: {
		events:  []string{protocol.EventAppsUpdated},
		subject: func(*wireWorld) string { return "wire-app" },
	},
	FactAppRemoved: {
		events:  []string{protocol.EventAppsUpdated},
		subject: func(*wireWorld) string { return "wire-app" },
	},
}

// factsWithoutWire are the declared facts that deliberately produce no WebSocket
// traffic. Same discipline as wireSenderExceptions: an entry is a design
// decision, and the reason is the point of writing it down. Without this list a
// projection deleted by accident would look exactly like a fact that never had
// one.
var factsWithoutWire = map[string]string{
	FactDocumentChanged:              "consumed by the live-query fan-out in documents.go, not by WebSocket clients",
	FactDocumentCollectionRemoved:    "same consumer as document.changed; ends the subscriptions that read the collection",
	FactDocumentCollectionRedeclared: "same consumer again; a redeclare that drops a queried field ends live queries at redeclare time",
	FactAppRuntimeChanged:            "supervision state is read back from the supervisor (`attn app runtime status`), never from a copy",
}

// TestEveryProjectedFactReachesTheWire publishes each fact that has a projection
// and asserts the wire saw exactly the events that fact is contracted to
// produce. Table-complete in both directions, so it covers a projection written
// next month without anyone remembering this file exists.
func TestEveryProjectedFactReachesTheWire(t *testing.T) {
	facts := declaredFactNames(t)

	for _, fact := range facts {
		if !factIsProjected(fact) {
			if _, known := factsWithoutWire[fact]; !known {
				t.Errorf("fact %q has no projection and no entry in factsWithoutWire.\n"+
					"If it should reach clients, add a wireProjections entry (internal/daemon/bus.go). "+
					"If it deliberately produces no WebSocket traffic, say so in factsWithoutWire "+
					"with the consumer that does read it.", fact)
			}
			continue
		}
		if _, wired := factsWithoutWire[fact]; wired {
			t.Errorf("fact %q is listed in factsWithoutWire but a projection matches it", fact)
			continue
		}
		fixture, ok := wireFixtures[fact]
		if !ok {
			t.Errorf("fact %q is projected but has no wireFixture.\n"+
				"Add one naming the wire events publishing it must produce; give it a subject "+
				"or a payload only if the projection needs one to do its work.", fact)
			continue
		}
		t.Run(fact, func(t *testing.T) {
			w := newWireWorld(t)
			subject := "no-such-entity"
			if fixture.subject != nil {
				subject = fixture.subject(w)
			}
			var payload any
			if fixture.payload != nil {
				payload = fixture.payload(w)
			}

			w.trace.Clear()
			w.d.publishFact(fact, subject, payload)

			got := sortedStrings(w.trace.EventNames())
			want := sortedStrings(fixture.events)
			if equalStrings(got, want) {
				return
			}
			if len(got) == 0 {
				t.Fatalf("publishing %q put nothing on the wire; its projection is contracted to send %v.\n"+
					"Either the projection stopped sending — which is the defect this test exists for — "+
					"or it needs a subject or payload the fixture does not give it.", fact, want)
			}
			t.Fatalf("publishing %q put %v on the wire, want %v", fact, got, want)
		})
	}

	for fact := range wireFixtures {
		if !factIsDeclared(facts, fact) {
			t.Errorf("wireFixtures has an entry for %q, which is not a declared fact constant in bus.go", fact)
		}
	}
	for fact := range factsWithoutWire {
		if !factIsDeclared(facts, fact) {
			t.Errorf("factsWithoutWire has an entry for %q, which is not a declared fact constant in bus.go", fact)
		}
	}
}

// wireWorld is one daemon with enough state that a projection re-reading the
// entity its fact names finds something. Everything a fixture can point at is
// built up front: a fixture picks, it does not construct.
type wireWorld struct {
	t              *testing.T
	d              *Daemon
	trace          *WireTrace
	sessionID      string
	workspaceID    string
	presentationID string
	worktreePath   string
}

func newWireWorld(t *testing.T) *wireWorld {
	t.Helper()
	dir := t.TempDir()
	d := NewForTesting(filepath.Join(dir, "attn.sock"))
	trace := &WireTrace{}
	d.wsHub.wireTap = trace.record

	w := &wireWorld{t: t, d: d, trace: trace}

	w.sessionID = "wire-session"
	d.store.Add(&protocol.Session{
		ID:        w.sessionID,
		Directory: dir,
		State:     protocol.SessionStateIdle,
		Agent:     protocol.SessionAgentClaude,
	})

	w.workspaceID = "wire-workspace"
	client := newWorkspaceProtocolTestClient()
	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        w.workspaceID,
		Title:     "wire",
		Directory: dir,
	})
	// Same ordering the app uses: register the workspace, then put the session
	// in a pane. A workspace without a layout is not a state the app produces.
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: w.workspaceID,
		PaneID:      protocol.Ptr("wire-pane"),
		SessionID:   w.sessionID,
	})
	if _, err := d.ensureWorkspaceLayout(w.workspaceID); err != nil {
		t.Fatalf("seed workspace layout: %v", err)
	}

	w.worktreePath = filepath.Join(dir, "worktree")

	pres, err := d.store.CreatePresentation(w.sessionID, nil, "wire", "diff", dir, time.Now())
	if err != nil {
		t.Fatalf("seed presentation: %v", err)
	}
	w.presentationID = pres.ID

	return w
}

func (w *wireWorld) session() string      { return w.sessionID }
func (w *wireWorld) workspace() string    { return w.workspaceID }
func (w *wireWorld) presentation() string { return w.presentationID }
func (w *wireWorld) worktree() string     { return w.worktreePath }

func (w *wireWorld) layout() *protocol.WorkspaceLayout {
	layout, err := w.d.protocolWorkspaceLayout(w.workspaceID)
	if err != nil {
		w.t.Fatalf("read seeded layout: %v", err)
	}
	return layout
}

// gitOperationFixture is the one payload a fixture has to build rather than
// pick: the daemon keeps no git-operation registry, so the fact is the only
// record of the operation.
func gitOperationFixture(id string) protocol.GitOperation {
	return protocol.GitOperation{
		ID:     id,
		Kind:   protocol.GitOperationKindDeleteWorktree,
		Status: protocol.GitOperationStatusRunning,
	}
}

// factIsProjected reports whether any wireProjections entry would run for it.
func factIsProjected(fact string) bool {
	for _, p := range wireProjections() {
		if p.filter.Matches(fact) {
			return true
		}
	}
	return false
}

func factIsDeclared(facts []string, fact string) bool {
	for _, f := range facts {
		if f == fact {
			return true
		}
	}
	return false
}

// declaredFactNames reads the fact vocabulary out of bus.go rather than
// re-listing it here: a new Fact… constant is picked up by parsing, so the only
// way to add a fact this test does not see is to stop declaring it as one.
func declaredFactNames(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "bus.go", nil, 0)
	if err != nil {
		t.Fatalf("parse bus.go: %v", err)
	}
	var names []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			values, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range values.Names {
				if !strings.HasPrefix(name.Name, "Fact") || i >= len(values.Values) {
					continue
				}
				lit, ok := values.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("read %s: %v", name.Name, err)
				}
				names = append(names, value)
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("no Fact… constants found in bus.go — this test would pass vacuously")
	}
	sort.Strings(names)
	return names
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
