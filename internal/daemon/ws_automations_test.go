package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
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
// run reports it in the result's Cleaned field (KeptDirty/KeptActive empty),
// correlated by request_id; an unknown definition surfaces as success=false,
// matching delete's business-failure shape.
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
	if len(res.KeptActive) != 0 {
		t.Fatalf("cleanup result KeptActive = %v, want none", res.KeptActive)
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

// TestAutomationValidateAndApplyAgreeOnCorpus is the regression test for D3
// (internal/daemon/automations.go's validateAutomationSpec doc comment): a
// validate command that only ran ParseDefinitionYAML would green-light YAML
// automation_apply then rejects, because apply layers four more checks on
// top of parse (resolveDelegationAgent, validateDelegationModelEffort, the
// codex|claude automatic-approval allowlist, and per-override
// ValidateLocalClone). Every case here must produce the SAME success/failure
// verdict — and, on failure, the same error text — from both
// handleAutomationValidateWS and handleAutomationApplyWS, whether the
// rejection comes from yaml.v3/ValidateDefinition (parse-level) or from one
// of validateAutomationSpec's extra checks (seam-level, the case this test
// exists to pin).
func TestAutomationValidateAndApplyAgreeOnCorpus(t *testing.T) {
	const template = `api_version: attn.dev/automations/v1alpha1
id: %s
name: Corpus case
trigger: {type: manual}
prompt: Do the thing.
launch: {driver: %s}
location: {type: directory, path: "%s"}
`
	cases := []struct {
		name      string
		id        string
		driver    string
		mutate    func(raw string) string
		wantValid bool
		wantErr   string
	}{
		{name: "valid codex baseline", id: "corpus-valid-codex", driver: "codex", wantValid: true},
		{name: "valid claude baseline", id: "corpus-valid-claude", driver: "claude", wantValid: true},
		{
			name:      "driver outside the automatic-approval allowlist is rejected beyond parse",
			id:        "corpus-shell-driver",
			driver:    "shell",
			wantValid: false,
			wantErr:   "does not support automation automatic approval",
		},
		{
			name:      "unresolvable agent is rejected beyond parse",
			id:        "corpus-fake-driver",
			driver:    "totally-not-a-real-agent",
			wantValid: false,
			wantErr:   "not available",
		},
		{
			name:   "missing prompt is rejected at parse",
			id:     "corpus-missing-prompt",
			driver: "codex",
			mutate: func(raw string) string {
				return strings.Replace(raw, "prompt: Do the thing.\n", "", 1)
			},
			wantValid: false,
			wantErr:   "prompt is required",
		},
		{
			name:   "bad api_version is rejected at parse",
			id:     "corpus-bad-api-version",
			driver: "codex",
			mutate: func(raw string) string {
				return strings.Replace(raw, "attn.dev/automations/v1alpha1", "attn.dev/automations/v0", 1)
			},
			wantValid: false,
			wantErr:   "api_version must be",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := store.New()
			d := &Daemon{store: s, wsHub: newWSHub()}
			raw := fmt.Sprintf(template, tc.id, tc.driver, t.TempDir())
			if tc.mutate != nil {
				raw = tc.mutate(raw)
			}

			validateClient := &wsClient{send: make(chan outboundMessage, 4)}
			d.handleAutomationValidateWS(validateClient, &protocol.AutomationValidateMessage{
				Cmd:            protocol.CmdAutomationValidate,
				DefinitionYaml: raw,
				RequestID:      protocol.Ptr("validate"),
			})
			var validateRes protocol.AutomationActionResultMessage
			readNotebookWSEvent(t, validateClient.send, &validateRes)

			applyClient := &wsClient{send: make(chan outboundMessage, 4)}
			d.handleAutomationApplyWS(applyClient, &protocol.AutomationApplyMessage{
				Cmd:            protocol.CmdAutomationApply,
				DefinitionYaml: raw,
				RequestID:      protocol.Ptr("apply"),
			})
			var applyRes protocol.AutomationActionResultMessage
			readNotebookWSEvent(t, applyClient.send, &applyRes)

			if validateRes.Success != applyRes.Success {
				t.Fatalf("validate/apply disagree on %q: validate success=%v (err=%v), apply success=%v (err=%v)",
					tc.name, validateRes.Success, validateRes.Error, applyRes.Success, applyRes.Error)
			}
			if validateRes.Success != tc.wantValid {
				t.Fatalf("success = %v, want %v (validate error=%v, apply error=%v)", validateRes.Success, tc.wantValid, validateRes.Error, applyRes.Error)
			}
			if !tc.wantValid {
				if validateRes.Error == nil || !strings.Contains(*validateRes.Error, tc.wantErr) {
					t.Fatalf("validate error = %v, want to contain %q", validateRes.Error, tc.wantErr)
				}
				if applyRes.Error == nil || !strings.Contains(*applyRes.Error, tc.wantErr) {
					t.Fatalf("apply error = %v, want to contain %q", applyRes.Error, tc.wantErr)
				}
			}
		})
	}
}

// TestAutomationApplyWSRefusesIDMismatch pins D4: the WS editor's Save
// carries expected_id (the id of the definition it loaded), and the daemon
// must refuse an apply whose YAML id differs rather than silently upserting
// a second definition and leaving the original (still enabled, still
// running) untouched — see automationApplyWithGuards's doc comment.
func TestAutomationApplyWSRefusesIDMismatch(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	original, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	renamed := strings.Replace(raw, "id: manual-check", "id: manual-check-renamed", 1)
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationApplyWS(client, &protocol.AutomationApplyMessage{
		Cmd:              protocol.CmdAutomationApply,
		DefinitionYaml:   renamed,
		ExpectedID:       protocol.Ptr(original.ID),
		ExpectedRevision: protocol.Ptr(original.Revision),
		RequestID:        protocol.Ptr("apply-id-mismatch"),
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil || !strings.Contains(*res.Error, "does not match the definition being edited") {
		t.Fatalf("apply result = %+v, want success=false with an id-mismatch error", res)
	}

	// The original definition must be untouched, and no second definition
	// created under the renamed id.
	stillOriginal, err := s.GetAutomationDefinition(original.ID)
	if err != nil || stillOriginal == nil || stillOriginal.Revision != original.Revision {
		t.Fatalf("original definition after refused apply = %#v err=%v, want unchanged", stillOriginal, err)
	}
	if got, err := s.GetAutomationDefinition("manual-check-renamed"); err != nil || got != nil {
		t.Fatalf("renamed id after refused apply = %#v err=%v, want no definition created", got, err)
	}
}

// TestAutomationApplyWSRefusesCreateOverLiveDefinition pins the create half
// of the identity guard. Apply is keyed on the id inside the YAML, so a
// create whose id collides with a live definition would UPDATE that
// definition in place — the user typing an existing id into a form labelled
// "New automation" would replace an automation they never opened. A
// soft-deleted row is not a collision: re-applying its id is how resurrect
// works, and the editor's list never showed it.
func TestAutomationApplyWSRefusesCreateOverLiveDefinition(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	original, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	// Create (no expected_id, no expected_revision) carrying the same id but
	// entirely different content.
	colliding := strings.Replace(raw, "Check locally.", "Something else entirely.", 1)
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationApplyWS(client, &protocol.AutomationApplyMessage{
		Cmd:            protocol.CmdAutomationApply,
		DefinitionYaml: colliding,
		RequestID:      protocol.Ptr("apply-create-collision"),
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil || !strings.Contains(*res.Error, "already exists") {
		t.Fatalf("apply result = %+v, want success=false with an already-exists error", res)
	}
	survivor, err := s.GetAutomationDefinition(original.ID)
	if err != nil || survivor == nil {
		t.Fatalf("definition after refused create = %#v err=%v, want it to survive", survivor, err)
	}
	if survivor.Revision != original.Revision || !strings.Contains(survivor.SpecJSON, "Check locally.") {
		t.Fatalf("definition after refused create = revision %d spec_json %q, want the original untouched",
			survivor.Revision, survivor.SpecJSON)
	}

	// Soft-deleted is not a collision: the same create now resurrects.
	if err := d.automationDelete(context.Background(), original.ID); err != nil {
		t.Fatal(err)
	}
	resurrectClient := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationApplyWS(resurrectClient, &protocol.AutomationApplyMessage{
		Cmd:            protocol.CmdAutomationApply,
		DefinitionYaml: colliding,
		RequestID:      protocol.Ptr("apply-create-resurrect"),
	})
	var resurrectRes protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, resurrectClient.send, &resurrectRes)
	if !resurrectRes.Success {
		t.Fatalf("resurrect apply result = %+v, want success", resurrectRes)
	}
	resurrected, err := s.GetAutomationDefinition(original.ID)
	if err != nil || resurrected == nil || !strings.Contains(resurrected.SpecJSON, "Something else entirely.") {
		t.Fatalf("definition after resurrect = %#v err=%v, want the new content live", resurrected, err)
	}
}

// TestAutomationApplyWSRefusesStaleRevision pins D5: the WS editor's Save
// carries expected_revision (the revision it loaded), and the daemon must
// refuse to clobber a definition that changed elsewhere (another app window,
// or the CLI) since the editor loaded it — the app cannot silently overwrite
// a concurrent apply. expected_revision 0 (the create case) never triggers
// this guard.
func TestAutomationApplyWSRefusesStaleRevision(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	original, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	// A concurrent CLI apply bumps the revision out from under the editor.
	concurrentlyApplied := strings.Replace(raw, "Manual check", "Manual check (renamed elsewhere)", 1)
	if _, err := d.automationApply(concurrentlyApplied); err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationApplyWS(client, &protocol.AutomationApplyMessage{
		Cmd:              protocol.CmdAutomationApply,
		DefinitionYaml:   strings.Replace(raw, "Manual check", "Manual check (from the stale editor)", 1),
		ExpectedID:       protocol.Ptr(original.ID),
		ExpectedRevision: protocol.Ptr(original.Revision),
		RequestID:        protocol.Ptr("apply-stale-revision"),
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil || !strings.Contains(*res.Error, "changed elsewhere") {
		t.Fatalf("apply result = %+v, want success=false with a changed-elsewhere error", res)
	}

	stored, err := s.GetAutomationDefinition(original.ID)
	if err != nil || stored == nil || stored.Name != "Manual check (renamed elsewhere)" {
		t.Fatalf("definition after refused stale apply = %#v err=%v, want the concurrent apply's name to survive", stored, err)
	}
}

// TestAutomationApplyWSRefusesEditOfDeletedDefinition pins the edit half of
// the delete/resurrect guard: DeleteAutomationDefinition sets deleted_at
// without touching revision, so a stale editor's expected_revision still
// matches after someone else deletes the definition it has open. Unlike a
// create (expected_revision 0), which deliberately treats a soft-deleted row
// as resurrectable, a Save from an open editor must refuse rather than
// silently bring back a definition that automationDelete already tore down
// (failed pending runs, fenced provider cursors, purged continuity/
// review-request state) — the alternative is an unattended cron the user
// deliberately deleted starting to fire again, reported as "saved
// successfully".
func TestAutomationApplyWSRefusesEditOfDeletedDefinition(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	original, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	// Someone else deletes the definition while the editor still has it open.
	if err := d.automationDelete(context.Background(), original.ID); err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationApplyWS(client, &protocol.AutomationApplyMessage{
		Cmd:              protocol.CmdAutomationApply,
		DefinitionYaml:   strings.Replace(raw, "Manual check", "Manual check (from the stale editor)", 1),
		ExpectedID:       protocol.Ptr(original.ID),
		ExpectedRevision: protocol.Ptr(original.Revision),
		RequestID:        protocol.Ptr("apply-edit-deleted"),
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil || !strings.Contains(*res.Error, "deleted") || !strings.Contains(*res.Error, "New") {
		t.Fatalf("apply result = %+v, want success=false with an error naming the deletion and pointing at New", res)
	}

	// The definition must remain deleted, not resurrected.
	if live, err := s.GetAutomationDefinition(original.ID); err != nil || live != nil {
		t.Fatalf("definition after refused edit-of-deleted apply = %#v err=%v, want still absent from the live set", live, err)
	}
	stillDeleted, err := s.GetAutomationDefinitionIncludingDeleted(original.ID)
	if err != nil || stillDeleted == nil || stillDeleted.DeletedAt == nil {
		t.Fatalf("definition after refused edit-of-deleted apply = %#v err=%v, want still soft-deleted", stillDeleted, err)
	}
	if stillDeleted.Revision != original.Revision {
		t.Fatalf("deleted definition revision = %d, want unchanged %d", stillDeleted.Revision, original.Revision)
	}
}

// TestAutomationDefinitionGetWSStarterTemplate pins D7: definition_id "" is
// the new-definition case and must return the starter template at revision
// 0, so create and edit share one frontend code path.
func TestAutomationDefinitionGetWSStarterTemplate(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationDefinitionGetWS(client, &protocol.AutomationDefinitionGetMessage{
		Cmd:          protocol.CmdAutomationDefinitionGet,
		DefinitionID: "",
		RequestID:    protocol.Ptr("get-starter"),
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.SpecYaml == nil || res.Revision == nil {
		t.Fatalf("definition_get(\"\") result = %+v, want success with spec_yaml and revision set", res)
	}
	if *res.Revision != 0 {
		t.Fatalf("starter template revision = %d, want 0", *res.Revision)
	}
	if !strings.Contains(*res.SpecYaml, "id: my-automation") {
		t.Fatalf("starter template spec_yaml = %q, want the StarterDefinition placeholder", *res.SpecYaml)
	}
}

// TestAutomationDefinitionGetWSUnknownID surfaces an unknown id as
// success=false with an error, matching the rest of the automations WS
// surface's business-failure shape (delete/cleanup/set_enabled), not a
// transport-level failure.
func TestAutomationDefinitionGetWSUnknownID(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationDefinitionGetWS(client, &protocol.AutomationDefinitionGetMessage{
		Cmd:          protocol.CmdAutomationDefinitionGet,
		DefinitionID: "does-not-exist",
		RequestID:    protocol.Ptr("get-missing"),
	})

	var res protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil {
		t.Fatalf("definition_get(unknown) result = %+v, want success=false with an error", res)
	}
}

// TestAutomationDefinitionGetWSAfterToggleDerivesFromSpecJSON is the
// replacement for the pre-PR2a split-brain regression test: enabled now has
// exactly one authority (the store's enabled column) and is never present in
// spec_yaml/spec_json at all, so there is no split-brain to close. This pins
// the remaining contract instead — toggling via handleAutomationSetEnabledWS
// must not touch spec content, and definition_get's derive-from-spec_json
// path (automationDefinitionYAML, now the only path) must keep returning
// re-appliable YAML after the toggle, with the definition staying disabled
// across a re-apply of that fetched YAML.
func TestAutomationDefinitionGetWSAfterToggleDerivesFromSpecJSON(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	setClient := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationSetEnabledWS(setClient, &protocol.AutomationSetEnabledMessage{
		Cmd:          protocol.CmdAutomationSetEnabled,
		DefinitionID: def.ID,
		Enabled:      false,
		RequestID:    protocol.Ptr("toggle-off"),
	})
	var setRes protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, setClient.send, &setRes)
	if !setRes.Success || len(setRes.Definitions) != 1 || setRes.Definitions[0].Enabled {
		t.Fatalf("set_enabled result = %+v, want a disabled summary", setRes)
	}

	getClient := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationDefinitionGetWS(getClient, &protocol.AutomationDefinitionGetMessage{
		Cmd:          protocol.CmdAutomationDefinitionGet,
		DefinitionID: def.ID,
		RequestID:    protocol.Ptr("get-after-toggle"),
	})
	var getRes protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, getClient.send, &getRes)
	if !getRes.Success || getRes.SpecYaml == nil {
		t.Fatalf("definition_get after toggle = %+v, want success with spec_yaml", getRes)
	}
	fetched := *getRes.SpecYaml
	if strings.Contains(fetched, "enabled:") {
		t.Fatalf("derived spec_yaml must never carry an enabled key:\n%s", fetched)
	}

	applyClient := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationApplyWS(applyClient, &protocol.AutomationApplyMessage{
		Cmd:              protocol.CmdAutomationApply,
		DefinitionYaml:   fetched,
		ExpectedID:       protocol.Ptr(def.ID),
		ExpectedRevision: getRes.Revision,
		RequestID:        protocol.Ptr("save-after-get"),
	})
	var applyRes protocol.AutomationActionResultMessage
	readNotebookWSEvent(t, applyClient.send, &applyRes)
	if !applyRes.Success {
		t.Fatalf("re-applying the fetched (disabled) yaml failed: %+v", applyRes)
	}

	final, err := s.GetAutomationDefinition(def.ID)
	if err != nil || final == nil || final.Enabled {
		t.Fatalf("definition after re-applying fetched yaml = %#v err=%v, want it to stay disabled (apply never toggles enabled)", final, err)
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

// A validate that passes has no payload to report, and the CLI's success
// output depends on that absence being visible on the wire. json.Marshal of a
// nil `any` yields the 4-byte literal `null` rather than an empty slice, which
// defeats `json:"data,omitempty"` and leaves the client decoding a non-nil
// json.RawMessage — so `attn automation validate` printed `null` instead of
// {"valid": true}. Pinned here on the encoded bytes, because the bug lives in
// the encoding and a decoded struct hides it.
func TestAutomationCommandOmitsDataWhenActionHasNoPayload(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleAutomationCommand(serverConn, protocol.CmdAutomationValidate,
		&protocol.AutomationValidateMessage{Cmd: protocol.CmdAutomationValidate, DefinitionYaml: raw})

	var encoded map[string]any
	if err := json.NewDecoder(clientConn).Decode(&encoded); err != nil {
		t.Fatal(err)
	}
	if success, _ := encoded["success"].(bool); !success {
		t.Fatalf("validate did not succeed: %+v", encoded)
	}
	if _, present := encoded["data"]; present {
		t.Fatalf("a payload-free action put %v on the wire under \"data\"; it must be omitted entirely so the client can tell \"no payload\" from \"payload that is null\"", encoded["data"])
	}
}

// TestAutomationCommandSetEnabledTogglesColumn is the unix-socket-command half
// of the enable/disable CLI verbs (cmd/attn/automation.go's "enable"/
// "disable" cases): CmdAutomationSetEnabled must reach the same
// automationSetEnabled the WS panel's toggle uses, over
// handleAutomationCommand's plain net.Pipe transport, following the same
// technique as TestAutomationCommandOmitsDataWhenActionHasNoPayload.
func TestAutomationCommandSetEnabledTogglesColumn(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !def.Enabled {
		t.Fatalf("fixture definition = %#v, want enabled", def)
	}

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleAutomationCommand(serverConn, protocol.CmdAutomationSetEnabled,
		&protocol.AutomationSetEnabledMessage{Cmd: protocol.CmdAutomationSetEnabled, DefinitionID: def.ID, Enabled: false})

	var encoded map[string]any
	if err := json.NewDecoder(clientConn).Decode(&encoded); err != nil {
		t.Fatal(err)
	}
	if success, _ := encoded["success"].(bool); !success {
		t.Fatalf("automation_set_enabled did not succeed: %+v", encoded)
	}

	stored, err := s.GetAutomationDefinition(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled {
		t.Fatalf("definition after socket automation_set_enabled(false) = %#v, want disabled", stored)
	}
}

// The sibling of the case above, pinned so the omitempty guard is not later
// "simplified" into dropping null payloads generally. GetAutomationDefinition
// returns a typed nil *store.AutomationDefinition, and assigning that to an
// `any` produces a NON-nil interface, so show still marshals to null and its
// output is unchanged. That distinction is the entire reason the guard is
// written as a nil check on the interface rather than on the encoded bytes.
func TestAutomationCommandStillReportsNullForMissingDefinition(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleAutomationCommand(serverConn, protocol.CmdAutomationShow,
		&protocol.AutomationShowMessage{Cmd: protocol.CmdAutomationShow, DefinitionID: "no-such-definition"})

	var encoded map[string]any
	if err := json.NewDecoder(clientConn).Decode(&encoded); err != nil {
		t.Fatal(err)
	}
	value, present := encoded["data"]
	if !present || value != nil {
		t.Fatalf("show of a missing definition = %+v, want an explicit null data field", encoded)
	}
}
