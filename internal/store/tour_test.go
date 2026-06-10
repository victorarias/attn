package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestTourEmptyCollectionsMarshalAsArrays(t *testing.T) {
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
		Files: []protocol.TourFile{{
			Path:   "main.go",
			Group:  "tour",
			View:   "diff",
			Status: "modified",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(payload)
	for _, field := range []string{
		`"warnings":[]`,
		`"drafts":[]`,
		`"transcript":[]`,
		`"annotations":[]`,
	} {
		if !strings.Contains(encoded, field) {
			t.Fatalf("tour payload missing %s: %s", field, encoded)
		}
	}
}

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
	createdUpdatedAt, err := time.Parse(time.RFC3339Nano, run.UpdatedAt)
	if err != nil {
		t.Fatalf("parse created updated_at: %v", err)
	}
	run, err = s.SaveTourDraft(run.TourID, "main.go", true, "Looks good",
		[]protocol.TourDraftText{{ID: "ann-1", Body: "Why?"}},
		[]protocol.TourLineComment{{Line: 12, Body: "Check this."}},
	)
	if err != nil {
		t.Fatalf("SaveTourDraft() error = %v", err)
	}
	draftUpdatedAt, err := time.Parse(time.RFC3339Nano, run.UpdatedAt)
	if err != nil {
		t.Fatalf("parse draft updated_at: %v", err)
	}
	if !draftUpdatedAt.After(createdUpdatedAt) {
		t.Fatalf("draft updated_at = %q, want after creation", run.UpdatedAt)
	}
	context := &protocol.TourQuestionContext{Source: "tour", Path: "main.go", LineStart: protocol.Ptr(12)}
	event, _, err := s.AddTourEvent(run.TourID, "question", "Question", false, context)
	if err != nil {
		t.Fatalf("AddTourEvent() error = %v", err)
	}
	afterEvent, err := s.GetTourByID(run.TourID)
	if err != nil {
		t.Fatalf("GetTourByID(after event) error = %v", err)
	}
	initialUpdatedAt, err := time.Parse(time.RFC3339Nano, run.UpdatedAt)
	if err != nil {
		t.Fatalf("parse initial updated_at: %v", err)
	}
	eventUpdatedAt, err := time.Parse(time.RFC3339Nano, afterEvent.UpdatedAt)
	if err != nil {
		t.Fatalf("parse event updated_at: %v", err)
	}
	if !eventUpdatedAt.After(initialUpdatedAt) {
		t.Fatalf("event updated_at = %q, want after %q", afterEvent.UpdatedAt, run.UpdatedAt)
	}
	afterTranscript, err := s.AddTourTranscript(run.TourID, "user", "Why?", &event.ID, context)
	if err != nil {
		t.Fatalf("AddTourTranscript(user) error = %v", err)
	}
	transcriptUpdatedAt, err := time.Parse(time.RFC3339Nano, afterTranscript.UpdatedAt)
	if err != nil {
		t.Fatalf("parse transcript updated_at: %v", err)
	}
	if !transcriptUpdatedAt.After(eventUpdatedAt) {
		t.Fatalf("transcript updated_at = %q, want after %q", afterTranscript.UpdatedAt, afterEvent.UpdatedAt)
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
	second, err := s.CreateOrOpenTour("session-1", "Two", "/repo", "/system/two.yml", "main", TourSnapshot{})
	if err != nil {
		t.Fatalf("create historical successor: %v", err)
	}
	firstCreatedAt, err := time.Parse(time.RFC3339Nano, first.CreatedAt)
	if err != nil {
		t.Fatalf("parse first created_at: %v", err)
	}
	secondCreatedAt, err := time.Parse(time.RFC3339Nano, second.CreatedAt)
	if err != nil {
		t.Fatalf("parse second created_at: %v", err)
	}
	if !secondCreatedAt.After(firstCreatedAt) {
		t.Fatalf("second created_at = %q, want after %q", second.CreatedAt, first.CreatedAt)
	}
}

func TestTourSuccessorUsesChronologicallyLatestHistoricalTimestamp(t *testing.T) {
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

	for _, historical := range []struct {
		id        string
		timestamp string
	}{
		{id: "historical-1", timestamp: "2999-01-01T00:00:00.9Z"},
		{id: "historical-2", timestamp: "2999-01-01T00:00:00.91Z"},
	} {
		if _, err := s.db.Exec(`
			INSERT INTO tour_runs (
				id, session_id, name, repo_path, guide_path, base_ref, status,
				summary, warnings_json, files_json, created_at, updated_at, ended_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, '', '[]', '[]', ?, ?, ?)
		`, historical.id, "session-1", historical.id, "/repo", "/system/guide.yml", "main",
			string(protocol.TourStatusEnded), historical.timestamp, historical.timestamp, historical.timestamp); err != nil {
			t.Fatalf("insert %s: %v", historical.id, err)
		}
	}

	run, err := s.CreateOrOpenTour("session-1", "Successor", "/repo", "/system/next.yml", "main", TourSnapshot{})
	if err != nil {
		t.Fatalf("CreateOrOpenTour() error = %v", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, run.CreatedAt)
	if err != nil {
		t.Fatalf("parse successor created_at: %v", err)
	}
	latestHistorical, err := time.Parse(time.RFC3339Nano, "2999-01-01T00:00:00.91Z")
	if err != nil {
		t.Fatal(err)
	}
	if !createdAt.After(latestHistorical) {
		t.Fatalf("successor created_at = %q, want after chronologically latest %q", run.CreatedAt, latestHistorical)
	}
	if run.ConnectionState != protocol.TourConnectionStateConnected {
		t.Fatalf("successor connection state = %q, want connected", run.ConnectionState)
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
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`UPDATE tour_runs SET listener_last_seen = ? WHERE id = ?`, future, run.TourID); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.GetTourByID(run.TourID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ConnectionState != protocol.TourConnectionStateDisconnected {
		t.Fatalf("future listener heartbeat connection state = %q", loaded.ConnectionState)
	}
}

func TestTourListenerTouchSeparatesWallClockFromMutationClock(t *testing.T) {
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
	futureMutation := "2999-01-01T00:00:00.91Z"
	if _, err := s.db.Exec(`UPDATE tour_runs SET updated_at = ? WHERE id = ?`, futureMutation, run.TourID); err != nil {
		t.Fatal(err)
	}

	touched, err := s.TouchTourListener(run.TourID, 1)
	if err != nil {
		t.Fatalf("TouchTourListener() error = %v", err)
	}
	var listenerLastSeen, updatedAt string
	if err := s.db.QueryRow(`
		SELECT listener_last_seen, updated_at FROM tour_runs WHERE id = ?
	`, run.TourID).Scan(&listenerLastSeen, &updatedAt); err != nil {
		t.Fatal(err)
	}
	seen, err := time.Parse(time.RFC3339Nano, listenerLastSeen)
	if err != nil {
		t.Fatalf("parse listener_last_seen: %v", err)
	}
	mutation, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		t.Fatalf("parse updated_at: %v", err)
	}
	future, err := time.Parse(time.RFC3339Nano, futureMutation)
	if err != nil {
		t.Fatal(err)
	}
	if seen.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("listener_last_seen = %q, want wall-clock heartbeat", listenerLastSeen)
	}
	if !mutation.After(future) {
		t.Fatalf("updated_at = %q, want after logical clock %q", updatedAt, futureMutation)
	}
	if touched.ConnectionState != protocol.TourConnectionStateConnected {
		t.Fatalf("connection state = %q, want connected", touched.ConnectionState)
	}
	_, afterEvent, err := s.AddTourEvent(run.TourID, "feedback", "Review", false, nil)
	if err != nil {
		t.Fatalf("AddTourEvent() error = %v", err)
	}
	if afterEvent.ConnectionState != protocol.TourConnectionStateConnected {
		t.Fatalf("event response connection state = %q, want connected", afterEvent.ConnectionState)
	}
	afterSnapshot, err := s.UpdateTourSnapshot(run.TourID, TourSnapshot{Summary: "Refreshed"})
	if err != nil {
		t.Fatalf("UpdateTourSnapshot() error = %v", err)
	}
	if afterSnapshot.ConnectionState != protocol.TourConnectionStateConnected {
		t.Fatalf("snapshot response connection state = %q, want connected", afterSnapshot.ConnectionState)
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
	second, afterSecond, err := s.AddTourEvent(run.TourID, "feedback", "Second", false, nil)
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
	beforeTouch, err := time.Parse(time.RFC3339Nano, afterSecond.UpdatedAt)
	if err != nil {
		t.Fatalf("parse before touch updated_at: %v", err)
	}
	afterTouch, err := time.Parse(time.RFC3339Nano, resumed.UpdatedAt)
	if err != nil {
		t.Fatalf("parse after touch updated_at: %v", err)
	}
	if !afterTouch.After(beforeTouch) {
		t.Fatalf("listener updated_at = %q, want after %q", resumed.UpdatedAt, afterSecond.UpdatedAt)
	}
	next, err := s.NextTourEvent(run.TourID, resumed.ListenerEventSeq)
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || next.ID != second.ID {
		t.Fatalf("next event = %+v, want %s", next, second.ID)
	}
}
