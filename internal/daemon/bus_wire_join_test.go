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

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

// The observation point is wsHub.wireTap — the marshalled bytes of every send
// path, never a broadcast hook, which a projection can fire without sending.

type wireFixture struct {
	// Exact, not "at least": a second projection entry matching the same fact by
	// accident would double-push.
	events []string
	// subject defaults to one nothing resolves, which is correct for a snapshot
	// projection and wrong for one that re-reads the entity.
	subject func(*wireWorld) string
	payload func(*wireWorld) any
}

var wireFixtures = map[string]wireFixture{
	FactSessionStateChanged: {
		events:  []string{protocol.EventSessionStateChanged},
		subject: (*wireWorld).session,
	},
	FactSessionRegistered: {
		events:  []string{protocol.EventSessionRegistered, protocol.EventGardenSeedsUpdated},
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
	FactSessionModelRequestStarted: {
		events:  []string{protocol.EventSessionStateChanged},
		subject: (*wireWorld).session,
	},
	FactSessionActivityChanged: {
		events:  []string{protocol.EventSessionStateChanged},
		subject: (*wireWorld).session,
	},
	FactSessionCostChanged: {
		events:  []string{protocol.EventSessionStateChanged},
		subject: (*wireWorld).session,
	},
	FactSessionConversationChanged: {
		events:  []string{protocol.EventSessionStateChanged},
		subject: (*wireWorld).session,
	},
	FactSessionPullRequestChanged: {
		events:  []string{protocol.EventSessionStateChanged},
		subject: (*wireWorld).session,
	},
	FactSessionTerminalBuildChanged: {
		events:  []string{protocol.EventSessionStateChanged},
		subject: (*wireWorld).session,
	},
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
		events:  []string{protocol.EventSessionUnregistered, protocol.EventGardenSeedsUpdated},
		subject: (*wireWorld).session,
		payload: func(w *wireWorld) any { return w.d.sessionForBroadcast(w.d.store.Get(w.sessionID)) },
	},
	FactSessionRespawned: {
		events:  []string{protocol.EventRuntimeRespawned, protocol.EventGardenSeedsUpdated},
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

	FactSessionTerminated:       {events: []string{protocol.EventSessionsUpdated}},
	FactSessionBranchChanged:    {events: []string{protocol.EventSessionsUpdated}},
	FactSessionChiefRoleChanged: {events: []string{protocol.EventSessionsUpdated}},
	FactSessionReconciled:       {events: []string{protocol.EventSessionsUpdated}},
	FactWorktreeSessionsRemoved: {events: []string{protocol.EventSessionsUpdated}},
	FactEndpointSessionsChanged: {events: []string{protocol.EventSessionsUpdated}},

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

	FactGardenPlanted:               {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenBodyEdited:            {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenResumeIdentityChanged: {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenTended:                {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenParked:                {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenHarvested:             {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenWithered:              {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenReplanted:             {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenNoted:                 {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenArtifactChanged:       {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenLinked:                {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenUnlinked:              {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenHarvestWhenChanged:    {events: []string{protocol.EventGardenSeedsUpdated}},
	FactGardenReviewChanged: {
		events:  []string{protocol.EventGardenReviewUpdated},
		subject: (*wireWorld).gardenReview,
	},

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

	FactWorktreeCreated: {
		events:  []string{protocol.EventWorktreeCreated},
		subject: (*wireWorld).worktree,
		payload: func(w *wireWorld) any { return protocol.Worktree{Path: w.worktreePath} },
	},
	FactWorktreeDeleted: {
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

	FactRateLimited: {
		events:  []string{protocol.EventRateLimited},
		subject: func(*wireWorld) string { return "core" },
		payload: func(*wireWorld) any { return rateLimitWindow{ResetAt: "2026-08-12T00:00:00Z"} },
	},
	FactGitHubHostAdded:   {events: []string{protocol.EventGitHubHostsUpdated}},
	FactGitHubHostRemoved: {events: []string{protocol.EventGitHubHostsUpdated}},

	FactEndpointAdded:   {events: []string{protocol.EventEndpointsUpdated}},
	FactEndpointRemoved: {events: []string{protocol.EventEndpointsUpdated}},
	FactEndpointChanged: {events: []string{protocol.EventEndpointsUpdated}},
	FactEndpointStatusChanged: {
		events:  []string{protocol.EventEndpointStatusChanged},
		payload: func(*wireWorld) any { return protocol.EndpointInfo{ID: "endpoint-1", Status: "connected"} },
	},

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

	FactNotificationCreated: {events: []string{protocol.EventNotificationsUpdated}},
	FactNotificationRead:    {events: []string{protocol.EventNotificationsUpdated}},
	FactAutoModeDenied:      {events: []string{protocol.EventNotificationsUpdated}},
	FactAutoModeConfigChanged: {
		events:  []string{protocol.EventAutoModeStateChanged},
		subject: func(*wireWorld) string { return AutoModeConfigSubject },
	},
	FactAutomationChanged: {events: []string{protocol.EventAutomationsChanged}},
	FactTaskChanged:       {events: []string{protocol.EventTasksChanged}},
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
	FactCrewRegistered: {events: []string{protocol.EventCrewUpdated}},
	FactCrewBound:      {events: []string{protocol.EventCrewUpdated}},
	FactCrewReleased:   {events: []string{protocol.EventCrewUpdated}},
	FactCrewUpdated:    {events: []string{protocol.EventCrewUpdated}},

	FactPresentationAdded: {
		events:  []string{protocol.EventPresentationAdded},
		subject: (*wireWorld).presentation,
	},
	FactPresentationUpdated: {
		events:  []string{protocol.EventPresentationUpdated},
		subject: (*wireWorld).presentation,
	},
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

var factsWithoutWire = map[string]string{
	FactDocumentChanged:              "consumed by the live-query fan-out in documents.go, not by WebSocket clients",
	FactDocumentCollectionRemoved:    "same consumer as document.changed; ends the subscriptions that read the collection",
	FactDocumentCollectionRedeclared: "same consumer again; a redeclare that drops a queried field ends live queries at redeclare time",
	FactAppRuntimeChanged:            "supervision state is read back from the supervisor (`attn app runtime status`), never from a copy",
	FactTicketCreated:                ticketFactsHaveNoClient,
	FactTicketStatusChanged:          ticketFactsHaveNoClient,
	FactTicketCommented:              ticketFactsHaveNoClient,
	FactTicketAssigned:               ticketFactsHaveNoClient,
	FactTicketAttached:               ticketFactsHaveNoClient,
	FactTicketChanged:                ticketFactsHaveNoClient,
	FactSessionClosed:                "the app learns a session left through session.unregistered, which already removes the row; the ledger row it records is read on demand through session_list and session_show",
}

const ticketFactsHaveNoClient = "no WebSocket client renders a ticket; the read verbs and subscribing apps read these off the durable log"

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

func (w *wireWorld) gardenReview() string {
	w.d.ensureGardenCollections()
	run := garden.ReviewRun{
		ID: "r-wire", Status: garden.ReviewRunStatusComplete,
		CapturedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Recipe:     garden.ReviewRecipe{Agent: "codex", Model: "gpt-5.6-luna", Effort: "xhigh"},
	}
	if err := w.d.createGardenReview(run, nil); err != nil {
		w.t.Fatalf("seed Garden review: %v", err)
	}
	return run.ID
}

func (w *wireWorld) layout() *protocol.WorkspaceLayout {
	layout, err := w.d.protocolWorkspaceLayout(w.workspaceID)
	if err != nil {
		w.t.Fatalf("read seeded layout: %v", err)
	}
	return layout
}

func gitOperationFixture(id string) protocol.GitOperation {
	return protocol.GitOperation{
		ID:     id,
		Kind:   protocol.GitOperationKindDeleteWorktree,
		Status: protocol.GitOperationStatusRunning,
	}
}

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

// Parses the fact vocabulary out of bus.go, so the only way to add a fact this
// test does not see is to stop declaring it as one.
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
