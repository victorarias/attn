package store

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestChiefOfStaffDispatchLifecycle(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	now := string(protocol.TimestampNow())
	dispatch := &protocol.ChiefOfStaffDispatch{
		ID:             "dispatch-1",
		ChiefSessionID: "chief-1",
		SessionID:      "worker-1",
		WorkspaceID:    "workspace-1",
		Brief:          "Investigate the failure.",
		Label:          "Investigate failure",
		Agent:          "codex",
		Directory:      "/tmp/project",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.AddChiefOfStaffDispatch(dispatch); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}

	listed := s.ListChiefOfStaffDispatches("chief-1")
	if len(listed) != 1 || listed[0].SessionID != "worker-1" {
		t.Fatalf("listed dispatches = %+v", listed)
	}
	if other := s.ListChiefOfStaffDispatches("chief-2"); len(other) != 0 {
		t.Fatalf("other chief dispatches = %+v", other)
	}

	updated, err := s.UpdateChiefOfStaffDispatchOutcome("worker-1", "Root cause found.", protocol.DispatchReport{
		ReportType: protocol.DispatchReportTypeProgress,
		WorkState:  protocol.DispatchWorkStateInProgress,
		Summary:    "Root cause found.",
	})
	if err != nil {
		t.Fatalf("update report: %v", err)
	}
	if protocol.Deref(updated.LatestReport) != "Root cause found." || protocol.Deref(updated.ReportedAt) == "" {
		t.Fatalf("updated dispatch = %+v", updated)
	}

	if err := s.DeleteChiefOfStaffDispatch("dispatch-1"); err != nil {
		t.Fatalf("delete dispatch: %v", err)
	}
	if got := s.GetChiefOfStaffDispatchBySession("worker-1"); got != nil {
		t.Fatalf("dispatch after delete = %+v", got)
	}
}

func TestDelegatedFromChiefSessionIDs(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if ids := s.DelegatedFromChiefSessionIDs(); len(ids) != 0 {
		t.Fatalf("empty store delegated set = %+v", ids)
	}

	now := string(protocol.TimestampNow())
	add := func(id, sessionID string) {
		t.Helper()
		if err := s.AddChiefOfStaffDispatch(&protocol.ChiefOfStaffDispatch{
			ID:             id,
			ChiefSessionID: "chief-1",
			SessionID:      sessionID,
			WorkspaceID:    "workspace-1",
			Brief:          "brief",
			Label:          "label",
			Agent:          "codex",
			Directory:      "/tmp/project",
			CreatedAt:      now,
			UpdatedAt:      now,
		}); err != nil {
			t.Fatalf("add dispatch %s: %v", id, err)
		}
	}
	add("dispatch-1", "worker-1")
	add("dispatch-2", "worker-2")

	ids := s.DelegatedFromChiefSessionIDs()
	if len(ids) != 2 || !ids["worker-1"] || !ids["worker-2"] {
		t.Fatalf("delegated set = %+v", ids)
	}
	if ids["never-delegated"] {
		t.Fatalf("unexpected non-delegated session in set: %+v", ids)
	}
}

func TestChiefOfStaffDispatchMessageLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	now := string(protocol.TimestampNow())
	dispatch := &protocol.ChiefOfStaffDispatch{
		ID:             "dispatch-mail",
		ChiefSessionID: "chief-1",
		SessionID:      "worker-1",
		WorkspaceID:    "workspace-1",
		Brief:          "Investigate the failure.",
		Label:          "Investigate failure",
		Agent:          "codex",
		Directory:      "/tmp/project",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.AddChiefOfStaffDispatch(dispatch); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	message := &protocol.DispatchMessage{
		ID:              "message-1",
		DispatchID:      dispatch.ID,
		SenderSessionID: dispatch.ChiefSessionID,
		TargetSessionID: dispatch.SessionID,
		Content:         "Re-check the failure on the current branch.",
		CreatedAt:       now,
	}
	if err := s.AddDispatchMessage(message); err != nil {
		t.Fatalf("add message: %v", err)
	}
	if got, err := s.CountUnreadDispatchMessages(dispatch.ID); err != nil || got != 1 {
		t.Fatalf("unread count = %d, want 1", got)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	s, err = NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	messages, err := s.ListDispatchMessages(dispatch.ID, true)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 1 || messages[0].Content != message.Content {
		t.Fatalf("persisted messages = %+v", messages)
	}
	if _, err := s.MarkDispatchMessageRead(message.ID, "other-dispatch", dispatch.SessionID); err == nil {
		t.Fatal("wrong dispatch read succeeded")
	}
	if got, err := s.CountUnreadDispatchMessages(dispatch.ID); err != nil || got != 1 {
		t.Fatalf("wrong dispatch read changed unread count to %d: %v", got, err)
	}
	read, err := s.MarkDispatchMessageRead(message.ID, dispatch.ID, dispatch.SessionID)
	if err != nil {
		t.Fatalf("mark read: %v", err)
	}
	unreadCount, err := s.CountUnreadDispatchMessages(dispatch.ID)
	if err != nil {
		t.Fatalf("count unread messages: %v", err)
	}
	if read.ReadAt == nil || unreadCount != 0 {
		t.Fatalf("read message = %+v", read)
	}
	acknowledged, err := s.AcknowledgeDispatchMessage(message.ID, dispatch.ID, dispatch.SessionID, "Re-check complete.")
	if err != nil {
		t.Fatalf("acknowledge: %v", err)
	}
	if acknowledged.AcknowledgedAt == nil ||
		protocol.Deref(acknowledged.Acknowledgement) != "Re-check complete." {
		t.Fatalf("acknowledged message = %+v", acknowledged)
	}
	acknowledged, err = s.AcknowledgeDispatchMessage(message.ID, dispatch.ID, dispatch.SessionID, "")
	if err != nil {
		t.Fatalf("acknowledge without text: %v", err)
	}
	if acknowledged.Acknowledgement != nil {
		t.Fatalf("empty acknowledgement retained stale text: %+v", acknowledged)
	}
	if _, err := s.MarkDispatchMessageRead(message.ID, dispatch.ID, "other-worker"); err == nil {
		t.Fatal("wrong worker read succeeded")
	}
}

func TestChiefOfStaffStructuredReportPersistsAndResolves(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	now := string(protocol.TimestampNow())
	dispatch := &protocol.ChiefOfStaffDispatch{
		ID:             "dispatch-structured",
		ChiefSessionID: "chief-1",
		SessionID:      "worker-1",
		WorkspaceID:    "workspace-1",
		Brief:          "Implement the feature.",
		Label:          "Feature",
		Agent:          "codex",
		Directory:      "/tmp/project",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.AddChiefOfStaffDispatch(dispatch); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}

	report := &protocol.DispatchReport{
		ReportType: protocol.DispatchReportTypeBlocker,
		Summary:    "Core implementation ready locally",
		WorkState:  protocol.DispatchWorkStateNeedsInput,
		NextActor:  protocol.Ptr("team"),
		NextAction: protocol.Ptr("Decide the event contract"),
		RemainingScope: []string{
			"Emit the event",
			"Add the integration test",
		},
		Constraints: []string{"uncommitted", "no push", "no PR"},
		Request: &protocol.DispatchDecisionRequest{
			Question:          "Which event contract should be used?",
			Recommendation:    protocol.Ptr("Use AisNoOperationV1"),
			Consequence:       protocol.Ptr("Event emission remains blocked"),
			ExpectedResponder: "team",
			Status:            protocol.DispatchRequestStatusPending,
		},
		Artifact: &protocol.DispatchArtifact{
			Identity: "dirty:abc123",
			Branch:   protocol.Ptr("feat/work"),
			Dirty:    protocol.Ptr(true),
		},
		Verification: []protocol.DispatchVerification{
			{
				Actor:            "agent",
				Target:           "go test ./internal/feature",
				Result:           "passed",
				Timestamp:        now,
				ArtifactIdentity: "dirty:abc123",
			},
			{
				Actor:            "chief",
				Target:           "go test ./internal/feature",
				Result:           "passed on prior revision",
				Timestamp:        now,
				ArtifactIdentity: "commit:old",
			},
		},
	}
	updated, err := s.UpdateChiefOfStaffDispatchOutcome(
		"worker-1",
		"Core implementation is ready; event emission needs a decision.",
		*report,
	)
	if err != nil {
		t.Fatalf("update structured report: %v", err)
	}
	if updated.StructuredReport == nil || updated.StructuredReport.ReportedAt == "" {
		t.Fatalf("structured report = %+v", updated.StructuredReport)
	}
	if !protocol.Deref(updated.StructuredReport.Verification[0].Current) {
		t.Fatalf("matching verification not current: %+v", updated.StructuredReport.Verification)
	}
	if protocol.Deref(updated.StructuredReport.Verification[1].Current) {
		t.Fatalf("mismatched verification current: %+v", updated.StructuredReport.Verification)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	s, err = NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	persisted := s.GetChiefOfStaffDispatchBySession("worker-1")
	if persisted == nil || persisted.StructuredReport == nil {
		t.Fatalf("persisted dispatch = %+v", persisted)
	}
	if persisted.StructuredReport.Summary != "Core implementation ready locally" {
		t.Fatalf("persisted report = %+v", persisted.StructuredReport)
	}

	resolved, err := s.ResolveChiefOfStaffDispatchRequest(
		"dispatch-structured",
		"chief-1",
		"Use AisNoOperationV1.",
		"https://example.test/decision",
	)
	if err != nil {
		t.Fatalf("resolve request: %v", err)
	}
	request := resolved.StructuredReport.Request
	if request.Status != protocol.DispatchRequestStatusResolved ||
		protocol.Deref(request.Response) != "Use AisNoOperationV1." ||
		protocol.Deref(request.ResolutionLink) != "https://example.test/decision" ||
		protocol.Deref(request.RespondedBy) != "chief-1" {
		t.Fatalf("resolved request = %+v", request)
	}
	if _, err := s.ResolveChiefOfStaffDispatchRequest(
		"dispatch-structured",
		"chief-other",
		"Wrong owner",
		"",
	); err == nil {
		t.Fatal("resolve with wrong chief succeeded")
	}
}

func TestChiefOfStaffDispatchClosedStateCapture(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	now := string(protocol.TimestampNow())
	dispatch := &protocol.ChiefOfStaffDispatch{
		ID:             "dispatch-close",
		ChiefSessionID: "chief-1",
		SessionID:      "worker-1",
		WorkspaceID:    "workspace-1",
		Brief:          "Do the work.",
		Label:          "Work",
		Agent:          "codex",
		Directory:      "/tmp/project",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.AddChiefOfStaffDispatch(dispatch); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}

	// A fresh dispatch has no captured close-state.
	if got := s.GetChiefOfStaffDispatchBySession("worker-1"); got == nil || got.ClosedState != nil {
		t.Fatalf("fresh dispatch close-state = %+v", got)
	}

	// An untracked session is a silent no-op (changed=false, no error) — most
	// session exits are not dispatches.
	if _, changed, err := s.SetChiefOfStaffDispatchClosedStateBySession("not-a-worker", "working"); err != nil || changed {
		t.Fatalf("untracked set: changed=%v err=%v", changed, err)
	}

	// Empty inputs are rejected.
	if _, _, err := s.SetChiefOfStaffDispatchClosedStateBySession("", "working"); err == nil {
		t.Fatal("empty session id accepted")
	}
	if _, _, err := s.SetChiefOfStaffDispatchClosedStateBySession("worker-1", "  "); err == nil {
		t.Fatal("empty close-state accepted")
	}

	// The first capture wins and is recorded.
	updated, changed, err := s.SetChiefOfStaffDispatchClosedStateBySession("worker-1", "working")
	if err != nil || !changed {
		t.Fatalf("first capture: changed=%v err=%v", changed, err)
	}
	if protocol.Deref(updated.ClosedState) != "working" {
		t.Fatalf("first capture close-state = %+v", updated.ClosedState)
	}

	// A later capture must NOT overwrite (first-writer-wins): a teardown read that
	// sees the clobbered idle cannot erase the true mid-flight close-state.
	again, changed, err := s.SetChiefOfStaffDispatchClosedStateBySession("worker-1", "idle")
	if err != nil {
		t.Fatalf("second capture err: %v", err)
	}
	if changed || again != nil {
		t.Fatalf("second capture overwrote: changed=%v dispatch=%+v", changed, again)
	}
	if got := s.GetChiefOfStaffDispatchBySession("worker-1"); protocol.Deref(got.ClosedState) != "working" {
		t.Fatalf("close-state after second capture = %+v", got.ClosedState)
	}

	// The close-state survives a reopen (durable across a daemon restart).
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	s, err = NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if got := s.GetChiefOfStaffDispatchBySession("worker-1"); got == nil || protocol.Deref(got.ClosedState) != "working" {
		t.Fatalf("persisted close-state = %+v", got)
	}
}

func TestChiefOfStaffDispatchClosedStateInMemory(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	now := string(protocol.TimestampNow())
	if err := s.AddChiefOfStaffDispatch(&protocol.ChiefOfStaffDispatch{
		ID:             "d1",
		ChiefSessionID: "chief",
		SessionID:      "w1",
		WorkspaceID:    "ws",
		Brief:          "b",
		Label:          "l",
		Agent:          "codex",
		Directory:      "/tmp",
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	if _, changed, err := s.SetChiefOfStaffDispatchClosedStateBySession("w1", "pending_approval"); err != nil || !changed {
		t.Fatalf("first capture: changed=%v err=%v", changed, err)
	}
	if _, changed, _ := s.SetChiefOfStaffDispatchClosedStateBySession("w1", "idle"); changed {
		t.Fatal("in-memory close-state overwrite happened (must be first-writer-wins)")
	}
	if got := s.GetChiefOfStaffDispatchBySession("w1"); protocol.Deref(got.ClosedState) != "pending_approval" {
		t.Fatalf("in-memory close-state = %+v", got.ClosedState)
	}
}
