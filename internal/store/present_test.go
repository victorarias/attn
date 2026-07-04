package store

import (
	"path/filepath"
	"testing"
	"time"
)

func newPresentTestStore(t *testing.T) *Store {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPresentCreatePresentationAndRounds(t *testing.T) {
	s := newPresentTestStore(t)
	now := time.Now()

	p, err := s.CreatePresentation("session-1", nil, "My PR", "pr", "/repo", now)
	if err != nil {
		t.Fatalf("CreatePresentation: %v", err)
	}
	if p.ID == "" {
		t.Fatal("expected non-empty presentation ID")
	}
	if p.Status != "open" {
		t.Errorf("expected status 'open', got %q", p.Status)
	}

	round1, err := s.CreatePresentationRound(p.ID, "manifest: v1", "base1", "head1", now)
	if err != nil {
		t.Fatalf("CreatePresentationRound (1): %v", err)
	}
	if round1.Seq != 1 {
		t.Errorf("expected first round seq=1, got %d", round1.Seq)
	}

	round2, err := s.CreatePresentationRound(p.ID, "manifest: v2", "base2", "head2", now)
	if err != nil {
		t.Fatalf("CreatePresentationRound (2): %v", err)
	}
	if round2.Seq != 2 {
		t.Errorf("expected second round seq=2, got %d", round2.Seq)
	}
}

func TestPresentTicketIDPointer(t *testing.T) {
	s := newPresentTestStore(t)
	now := time.Now()

	ticketID := "ticket-42"
	p, err := s.CreatePresentation("session-1", &ticketID, "Ticket Present", "ticket", "/repo", now)
	if err != nil {
		t.Fatalf("CreatePresentation: %v", err)
	}
	if p.TicketID == nil || *p.TicketID != ticketID {
		t.Fatalf("expected ticket ID %q, got %v", ticketID, p.TicketID)
	}

	got, err := s.GetPresentation(p.ID)
	if err != nil {
		t.Fatalf("GetPresentation: %v", err)
	}
	if got.TicketID == nil || *got.TicketID != ticketID {
		t.Fatalf("expected fetched ticket ID %q, got %v", ticketID, got.TicketID)
	}
}

func TestPresentLatestRoundEnrichment(t *testing.T) {
	s := newPresentTestStore(t)
	now := time.Now()

	p, err := s.CreatePresentation("session-1", nil, "Title", "pr", "/repo", now)
	if err != nil {
		t.Fatalf("CreatePresentation: %v", err)
	}

	// No rounds yet.
	got, err := s.GetPresentation(p.ID)
	if err != nil {
		t.Fatalf("GetPresentation: %v", err)
	}
	if got.LatestRoundSeq != 0 || got.LatestRoundSubmitted {
		t.Errorf("expected no latest round yet, got seq=%d submitted=%v", got.LatestRoundSeq, got.LatestRoundSubmitted)
	}

	round1, err := s.CreatePresentationRound(p.ID, "manifest: v1", "base1", "head1", now)
	if err != nil {
		t.Fatalf("CreatePresentationRound: %v", err)
	}

	got, err = s.GetPresentation(p.ID)
	if err != nil {
		t.Fatalf("GetPresentation: %v", err)
	}
	if got.LatestRoundSeq != 1 || got.LatestRoundSubmitted {
		t.Errorf("expected latest round seq=1 unsubmitted, got seq=%d submitted=%v", got.LatestRoundSeq, got.LatestRoundSubmitted)
	}

	if err := s.SubmitPresentationRound(round1.ID, nil, now); err != nil {
		t.Fatalf("SubmitPresentationRound: %v", err)
	}

	got, err = s.GetPresentation(p.ID)
	if err != nil {
		t.Fatalf("GetPresentation: %v", err)
	}
	if !got.LatestRoundSubmitted {
		t.Error("expected latest round to be submitted")
	}

	round2, err := s.CreatePresentationRound(p.ID, "manifest: v2", "base2", "head2", now)
	if err != nil {
		t.Fatalf("CreatePresentationRound (2): %v", err)
	}

	got, err = s.GetPresentation(p.ID)
	if err != nil {
		t.Fatalf("GetPresentation: %v", err)
	}
	if got.LatestRoundSeq != round2.Seq || got.LatestRoundSubmitted {
		t.Errorf("expected latest round seq=%d unsubmitted, got seq=%d submitted=%v", round2.Seq, got.LatestRoundSeq, got.LatestRoundSubmitted)
	}

	// ListPresentations should show the same enrichment.
	list, err := s.ListPresentations()
	if err != nil {
		t.Fatalf("ListPresentations: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 presentation, got %d", len(list))
	}
	if list[0].LatestRoundSeq != round2.Seq || list[0].LatestRoundSubmitted {
		t.Errorf("ListPresentations enrichment mismatch: seq=%d submitted=%v", list[0].LatestRoundSeq, list[0].LatestRoundSubmitted)
	}
}

func TestPresentListPresentationsNewestFirst(t *testing.T) {
	s := newPresentTestStore(t)
	now := time.Now()

	p1, err := s.CreatePresentation("session-1", nil, "First", "pr", "/repo", now)
	if err != nil {
		t.Fatalf("CreatePresentation: %v", err)
	}
	p2, err := s.CreatePresentation("session-1", nil, "Second", "pr", "/repo", now.Add(time.Second))
	if err != nil {
		t.Fatalf("CreatePresentation: %v", err)
	}

	list, err := s.ListPresentations()
	if err != nil {
		t.Fatalf("ListPresentations: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 presentations, got %d", len(list))
	}
	if list[0].ID != p2.ID || list[1].ID != p1.ID {
		t.Errorf("expected newest first order [%s, %s], got [%s, %s]", p2.ID, p1.ID, list[0].ID, list[1].ID)
	}
}

func TestPresentGetPresentationRoundLatestVsExplicit(t *testing.T) {
	s := newPresentTestStore(t)
	now := time.Now()

	p, err := s.CreatePresentation("session-1", nil, "Title", "pr", "/repo", now)
	if err != nil {
		t.Fatalf("CreatePresentation: %v", err)
	}

	round1, err := s.CreatePresentationRound(p.ID, "manifest: v1", "base1", "head1", now)
	if err != nil {
		t.Fatalf("CreatePresentationRound: %v", err)
	}
	round2, err := s.CreatePresentationRound(p.ID, "manifest: v2", "base2", "head2", now)
	if err != nil {
		t.Fatalf("CreatePresentationRound: %v", err)
	}

	latest, err := s.GetPresentationRound(p.ID, 0)
	if err != nil {
		t.Fatalf("GetPresentationRound(seq=0): %v", err)
	}
	if latest.ID != round2.ID {
		t.Errorf("expected latest round to be round2, got %s", latest.ID)
	}

	latestNeg, err := s.GetPresentationRound(p.ID, -1)
	if err != nil {
		t.Fatalf("GetPresentationRound(seq=-1): %v", err)
	}
	if latestNeg.ID != round2.ID {
		t.Errorf("expected negative seq to also mean latest, got %s", latestNeg.ID)
	}

	explicit, err := s.GetPresentationRound(p.ID, 1)
	if err != nil {
		t.Fatalf("GetPresentationRound(seq=1): %v", err)
	}
	if explicit.ID != round1.ID {
		t.Errorf("expected explicit seq=1 to be round1, got %s", explicit.ID)
	}

	if _, err := s.GetPresentationRound(p.ID, 99); err == nil {
		t.Error("expected error for nonexistent seq")
	}
}

func TestPresentGetPresentationRoundByID(t *testing.T) {
	s := newPresentTestStore(t)
	now := time.Now()

	p, err := s.CreatePresentation("session-1", nil, "Title", "pr", "/repo", now)
	if err != nil {
		t.Fatalf("CreatePresentation: %v", err)
	}
	round, err := s.CreatePresentationRound(p.ID, "manifest: v1", "base1", "head1", now)
	if err != nil {
		t.Fatalf("CreatePresentationRound: %v", err)
	}

	got, err := s.GetPresentationRoundByID(round.ID)
	if err != nil {
		t.Fatalf("GetPresentationRoundByID: %v", err)
	}
	if got.PresentationID != p.ID || got.Seq != round.Seq {
		t.Errorf("GetPresentationRoundByID() = %+v, want presentation_id=%s seq=%d", got, p.ID, round.Seq)
	}

	if _, err := s.GetPresentationRoundByID("no-such-round"); err == nil {
		t.Error("expected error for nonexistent round id")
	}
}

func TestPresentSubmitRoundStoresCommentsAndSetsSubmittedAt(t *testing.T) {
	s := newPresentTestStore(t)
	now := time.Now()

	p, err := s.CreatePresentation("session-1", nil, "Title", "pr", "/repo", now)
	if err != nil {
		t.Fatalf("CreatePresentation: %v", err)
	}
	round, err := s.CreatePresentationRound(p.ID, "manifest: v1", "base1", "head1", now)
	if err != nil {
		t.Fatalf("CreatePresentationRound: %v", err)
	}
	if round.SubmittedAt != nil {
		t.Fatal("expected new round to be unsubmitted")
	}

	comments := []PresentationComment{
		{Filepath: "b.go", LineStart: 10, LineEnd: 10, Side: "right", Content: "b comment"},
		{Filepath: "a.go", LineStart: 20, LineEnd: 20, Side: "left", Content: "a comment, no author"},
		{Filepath: "a.go", LineStart: 5, LineEnd: 5, Side: "right", Content: "a comment earlier line", Author: "agent"},
	}

	if err := s.SubmitPresentationRound(round.ID, comments, now); err != nil {
		t.Fatalf("SubmitPresentationRound: %v", err)
	}

	got, err := s.GetPresentationRound(p.ID, round.Seq)
	if err != nil {
		t.Fatalf("GetPresentationRound: %v", err)
	}
	if got.SubmittedAt == nil || *got.SubmittedAt == "" {
		t.Fatal("expected round to be marked submitted")
	}

	all, err := s.ListPresentationComments(round.ID)
	if err != nil {
		t.Fatalf("ListPresentationComments: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 comments, got %d", len(all))
	}

	// Default author applied when empty.
	for _, c := range all {
		if c.Filepath == "a.go" && c.Content == "a comment, no author" && c.Author != "user" {
			t.Errorf("expected default author 'user', got %q", c.Author)
		}
	}

	// Ordering: filepath, then line_start.
	if all[0].Filepath != "a.go" || all[0].LineStart != 5 {
		t.Errorf("expected first comment a.go:5, got %s:%d", all[0].Filepath, all[0].LineStart)
	}
	if all[1].Filepath != "a.go" || all[1].LineStart != 20 {
		t.Errorf("expected second comment a.go:20, got %s:%d", all[1].Filepath, all[1].LineStart)
	}
	if all[2].Filepath != "b.go" || all[2].LineStart != 10 {
		t.Errorf("expected third comment b.go:10, got %s:%d", all[2].Filepath, all[2].LineStart)
	}

	// Double-submit should error.
	if err := s.SubmitPresentationRound(round.ID, nil, now); err == nil {
		t.Error("expected error submitting an already-submitted round")
	}
}

func TestPresentSubmitRoundMissingRoundErrors(t *testing.T) {
	s := newPresentTestStore(t)
	if err := s.SubmitPresentationRound("does-not-exist", nil, time.Now()); err == nil {
		t.Error("expected error submitting a nonexistent round")
	}
}
