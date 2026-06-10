package store

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestTourPersistsDraftTranscriptAndEvents(t *testing.T) {
	s := New()
	defer s.Close()
	s.Add(&protocol.Session{
		ID:             "session-1",
		Label:          "session",
		Agent:          "codex",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     string(protocol.TimestampNow()),
		StateUpdatedAt: string(protocol.TimestampNow()),
		LastSeen:       string(protocol.TimestampNow()),
	})

	run, err := s.CreateOrOpenTour("session-1", "Tour", "/repo", "/system/guide.yml", "main", TourSnapshot{
		Summary: "Summary",
		Files: []protocol.TourFile{{
			Path:        "main.go",
			Group:       "tour",
			View:        "diff",
			Status:      "modified",
			Annotations: []protocol.TourAnnotation{},
		}},
	})
	if err != nil {
		t.Fatalf("CreateOrOpenTour() error = %v", err)
	}
	run, err = s.SaveTourDraft(run.TourID, "main.go", true, "Looks good",
		[]protocol.TourDraftText{{ID: "ann-1", Body: "Why?"}},
		[]protocol.TourLineComment{{Line: 12, Body: "Check this."}},
	)
	if err != nil {
		t.Fatalf("SaveTourDraft() error = %v", err)
	}
	context := &protocol.TourQuestionContext{Source: "tour", Path: "main.go", LineStart: protocol.Ptr(12)}
	event, _, err := s.AddTourEvent(run.TourID, "question", "Question", false, context)
	if err != nil {
		t.Fatalf("AddTourEvent() error = %v", err)
	}
	if _, err := s.AddTourTranscript(run.TourID, "user", "Why?", &event.ID, context); err != nil {
		t.Fatalf("AddTourTranscript(user) error = %v", err)
	}
	if _, err := s.AddTourTranscript(run.TourID, "agent", "Because.", &event.ID, nil); err != nil {
		t.Fatalf("AddTourTranscript(agent) error = %v", err)
	}

	loaded, err := s.GetTourByID(run.TourID)
	if err != nil {
		t.Fatalf("GetTourByID() error = %v", err)
	}
	if len(loaded.Drafts) != 1 || !loaded.Drafts[0].Reviewed || len(loaded.Drafts[0].LineComments) != 1 {
		t.Fatalf("drafts = %+v", loaded.Drafts)
	}
	if len(loaded.Transcript) != 2 || loaded.Transcript[1].Body != "Because." {
		t.Fatalf("transcript = %+v", loaded.Transcript)
	}
	next, err := s.NextTourEvent(run.TourID, 0)
	if err != nil || next == nil || next.ID != event.ID {
		t.Fatalf("NextTourEvent() = %+v, %v", next, err)
	}
}

func TestTourEnforcesOneActiveRunPerSessionAndAllowsHistory(t *testing.T) {
	s := New()
	defer s.Close()
	s.Add(&protocol.Session{
		ID:             "session-1",
		Label:          "session",
		Agent:          "codex",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     string(protocol.TimestampNow()),
		StateUpdatedAt: string(protocol.TimestampNow()),
		LastSeen:       string(protocol.TimestampNow()),
	})
	first, err := s.CreateOrOpenTour("session-1", "One", "/repo", "/system/one.yml", "main", TourSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOrOpenTour("session-1", "Two", "/repo", "/system/two.yml", "main", TourSnapshot{}); err == nil || !strings.Contains(err.Error(), "active tour") {
		t.Fatalf("second active tour error = %v", err)
	}
	if _, _, err := s.AddTourEvent(first.TourID, "finish", "", true, nil); err != nil {
		t.Fatalf("finish first tour: %v", err)
	}
	if _, err := s.CreateOrOpenTour("session-1", "Two", "/repo", "/system/two.yml", "main", TourSnapshot{}); err != nil {
		t.Fatalf("create historical successor: %v", err)
	}
}

func TestTourListenerLeaseBecomesDisconnected(t *testing.T) {
	s := New()
	defer s.Close()
	s.Add(&protocol.Session{
		ID:             "session-1",
		Label:          "session",
		Agent:          "codex",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     string(protocol.TimestampNow()),
		StateUpdatedAt: string(protocol.TimestampNow()),
		LastSeen:       string(protocol.TimestampNow()),
	})
	run, err := s.CreateOrOpenTour("session-1", "Tour", "/repo", "/system/guide.yml", "main", TourSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-tourListenerLease - time.Second).Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`UPDATE tour_runs SET listener_last_seen = ? WHERE id = ?`, old, run.TourID); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetTourByID(run.TourID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ConnectionState != protocol.TourConnectionStateDisconnected {
		t.Fatalf("connection state = %q", loaded.ConnectionState)
	}
}

func TestTourListenerCursorResumesAfterDeliveredEvents(t *testing.T) {
	s := New()
	defer s.Close()
	s.Add(&protocol.Session{
		ID:             "session-1",
		Label:          "session",
		Agent:          "codex",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     string(protocol.TimestampNow()),
		StateUpdatedAt: string(protocol.TimestampNow()),
		LastSeen:       string(protocol.TimestampNow()),
	})
	run, err := s.CreateOrOpenTour("session-1", "Tour", "/repo", "/system/guide.yml", "main", TourSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := s.AddTourEvent(run.TourID, "question", "First", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.AddTourEvent(run.TourID, "feedback", "Second", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := s.TouchTourListener(run.TourID, first.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ListenerEventSeq != first.Seq {
		t.Fatalf("listener event seq = %d, want %d", resumed.ListenerEventSeq, first.Seq)
	}
	next, err := s.NextTourEvent(run.TourID, resumed.ListenerEventSeq)
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || next.ID != second.ID {
		t.Fatalf("next event = %+v, want %s", next, second.ID)
	}
}
