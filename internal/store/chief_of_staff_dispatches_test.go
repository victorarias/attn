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

	updated, err := s.UpdateChiefOfStaffDispatchReport("worker-1", "Root cause found.")
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
	updated, err := s.UpdateChiefOfStaffDispatchReportEnvelope(
		"worker-1",
		"Core implementation is ready; event emission needs a decision.",
		report,
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
