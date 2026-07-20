package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// TestAutomationDefinitionsGetWSResultCorrelatesRequest exercises the WS list
// path: an applied definition comes back as one summary correlated by
// request_id, with the schedule/policy fields pulled from its spec.
func TestAutomationDefinitionsGetWSResultCorrelatesRequest(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationDefinitionsGetWS(client, &protocol.AutomationDefinitionsGetMessage{
		Cmd:       protocol.CmdAutomationDefinitionsGet,
		RequestID: protocol.Ptr("defs-1"),
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.RequestID == nil || *res.RequestID != "defs-1" {
		t.Fatalf("definitions_get result = %+v, want success for defs-1", res)
	}
	if len(res.Definitions) != 1 || res.Definitions[0].ID != def.ID {
		t.Fatalf("definitions = %+v, want one summary for %s", res.Definitions, def.ID)
	}
	summary := res.Definitions[0]
	if summary.TriggerType != "manual" || !summary.Enabled {
		t.Fatalf("summary = %+v, want manual+enabled", summary)
	}
}

// TestAutomationRunsGetWSResultCorrelatesRequest claims one manual run and
// confirms the WS runs_get result carries it, including the joined
// occurrence_key, correlated by request_id.
func TestAutomationRunsGetWSResultCorrelatesRequest(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.ClaimManualAutomationRun(def.ID, "request-1", "", `{}`, def.Revision, `{}`, time.Now(), store.AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationRunsGetWS(client, &protocol.AutomationRunsGetMessage{
		Cmd:          protocol.CmdAutomationRunsGet,
		DefinitionID: def.ID,
		RequestID:    protocol.Ptr("runs-1"),
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.RequestID == nil || *res.RequestID != "runs-1" {
		t.Fatalf("runs_get result = %+v, want success for runs-1", res)
	}
	if res.Truncated != nil && *res.Truncated {
		t.Fatalf("runs_get result truncated=%v, want unset/false for one run", res.Truncated)
	}
	if len(res.Runs) != 1 || res.Runs[0].ID != run.ID {
		t.Fatalf("runs = %+v, want one summary for %s", res.Runs, run.ID)
	}
	if res.Runs[0].OccurrenceKey == nil || *res.Runs[0].OccurrenceKey != "manual:request-1" {
		t.Fatalf("run occurrence_key = %v, want manual:request-1", res.Runs[0].OccurrenceKey)
	}
}

// TestAutomationRunsGetWSResultTruncatesAtCap seeds one more run than
// automationRunSummaryListCap and confirms the result caps the list and sets
// truncated=true rather than returning an unbounded payload.
func TestAutomationRunsGetWSResultTruncatesAtCap(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < automationRunSummaryListCap+1; i++ {
		requestID := fmt.Sprintf("request-%d", i)
		if _, _, err := s.ClaimManualAutomationRun(def.ID, requestID, "", `{}`, def.Revision, `{}`, time.Now(), store.AutomationRunReservation{
			RunID: fmt.Sprintf("run-%d", i), OccurrenceID: fmt.Sprintf("occ-%d", i),
			TicketID: fmt.Sprintf("ticket-%d", i), SessionID: fmt.Sprintf("session-%d", i),
			WorkspaceID: fmt.Sprintf("workspace-%d", i), PaneID: fmt.Sprintf("pane-%d", i),
		}); err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationRunsGetWS(client, &protocol.AutomationRunsGetMessage{
		Cmd:          protocol.CmdAutomationRunsGet,
		DefinitionID: def.ID,
		RequestID:    protocol.Ptr("runs-cap"),
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success {
		t.Fatalf("runs_get result = %+v, want success", res)
	}
	if len(res.Runs) != automationRunSummaryListCap {
		t.Fatalf("runs = %d, want capped at %d", len(res.Runs), automationRunSummaryListCap)
	}
	if res.Truncated == nil || !*res.Truncated {
		t.Fatalf("truncated = %v, want true", res.Truncated)
	}
}

// TestAutomationSetEnabledWSResultCorrelatesRequest drives the mutation
// handler (which runs the store call in a goroutine) and confirms the result
// is still correlated by request_id once it lands.
func TestAutomationSetEnabledWSResultCorrelatesRequest(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationSetEnabledWS(client, &protocol.AutomationSetEnabledMessage{
		Cmd:          protocol.CmdAutomationSetEnabled,
		DefinitionID: def.ID,
		Enabled:      false,
		RequestID:    protocol.Ptr("set-1"),
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.RequestID == nil || *res.RequestID != "set-1" {
		t.Fatalf("set_enabled result = %+v, want success for set-1", res)
	}
	if len(res.Definitions) != 1 || res.Definitions[0].Enabled {
		t.Fatalf("set_enabled definitions = %+v, want one disabled summary", res.Definitions)
	}

	// Unknown definition surfaces as success=false with an error, not a
	// transport failure.
	client2 := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationSetEnabledWS(client2, &protocol.AutomationSetEnabledMessage{
		Cmd:          protocol.CmdAutomationSetEnabled,
		DefinitionID: "does-not-exist",
		Enabled:      true,
		RequestID:    protocol.Ptr("set-2"),
	})
	var errRes protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client2.send, &errRes)
	if errRes.Success || errRes.Error == nil {
		t.Fatalf("set_enabled unknown definition result = %+v, want success=false with error", errRes)
	}
}

// TestAutomationDeleteWSResultCorrelatesRequest mirrors
// TestAutomationSetEnabledWSResultCorrelatesRequest for handleAutomationDeleteWS:
// a successful delete's result is still correlated by request_id once it
// lands, and an unknown definition surfaces as success=false with an error
// rather than a transport failure.
func TestAutomationDeleteWSResultCorrelatesRequest(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationDeleteWS(client, &protocol.AutomationDeleteMessage{
		Cmd:          protocol.CmdAutomationDelete,
		DefinitionID: def.ID,
		RequestID:    protocol.Ptr("delete-1"),
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.RequestID == nil || *res.RequestID != "delete-1" {
		t.Fatalf("delete result = %+v, want success for delete-1", res)
	}

	if got, err := s.GetAutomationDefinition(def.ID); err != nil || got != nil {
		t.Fatalf("expected the definition to be soft-deleted, got %#v err=%v", got, err)
	}

	// Unknown definition surfaces as success=false with an error, not a
	// transport failure.
	client2 := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationDeleteWS(client2, &protocol.AutomationDeleteMessage{
		Cmd:          protocol.CmdAutomationDelete,
		DefinitionID: "does-not-exist",
		RequestID:    protocol.Ptr("delete-2"),
	})
	var errRes protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client2.send, &errRes)
	if errRes.Success || errRes.Error == nil {
		t.Fatalf("delete unknown definition result = %+v, want success=false with error", errRes)
	}
}

// TestAutomationCleanupWSResultCorrelatesRequest exercises
// handleAutomationCleanupWS: a definition with one clean-worktree terminal
// run reports it in the result's Cleaned field, correlated by request_id;
// an unknown definition surfaces as success=false, matching delete's
// business-failure shape.
func TestAutomationCleanupWSResultCorrelatesRequest(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(root, "repo--clean")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/cleanup-ws", worktree)

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	run := claimTerminalAutomationRun(t, s, def, "cleanup-ws-1", time.Now(), automationResolvedLocationJSON(t, mainRepo, worktree))

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationCleanupWS(client, &protocol.AutomationCleanupMessage{
		Cmd:          protocol.CmdAutomationCleanup,
		DefinitionID: def.ID,
		RequestID:    protocol.Ptr("cleanup-1"),
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.RequestID == nil || *res.RequestID != "cleanup-1" {
		t.Fatalf("cleanup result = %+v, want success for cleanup-1", res)
	}
	if len(res.Cleaned) != 1 || res.Cleaned[0] != run.ID {
		t.Fatalf("cleanup result Cleaned = %v, want [%s]", res.Cleaned, run.ID)
	}
	if len(res.KeptDirty) != 0 {
		t.Fatalf("cleanup result KeptDirty = %v, want none", res.KeptDirty)
	}

	// Unknown definition surfaces as success=false with an error, not a
	// transport failure.
	client2 := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationCleanupWS(client2, &protocol.AutomationCleanupMessage{
		Cmd:          protocol.CmdAutomationCleanup,
		DefinitionID: "does-not-exist",
		RequestID:    protocol.Ptr("cleanup-2"),
	})
	var errRes protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client2.send, &errRes)
	if errRes.Success || errRes.Error == nil {
		t.Fatalf("cleanup unknown definition result = %+v, want success=false with error", errRes)
	}
}

// TestAutomationRunWSResultCorrelatesRequest exercises handleAutomationRunWS
// (also goroutine-backed) via the same idempotent-dedup technique as
// TestAutomationRunBroadcastsAfterClaim: pre-claiming and pre-delivering the
// run under the same request_id means the handler's own claim short-circuits
// before reaching real delivery/backend machinery.
func TestAutomationRunWSResultCorrelatesRequest(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	run, _, err := s.ClaimManualAutomationRun(def.ID, "request-1", "", `{}`, def.Revision, `{}`, now, store.AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run.ID, "{}", now); err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationRunWS(client, &protocol.AutomationRunMessage{
		Cmd:          protocol.CmdAutomationRun,
		DefinitionID: def.ID,
		RequestID:    "request-1",
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.RequestID == nil || *res.RequestID != "request-1" {
		t.Fatalf("run result = %+v, want success for request-1", res)
	}
	if res.RunID == nil || *res.RunID != run.ID {
		t.Fatalf("run result run_id = %v, want %s", res.RunID, run.ID)
	}
	if res.TicketID == nil || *res.TicketID != run.TicketID || res.SessionID == nil || *res.SessionID != run.SessionID {
		t.Fatalf("run result ticket/session = %+v, want %s/%s", res, run.TicketID, run.SessionID)
	}
}

// TestAutomationRunWSRejectsNonManualTrigger is the WS-level half of Fix F8:
// handleAutomationRunWS must surface a provider-driven definition's
// manual-trigger rejection as success=false with the same error text as the
// daemon-level automationRun path, not a transport-level failure.
func TestAutomationRunWSRejectsNonManualTrigger(t *testing.T) {
	d, _, def, _ := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationRunWS(client, &protocol.AutomationRunMessage{
		Cmd:          protocol.CmdAutomationRun,
		DefinitionID: def.ID,
		RequestID:    "request-1",
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil || !strings.Contains(*res.Error, "cannot be run manually") {
		t.Fatalf("run result = %+v, want success=false with a manual-trigger rejection", res)
	}
}

// TestAutomationRunWSMutualExclusion mirrors the unix-socket arm's pr_url /
// input_json mutual-exclusion guard (Fix F3): the WS arm must reject both
// being set with the same error text rather than silently ignoring pr_url.
func TestAutomationRunWSMutualExclusion(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationRunWS(client, &protocol.AutomationRunMessage{
		Cmd:          protocol.CmdAutomationRun,
		DefinitionID: def.ID,
		RequestID:    "request-1",
		PRURL:        protocol.Ptr("https://github.com/owner/repo/pull/1"),
		InputJson:    protocol.Ptr(`{}`),
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil || !strings.Contains(*res.Error, "mutually exclusive") {
		t.Fatalf("run result = %+v, want success=false mutual-exclusion error", res)
	}
}

// TestAutomationRunWSRoutesPRURL mirrors the unix-socket arm's pr_url
// routing (Fix F3): a WS automation_run with only pr_url set must reach
// automationRunPullRequest, not the manual automationRun path. A manual
// definition's location isn't repository_worktree, so
// automationRunPullRequest's own validation rejects it — proving routing
// without building GitHub fixtures, and with an error text distinct from the
// manual-path's own errors.
func TestAutomationRunWSRoutesPRURL(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationRunWS(client, &protocol.AutomationRunMessage{
		Cmd:          protocol.CmdAutomationRun,
		DefinitionID: def.ID,
		RequestID:    "request-1",
		PRURL:        protocol.Ptr("https://github.com/owner/repo/pull/1"),
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil {
		t.Fatalf("run result = %+v, want success=false (pr_url routed to the pull-request path)", res)
	}
	if strings.Contains(*res.Error, "mutually exclusive") || strings.Contains(*res.Error, "cannot be run manually") {
		t.Fatalf("run result error = %q, want automationRunPullRequest's own validation error, not a manual-path error", *res.Error)
	}
}

// TestAutomationSetEnabledWSDeadlineAbortsWithoutMutating pins the
// timeout-vs-mutation trap from PR #619's review: automationSetEnabled must
// abort once its daemon-side deadline (wsAutomationMutationTimeout) elapses
// while still waiting on automationMu, and must NOT mutate the definition
// afterward. Without this guard, a slow in-flight delivery holding
// automationMu for longer than the frontend's 30s client timeout lets the
// toggle flip after the UI has already reported "timed out" — invisible to
// the user who believes their click had no effect.
func TestAutomationSetEnabledWSDeadlineAbortsWithoutMutating(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub(), wsAutomationMutationTimeout: 50 * time.Millisecond}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !def.Enabled {
		t.Fatalf("fixture definition = %#v, want enabled", def)
	}

	// Simulate an in-flight automation delivery holding automationMu well past
	// the 50ms deadline; released after 200ms.
	d.automationMu.Lock()
	released := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		d.automationMu.Unlock()
		close(released)
	}()

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationSetEnabledWS(client, &protocol.AutomationSetEnabledMessage{
		Cmd:          protocol.CmdAutomationSetEnabled,
		DefinitionID: def.ID,
		Enabled:      false,
		RequestID:    protocol.Ptr("set-deadline"),
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil || !strings.Contains(*res.Error, "deadline exceeded") {
		t.Fatalf("set_enabled result = %+v, want success=false with a deadline error", res)
	}

	<-released

	stored, err := s.GetAutomationDefinition(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Enabled {
		t.Fatalf("definition after deadline abort = %#v, want still enabled (no late flip)", stored)
	}

	// Sanity: an uncontended set_enabled once the lock has freed still succeeds.
	client2 := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationSetEnabledWS(client2, &protocol.AutomationSetEnabledMessage{
		Cmd:          protocol.CmdAutomationSetEnabled,
		DefinitionID: def.ID,
		Enabled:      false,
		RequestID:    protocol.Ptr("set-after"),
	})
	var res2 protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client2.send, &res2)
	if !res2.Success {
		t.Fatalf("set_enabled after lock freed = %+v, want success", res2)
	}
	stored2, err := s.GetAutomationDefinition(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored2.Enabled {
		t.Fatalf("definition after uncontended set_enabled = %#v, want disabled", stored2)
	}
}

// TestAutomationDeleteWSDeadlineAbortsWithoutMutating mirrors
// TestAutomationSetEnabledWSDeadlineAbortsWithoutMutating for delete's own
// post-lock ctx.Err() check: automationDelete must abort once the deadline
// elapses while still waiting on automationMu, and must NOT have deleted the
// definition afterward — the same invisible-to-the-user trap as set_enabled,
// since delete makes five separate store mutations once it acquires the lock.
func TestAutomationDeleteWSDeadlineAbortsWithoutMutating(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub(), wsAutomationMutationTimeout: 50 * time.Millisecond}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate an in-flight automation delivery holding automationMu well past
	// the 50ms deadline; released after 200ms.
	d.automationMu.Lock()
	released := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		d.automationMu.Unlock()
		close(released)
	}()

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationDeleteWS(client, &protocol.AutomationDeleteMessage{
		Cmd:          protocol.CmdAutomationDelete,
		DefinitionID: def.ID,
		RequestID:    protocol.Ptr("delete-deadline"),
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil || !strings.Contains(*res.Error, "deadline exceeded") {
		t.Fatalf("delete result = %+v, want success=false with a deadline error", res)
	}

	<-released

	stored, err := s.GetAutomationDefinition(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil {
		t.Fatal("definition after deadline abort = nil, want still present (no late delete)")
	}

	// Sanity: an uncontended delete once the lock has freed still succeeds.
	client2 := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationDeleteWS(client2, &protocol.AutomationDeleteMessage{
		Cmd:          protocol.CmdAutomationDelete,
		DefinitionID: def.ID,
		RequestID:    protocol.Ptr("delete-after"),
	})
	var res2 protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client2.send, &res2)
	if !res2.Success {
		t.Fatalf("delete after lock freed = %+v, want success", res2)
	}
	stored2, err := s.GetAutomationDefinition(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored2 != nil {
		t.Fatalf("definition after uncontended delete = %#v, want gone (soft-deleted)", stored2)
	}
}

// TestAutomationRunWSRetryWithSameRequestIDDoesNotDuplicate pins the run-now
// half of the timeout-vs-mutation trap: a client that times out waiting for
// automation_run and retries with the SAME request_id (the fix in
// AutomationsPanel/useDaemonSocket) must dedup onto the original claim rather
// than creating a second run, even when both calls contend on automationMu.
//
// The run is pre-claimed and pre-marked delivered directly through the store
// (matching TestAutomationRunBroadcastsAfterClaim's technique) so neither
// call re-enters deliverAutomationRun/real backend machinery: automationRun's
// own ClaimManualAutomationRun call idempotently dedups on request_id before
// either goroutine even reaches automationMu, and both then read back the
// same already-delivered run once the held lock frees. automationDeliveryHook
// does not apply here — it only affects deliverObservedAutomationRun's
// provider-observation path, not automationRun's manual-run path.
func TestAutomationRunWSRetryWithSameRequestIDDoesNotDuplicate(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	run, _, err := s.ClaimManualAutomationRun(def.ID, "retry-request", "", `{}`, def.Revision, `{}`, now, store.AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run.ID, "{}", now); err != nil {
		t.Fatal(err)
	}

	// Simulate an in-flight delivery holding automationMu while both retry
	// attempts arrive and queue behind it.
	d.automationMu.Lock()
	released := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		d.automationMu.Unlock()
		close(released)
	}()

	client1 := &wsClient{send: make(chan outboundMessage, 4)}
	client2 := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationRunWS(client1, &protocol.AutomationRunMessage{
		Cmd:          protocol.CmdAutomationRun,
		DefinitionID: def.ID,
		RequestID:    "retry-request",
	})
	d.handleAutomationRunWS(client2, &protocol.AutomationRunMessage{
		Cmd:          protocol.CmdAutomationRun,
		DefinitionID: def.ID,
		RequestID:    "retry-request",
	})

	var res1, res2 protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client1.send, &res1)
	readNotebookWSEvent(t, client2.send, &res2)
	<-released

	if !res1.Success || !res2.Success {
		t.Fatalf("run results = %+v / %+v, want both success", res1, res2)
	}
	if res1.RunID == nil || res2.RunID == nil || *res1.RunID != run.ID || *res2.RunID != run.ID {
		t.Fatalf("run ids = %v / %v, want both %s", res1.RunID, res2.RunID, run.ID)
	}

	runs, err := s.ListAutomationRuns(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs for definition = %d, want exactly 1 (no duplicate claim from the retried request_id)", len(runs))
	}
}
