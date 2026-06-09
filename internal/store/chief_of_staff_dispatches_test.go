package store

import (
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
