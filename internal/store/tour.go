package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
)

const tourListenerLease = 35 * time.Second

type TourSnapshot struct {
	Summary  string
	Warnings []string
	Files    []protocol.TourFile
}

func (s *Store) CreateOrOpenTour(
	sessionID, name, repoPath, guidePath, baseRef string,
	snapshot TourSnapshot,
) (*protocol.TourRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	wallNow := time.Now().UTC()
	mutationTime := wallNow
	existing, err := s.getActiveTourBySession(sessionID, wallNow)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if existing != nil {
		if existing.GuidePath != guidePath {
			return nil, fmt.Errorf("session already has an active tour")
		}
		mutationTime = nextTourMutationTime(existing.UpdatedAt)
		if err := s.updateTourSnapshot(existing.TourID, name, repoPath, guidePath, baseRef, snapshot, wallNow, mutationTime); err != nil {
			return nil, err
		}
		return s.getTourByID(existing.TourID, wallNow)
	}

	latestUpdatedAt, ok, err := s.latestTourUpdatedAt(sessionID)
	if err != nil {
		return nil, err
	}
	if ok {
		mutationTime = nextTourMutationTime(latestUpdatedAt.Format(time.RFC3339Nano))
	}
	warningsJSON, filesJSON, err := encodeTourSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	mutationTS := mutationTime.Format(time.RFC3339Nano)
	_, err = s.db.Exec(`
		INSERT INTO tour_runs (
			id, session_id, name, repo_path, guide_path, base_ref, status,
			summary, warnings_json, files_json, listener_last_seen, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, sessionID, name, repoPath, guidePath, baseRef, string(protocol.TourStatusActive),
		snapshot.Summary, warningsJSON, filesJSON, wallNow.Format(time.RFC3339Nano), mutationTS, mutationTS)
	if err != nil {
		return nil, fmt.Errorf("create tour: %w", err)
	}
	return s.getTourByID(id, wallNow)
}

func (s *Store) UpdateTourSnapshot(tourID string, snapshot TourSnapshot) (*protocol.TourRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	wallNow := time.Now().UTC()
	current, err := s.getTourByID(tourID, wallNow)
	if err != nil {
		return nil, err
	}
	if current.Status != protocol.TourStatusActive {
		return nil, fmt.Errorf("tour has ended")
	}
	mutationTime := nextTourMutationTime(current.UpdatedAt)
	if err := s.updateTourSnapshot(
		tourID,
		current.Name,
		current.RepoPath,
		current.GuidePath,
		current.BaseRef,
		snapshot,
		wallNow,
		mutationTime,
	); err != nil {
		return nil, err
	}
	return s.getTourByID(tourID, wallNow)
}

func (s *Store) GetActiveTourBySession(sessionID string) (*protocol.TourRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tour, err := s.getActiveTourBySession(sessionID, time.Now().UTC())
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return tour, err
}

func (s *Store) GetTourByID(tourID string) (*protocol.TourRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getTourByID(tourID, time.Now().UTC())
}

func (s *Store) GetTourEvent(tourID, eventID string) (*protocol.TourEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(`
		SELECT id, seq, kind, markdown, finish, context_json, created_at
		FROM tour_events
		WHERE tour_id = ? AND id = ?
	`, tourID, eventID)
	event, err := scanTourEvent(tourID, row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("tour event not found")
	}
	return event, err
}

func (s *Store) TouchTourListener(tourID string, deliveredSeq int) (*protocol.TourRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if deliveredSeq < 0 {
		deliveredSeq = 0
	}
	wallNow := time.Now().UTC()
	tour, err := s.getTourByID(tourID, wallNow)
	if err != nil {
		return nil, err
	}
	mutationTime := nextTourMutationTime(tour.UpdatedAt)
	result, err := s.db.Exec(`
		UPDATE tour_runs
		SET listener_last_seen = ?,
			listener_event_seq = MAX(listener_event_seq, ?),
			updated_at = ?
		WHERE id = ? AND status = ?
	`, wallNow.Format(time.RFC3339Nano), deliveredSeq, mutationTime.Format(time.RFC3339Nano), tourID, string(protocol.TourStatusActive))
	if err != nil {
		return nil, fmt.Errorf("touch tour listener: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return nil, fmt.Errorf("active tour not found")
	}
	return s.getTourByID(tourID, wallNow)
}

func (s *Store) SaveTourDraft(
	tourID, path string,
	reviewed bool,
	note string,
	annotationReplies []protocol.TourDraftText,
	lineComments []protocol.TourLineComment,
) (*protocol.TourRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	wallNow := time.Now().UTC()
	tour, err := s.getTourByID(tourID, wallNow)
	if err != nil {
		return nil, err
	}
	repliesJSON, err := json.Marshal(annotationReplies)
	if err != nil {
		return nil, fmt.Errorf("encode annotation replies: %w", err)
	}
	commentsJSON, err := json.Marshal(lineComments)
	if err != nil {
		return nil, fmt.Errorf("encode line comments: %w", err)
	}
	reviewedInt := 0
	if reviewed {
		reviewedInt = 1
	}
	_, err = s.db.Exec(`
		INSERT INTO tour_drafts (
			tour_id, path, reviewed, note, annotation_replies_json, line_comments_json
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(tour_id, path) DO UPDATE SET
			reviewed = excluded.reviewed,
			note = excluded.note,
			annotation_replies_json = excluded.annotation_replies_json,
			line_comments_json = excluded.line_comments_json
	`, tourID, path, reviewedInt, note, string(repliesJSON), string(commentsJSON))
	if err != nil {
		return nil, fmt.Errorf("save tour draft: %w", err)
	}
	mutationTime := nextTourMutationTime(tour.UpdatedAt)
	if _, err := s.db.Exec(`UPDATE tour_runs SET current_file = ?, updated_at = ? WHERE id = ?`,
		path, mutationTime.Format(time.RFC3339Nano), tourID); err != nil {
		return nil, fmt.Errorf("update tour position: %w", err)
	}
	return s.getTourByID(tourID, wallNow)
}

func (s *Store) AddTourEvent(
	tourID, kind, markdown string,
	finish bool,
	context *protocol.TourQuestionContext,
) (*protocol.TourEvent, *protocol.TourRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	wallNow := time.Now().UTC()
	tour, err := s.getTourByID(tourID, wallNow)
	if err != nil {
		return nil, nil, err
	}
	if tour.Status != protocol.TourStatusActive {
		return nil, nil, fmt.Errorf("tour has ended")
	}
	var nextSeq int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM tour_events WHERE tour_id = ?`, tourID).Scan(&nextSeq); err != nil {
		return nil, nil, fmt.Errorf("get next tour event sequence: %w", err)
	}
	contextJSON, err := encodeTourQuestionContext(context)
	if err != nil {
		return nil, nil, err
	}
	mutationTime := nextTourMutationTime(tour.UpdatedAt)
	id := uuid.NewString()
	finishInt := 0
	if finish {
		finishInt = 1
	}
	_, err = s.db.Exec(`
		INSERT INTO tour_events (id, tour_id, seq, kind, markdown, finish, context_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, id, tourID, nextSeq, kind, markdown, finishInt, contextJSON, mutationTime.Format(time.RFC3339Nano))
	if err != nil {
		return nil, nil, fmt.Errorf("add tour event: %w", err)
	}
	event := &protocol.TourEvent{
		ID:        id,
		TourID:    tourID,
		Seq:       nextSeq,
		Kind:      kind,
		Markdown:  markdown,
		Finish:    finish,
		Context:   context,
		CreatedAt: mutationTime.Format(time.RFC3339Nano),
	}
	if finish {
		if _, err := s.db.Exec(`
			UPDATE tour_runs SET status = ?, ended_at = ?, updated_at = ? WHERE id = ?
		`, string(protocol.TourStatusEnded), mutationTime.Format(time.RFC3339Nano), mutationTime.Format(time.RFC3339Nano), tourID); err != nil {
			return nil, nil, fmt.Errorf("end tour: %w", err)
		}
	} else if _, err := s.db.Exec(`
		UPDATE tour_runs SET updated_at = ? WHERE id = ?
	`, mutationTime.Format(time.RFC3339Nano), tourID); err != nil {
		return nil, nil, fmt.Errorf("update tour event timestamp: %w", err)
	}
	updated, err := s.getTourByID(tourID, wallNow)
	return event, updated, err
}

func (s *Store) NextTourEvent(tourID string, afterSeq int) (*protocol.TourEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(`
		SELECT id, seq, kind, markdown, finish, context_json, created_at
		FROM tour_events
		WHERE tour_id = ? AND seq > ?
		ORDER BY seq ASC
		LIMIT 1
	`, tourID, afterSeq)
	event, err := scanTourEvent(tourID, row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return event, err
}

func (s *Store) AddTourTranscript(
	tourID, role, body string,
	eventID *string,
	context *protocol.TourQuestionContext,
) (*protocol.TourRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	wallNow := time.Now().UTC()
	tour, err := s.getTourByID(tourID, wallNow)
	if err != nil {
		return nil, err
	}
	contextJSON, err := encodeTourQuestionContext(context)
	if err != nil {
		return nil, err
	}
	mutationTime := nextTourMutationTime(tour.UpdatedAt)
	_, err = s.db.Exec(`
		INSERT INTO tour_transcript (id, tour_id, role, body, event_id, context_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, uuid.NewString(), tourID, role, body, eventID, contextJSON, mutationTime.Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("add tour transcript: %w", err)
	}
	if _, err := s.db.Exec(`
		UPDATE tour_runs SET updated_at = ? WHERE id = ?
	`, mutationTime.Format(time.RFC3339Nano), tourID); err != nil {
		return nil, fmt.Errorf("update tour transcript timestamp: %w", err)
	}
	return s.getTourByID(tourID, wallNow)
}

func nextTourMutationTime(previous string) time.Time {
	now := time.Now().UTC()
	previousTime, err := time.Parse(time.RFC3339Nano, previous)
	if err == nil && !now.After(previousTime) {
		return previousTime.Add(time.Nanosecond)
	}
	return now
}

func (s *Store) latestTourUpdatedAt(sessionID string) (time.Time, bool, error) {
	rows, err := s.db.Query(`SELECT updated_at FROM tour_runs WHERE session_id = ?`, sessionID)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("get tour timestamps: %w", err)
	}
	defer rows.Close()

	var latest time.Time
	found := false
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return time.Time{}, false, fmt.Errorf("scan tour timestamp: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("parse tour timestamp %q: %w", value, err)
		}
		if !found || parsed.After(latest) {
			latest = parsed
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return time.Time{}, false, fmt.Errorf("read tour timestamps: %w", err)
	}
	return latest, found, nil
}

func (s *Store) updateTourSnapshot(
	tourID, name, repoPath, guidePath, baseRef string,
	snapshot TourSnapshot,
	wallNow, mutationTime time.Time,
) error {
	warningsJSON, filesJSON, err := encodeTourSnapshot(snapshot)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		UPDATE tour_runs SET
			name = ?, repo_path = ?, guide_path = ?, base_ref = ?,
			summary = ?, warnings_json = ?, files_json = ?,
			listener_last_seen = ?, updated_at = ?
		WHERE id = ?
	`, name, repoPath, guidePath, baseRef, snapshot.Summary, warningsJSON, filesJSON,
		wallNow.Format(time.RFC3339Nano), mutationTime.Format(time.RFC3339Nano), tourID)
	if err != nil {
		return fmt.Errorf("update tour snapshot: %w", err)
	}
	return nil
}

func encodeTourSnapshot(snapshot TourSnapshot) (string, string, error) {
	if snapshot.Warnings == nil {
		snapshot.Warnings = []string{}
	}
	snapshot.Files = normalizeTourFiles(snapshot.Files)
	warningsJSON, err := json.Marshal(snapshot.Warnings)
	if err != nil {
		return "", "", fmt.Errorf("encode tour warnings: %w", err)
	}
	filesJSON, err := json.Marshal(snapshot.Files)
	if err != nil {
		return "", "", fmt.Errorf("encode tour files: %w", err)
	}
	return string(warningsJSON), string(filesJSON), nil
}

func encodeTourQuestionContext(context *protocol.TourQuestionContext) (*string, error) {
	if context == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(context)
	if err != nil {
		return nil, fmt.Errorf("encode tour context: %w", err)
	}
	value := string(encoded)
	return &value, nil
}

func (s *Store) getActiveTourBySession(sessionID string, now time.Time) (*protocol.TourRun, error) {
	row := s.db.QueryRow(`
		SELECT id, session_id, name, repo_path, guide_path, base_ref, status,
			summary, warnings_json, files_json, current_file, listener_last_seen,
			listener_event_seq, created_at, updated_at, ended_at
		FROM tour_runs WHERE session_id = ? AND status = ?
	`, sessionID, string(protocol.TourStatusActive))
	return s.scanTourRun(row, now)
}

func (s *Store) getTourByID(tourID string, now time.Time) (*protocol.TourRun, error) {
	row := s.db.QueryRow(`
		SELECT id, session_id, name, repo_path, guide_path, base_ref, status,
			summary, warnings_json, files_json, current_file, listener_last_seen,
			listener_event_seq, created_at, updated_at, ended_at
		FROM tour_runs WHERE id = ?
	`, tourID)
	return s.scanTourRun(row, now)
}

type tourRow interface {
	Scan(dest ...any) error
}

func (s *Store) scanTourRun(row tourRow, now time.Time) (*protocol.TourRun, error) {
	var run protocol.TourRun
	var status string
	var warningsJSON, filesJSON string
	var currentFile, listenerLastSeen, endedAt sql.NullString
	if err := row.Scan(
		&run.TourID,
		&run.SessionID,
		&run.Name,
		&run.RepoPath,
		&run.GuidePath,
		&run.BaseRef,
		&status,
		&run.Summary,
		&warningsJSON,
		&filesJSON,
		&currentFile,
		&listenerLastSeen,
		&run.ListenerEventSeq,
		&run.CreatedAt,
		&run.UpdatedAt,
		&endedAt,
	); err != nil {
		return nil, err
	}
	run.Status = protocol.TourStatus(status)
	run.ConnectionState = protocol.TourConnectionStateDisconnected
	if listenerLastSeen.Valid {
		if seen, err := time.Parse(time.RFC3339Nano, listenerLastSeen.String); err == nil {
			age := now.Sub(seen)
			if age >= 0 && age <= tourListenerLease {
				run.ConnectionState = protocol.TourConnectionStateConnected
			}
		}
	}
	if currentFile.Valid {
		run.CurrentFile = &currentFile.String
	}
	if endedAt.Valid {
		run.EndedAt = &endedAt.String
	}
	if err := json.Unmarshal([]byte(warningsJSON), &run.Warnings); err != nil {
		return nil, fmt.Errorf("decode tour warnings: %w", err)
	}
	if err := json.Unmarshal([]byte(filesJSON), &run.Files); err != nil {
		return nil, fmt.Errorf("decode tour files: %w", err)
	}
	if run.Warnings == nil {
		run.Warnings = []string{}
	}
	run.Files = normalizeTourFiles(run.Files)
	drafts, err := s.loadTourDrafts(run.TourID)
	if err != nil {
		return nil, err
	}
	run.Drafts = drafts
	transcript, err := s.loadTourTranscript(run.TourID)
	if err != nil {
		return nil, err
	}
	run.Transcript = transcript
	return &run, nil
}

func normalizeTourFiles(files []protocol.TourFile) []protocol.TourFile {
	if files == nil {
		files = []protocol.TourFile{}
	}
	for fileIndex := range files {
		if files[fileIndex].Annotations == nil {
			files[fileIndex].Annotations = []protocol.TourAnnotation{}
		}
		for annotationIndex := range files[fileIndex].Annotations {
			if files[fileIndex].Annotations[annotationIndex].Comments == nil {
				files[fileIndex].Annotations[annotationIndex].Comments = []protocol.TourComment{}
			}
		}
	}
	return files
}

func (s *Store) loadTourDrafts(tourID string) ([]protocol.TourFileDraft, error) {
	rows, err := s.db.Query(`
		SELECT path, reviewed, note, annotation_replies_json, line_comments_json
		FROM tour_drafts WHERE tour_id = ? ORDER BY path
	`, tourID)
	if err != nil {
		return nil, fmt.Errorf("load tour drafts: %w", err)
	}
	defer rows.Close()

	drafts := []protocol.TourFileDraft{}
	for rows.Next() {
		var draft protocol.TourFileDraft
		var reviewed int
		var repliesJSON, commentsJSON string
		if err := rows.Scan(&draft.Path, &reviewed, &draft.Note, &repliesJSON, &commentsJSON); err != nil {
			return nil, err
		}
		draft.Reviewed = reviewed != 0
		if err := json.Unmarshal([]byte(repliesJSON), &draft.AnnotationReplies); err != nil {
			return nil, fmt.Errorf("decode annotation replies: %w", err)
		}
		if err := json.Unmarshal([]byte(commentsJSON), &draft.LineComments); err != nil {
			return nil, fmt.Errorf("decode line comments: %w", err)
		}
		if draft.AnnotationReplies == nil {
			draft.AnnotationReplies = []protocol.TourDraftText{}
		}
		if draft.LineComments == nil {
			draft.LineComments = []protocol.TourLineComment{}
		}
		drafts = append(drafts, draft)
	}
	return drafts, rows.Err()
}

func (s *Store) loadTourTranscript(tourID string) ([]protocol.TourTranscriptEntry, error) {
	rows, err := s.db.Query(`
		SELECT id, role, body, event_id, context_json, created_at
		FROM tour_transcript WHERE tour_id = ? ORDER BY created_at, id
	`, tourID)
	if err != nil {
		return nil, fmt.Errorf("load tour transcript: %w", err)
	}
	defer rows.Close()

	entries := []protocol.TourTranscriptEntry{}
	for rows.Next() {
		var entry protocol.TourTranscriptEntry
		var eventID, contextJSON sql.NullString
		if err := rows.Scan(&entry.ID, &entry.Role, &entry.Body, &eventID, &contextJSON, &entry.CreatedAt); err != nil {
			return nil, err
		}
		if eventID.Valid {
			entry.EventID = &eventID.String
		}
		if contextJSON.Valid {
			var context protocol.TourQuestionContext
			if err := json.Unmarshal([]byte(contextJSON.String), &context); err != nil {
				return nil, fmt.Errorf("decode transcript context: %w", err)
			}
			entry.Context = &context
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func scanTourEvent(tourID string, row tourRow) (*protocol.TourEvent, error) {
	var event protocol.TourEvent
	var finish int
	var contextJSON sql.NullString
	if err := row.Scan(
		&event.ID,
		&event.Seq,
		&event.Kind,
		&event.Markdown,
		&finish,
		&contextJSON,
		&event.CreatedAt,
	); err != nil {
		return nil, err
	}
	event.TourID = tourID
	event.Finish = finish != 0
	if contextJSON.Valid {
		var context protocol.TourQuestionContext
		if err := json.Unmarshal([]byte(contextJSON.String), &context); err != nil {
			return nil, fmt.Errorf("decode tour event context: %w", err)
		}
		event.Context = &context
	}
	return &event, nil
}
