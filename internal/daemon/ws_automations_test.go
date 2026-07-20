package daemon

import (
	"fmt"
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
