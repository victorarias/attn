package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func parseOptionalAutomationTime(value string) *time.Time {
	if value == "" {
		return nil
	}
	parsed := parseTicketTime(value)
	return &parsed
}

type AutomationDefinition struct {
	ID, Name, SpecJSON   string
	Enabled              bool
	Revision             int
	CreatedAt, UpdatedAt time.Time
	DeletedAt            *time.Time
}

type AutomationRun struct {
	ID, DefinitionID, OccurrenceID           string
	DefinitionRevision                       int
	SnapshotJSON, State, LastError           string
	TicketID, SessionID, WorkspaceID, PaneID string
	ResolvedLocationJSON                     string
	CreatedAt, UpdatedAt                     time.Time
	DeliveredAt                              *time.Time
}

type AutomationOccurrence struct {
	ID, DefinitionID, Provider, OccurrenceKey, SubjectKey, PayloadJSON string
	ObservedAt, CreatedAt                                              time.Time
}

type AutomationRunReservation struct {
	RunID, OccurrenceID, TicketID, SessionID, WorkspaceID, PaneID string
}

type AutomationReviewRequestCandidate struct {
	SubjectKey string
	Cycle      int
}

const AutomationReviewWithdrawnError = "GitHub review request withdrawn before delivery"

func (s *Store) AutomationReviewRequestNeedsClaim(definitionID, subjectKey string, cycle int) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return false, errors.New("automation persistence unavailable")
	}
	var active, currentCycle, acceptedCycle int
	err := s.db.QueryRow(`SELECT active,cycle,accepted_cycle FROM automation_review_request_edges WHERE definition_id=? AND subject_key=?`, definitionID, subjectKey).Scan(&active, &currentCycle, &acceptedCycle)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if active != 1 || currentCycle != cycle {
		return false, nil
	}
	if acceptedCycle < cycle {
		return true, nil
	}
	occurrenceKey := fmt.Sprintf("review_requested:%s:%d", subjectKey, cycle)
	var pending int
	err = s.db.QueryRow(`
		SELECT EXISTS(
			SELECT 1
			FROM automation_occurrences o
			JOIN automation_runs r ON r.occurrence_id=o.id
			WHERE o.definition_id=? AND o.provider='github' AND o.occurrence_key=? AND r.state='pending'
		)
	`, definitionID, occurrenceKey).Scan(&pending)
	return pending != 0, err
}

func (s *Store) UpsertAutomationDefinition(id, name, specJSON string, enabled bool, now time.Time) (*AutomationDefinition, error) {
	s.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()
	if s.db == nil {
		return nil, errors.New("automation persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var revision, oldEnabled int
	var oldSpec string
	err = tx.QueryRow(`SELECT revision, spec_json, enabled FROM automation_definitions WHERE id=? AND deleted_at=''`, id).Scan(&revision, &oldSpec, &oldEnabled)
	switch err {
	case sql.ErrNoRows:
		revision = 1
		_, err = tx.Exec(`INSERT INTO automation_definitions(id,name,enabled,revision,spec_json,created_at,updated_at,deleted_at) VALUES(?,?,?,?,?,?,?,'')`, id, name, enabled, revision, specJSON, formatTicketTime(now), formatTicketTime(now))
	case nil:
		if oldSpec != specJSON {
			revision++
		}
		_, err = tx.Exec(`UPDATE automation_definitions SET name=?, enabled=?, revision=?, spec_json=?, updated_at=? WHERE id=?`, name, enabled, revision, specJSON, formatTicketTime(now), id)
		if err == nil && oldEnabled == 0 && enabled {
			// Re-enabling begins from the provider's current truth. A request that
			// arrived while disabled must be eligible for latest catch-up even if an
			// older cycle for the same subject had been active before disable.
			_, err = tx.Exec(`UPDATE automation_review_request_edges SET active=0,updated_at=? WHERE definition_id=?`, formatTicketTime(now), id)
		}
	}
	if err != nil {
		return nil, err
	}
	if enabled {
		// Fence provider snapshots that began before this definition became
		// active (or was reapplied). Observation timestamps are captured before
		// provider fetches, so an in-flight stale refresh cannot launch work under
		// a newly enabled revision.
		fence := now.UTC().Format(time.RFC3339Nano)
		if _, err := tx.Exec(`INSERT INTO automation_provider_cursors(definition_id,provider,scope,observed_at) VALUES(?,'github_review_requested','*',?) ON CONFLICT(definition_id,provider,scope) DO UPDATE SET observed_at=excluded.observed_at`, id, fence); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.mu.Unlock()
	locked = false
	return s.GetAutomationDefinition(id)
}

func scanAutomationDefinition(scanner interface{ Scan(...any) error }) (*AutomationDefinition, error) {
	var d AutomationDefinition
	var enabled int
	var created, updated, deleted string
	if err := scanner.Scan(&d.ID, &d.Name, &enabled, &d.Revision, &d.SpecJSON, &created, &updated, &deleted); err != nil {
		return nil, err
	}
	d.Enabled = enabled != 0
	d.CreatedAt = parseTicketTime(created)
	d.UpdatedAt = parseTicketTime(updated)
	d.DeletedAt = parseOptionalAutomationTime(deleted)
	return &d, nil
}

func (s *Store) GetAutomationDefinition(id string) (*AutomationDefinition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	d, err := scanAutomationDefinition(s.db.QueryRow(`SELECT id,name,enabled,revision,spec_json,created_at,updated_at,deleted_at FROM automation_definitions WHERE id=? AND deleted_at=''`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

func (s *Store) ListAutomationDefinitions() ([]AutomationDefinition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT id,name,enabled,revision,spec_json,created_at,updated_at,deleted_at FROM automation_definitions WHERE deleted_at='' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationDefinition
	for rows.Next() {
		d, err := scanAutomationDefinition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *Store) ClaimManualAutomationRun(definitionID, requestID, subjectKey, payloadJSON string, expectedRevision int, snapshotJSON string, observedAt time.Time, ids AutomationRunReservation) (*AutomationRun, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, false, errors.New("automation persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	key := "manual:" + requestID
	var existingID string
	err = tx.QueryRow(`SELECT r.id FROM automation_occurrences o JOIN automation_runs r ON r.occurrence_id=o.id WHERE o.definition_id=? AND o.provider='manual' AND o.occurrence_key=?`, definitionID, key).Scan(&existingID)
	if err == nil {
		tx.Rollback()
		run, e := s.getAutomationRunUnlocked(existingID)
		return run, false, e
	}
	if err != sql.ErrNoRows {
		return nil, false, err
	}
	var revision int
	var enabled int
	if err := tx.QueryRow(`SELECT revision,enabled FROM automation_definitions WHERE id=? AND deleted_at=''`, definitionID).Scan(&revision, &enabled); err != nil {
		return nil, false, err
	}
	if revision != expectedRevision {
		return nil, false, fmt.Errorf("automation definition changed while starting run")
	}
	if enabled == 0 {
		return nil, false, fmt.Errorf("automation %q is disabled", definitionID)
	}
	now := formatTicketTime(observedAt)
	if _, err = tx.Exec(`INSERT INTO automation_occurrences(id,definition_id,provider,occurrence_key,subject_key,observed_at,payload_json,created_at) VALUES(?,?, 'manual',?,?,?,?,?)`, ids.OccurrenceID, definitionID, key, subjectKey, now, payloadJSON, now); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(`INSERT INTO automation_runs(id,definition_id,occurrence_id,definition_revision,snapshot_json,state,ticket_id,session_id,workspace_id,pane_id,created_at,updated_at) VALUES(?,?,?,?,?,'pending',?,?,?,?,?,?)`, ids.RunID, definitionID, ids.OccurrenceID, revision, snapshotJSON, ids.TicketID, ids.SessionID, ids.WorkspaceID, ids.PaneID, now, now); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	run, e := s.getAutomationRunUnlocked(ids.RunID)
	return run, true, e
}

// ReconcileAutomationReviewRequests records the provider's complete current
// review-request demand for one host. It returns active edges whose current cycle
// either has not been accepted or still owns a pending run, so both detail-fetch
// and delivery failures retry on a later observation without inventing a cycle.
func (s *Store) ReconcileAutomationReviewRequests(definitionID, host string, subjectKeys []string, observedAt time.Time) ([]AutomationReviewRequestCandidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, errors.New("automation persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	observedRaw := observedAt.UTC().Format(time.RFC3339Nano)
	updatedRaw := formatTicketTime(observedAt)
	var enableFenceRaw string
	err = tx.QueryRow(`SELECT observed_at FROM automation_provider_cursors WHERE definition_id=? AND provider='github_review_requested' AND scope='*'`, definitionID).Scan(&enableFenceRaw)
	if err == nil {
		enableFence, parseErr := time.Parse(time.RFC3339Nano, enableFenceRaw)
		if parseErr != nil {
			return nil, fmt.Errorf("parse automation enable fence: %w", parseErr)
		}
		if observedAt.Before(enableFence) {
			return nil, nil
		}
	}
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	var cursorRaw string
	err = tx.QueryRow(`SELECT observed_at FROM automation_provider_cursors WHERE definition_id=? AND provider='github_review_requested' AND scope=?`, definitionID, host).Scan(&cursorRaw)
	if err == nil {
		cursorAt, parseErr := time.Parse(time.RFC3339Nano, cursorRaw)
		if parseErr != nil {
			return nil, fmt.Errorf("parse automation provider cursor: %w", parseErr)
		}
		if observedAt.Before(cursorAt) {
			return nil, nil
		}
	}
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	current := make(map[string]bool, len(subjectKeys))
	for _, subjectKey := range subjectKeys {
		if subjectKey == "" || current[subjectKey] {
			continue
		}
		current[subjectKey] = true
		var active, cycle int
		err := tx.QueryRow(`SELECT active,cycle FROM automation_review_request_edges WHERE definition_id=? AND subject_key=?`, definitionID, subjectKey).Scan(&active, &cycle)
		switch err {
		case sql.ErrNoRows:
			_, err = tx.Exec(`INSERT INTO automation_review_request_edges(definition_id,subject_key,host,active,cycle,accepted_cycle,last_observed_at,updated_at) VALUES(?,?,?,1,1,0,?,?)`, definitionID, subjectKey, host, observedRaw, updatedRaw)
		case nil:
			if active == 0 {
				cycle++
			}
			_, err = tx.Exec(`UPDATE automation_review_request_edges SET host=?,active=1,cycle=?,last_observed_at=?,updated_at=? WHERE definition_id=? AND subject_key=?`, host, cycle, observedRaw, updatedRaw, definitionID, subjectKey)
		}
		if err != nil {
			return nil, err
		}
	}
	rows, err := tx.Query(`SELECT subject_key FROM automation_review_request_edges WHERE definition_id=? AND host=? AND active=1`, definitionID, host)
	if err != nil {
		return nil, err
	}
	var deactivate []string
	for rows.Next() {
		var subjectKey string
		if err := rows.Scan(&subjectKey); err != nil {
			rows.Close()
			return nil, err
		}
		if !current[subjectKey] {
			deactivate = append(deactivate, subjectKey)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, subjectKey := range deactivate {
		if _, err := tx.Exec(`UPDATE automation_review_request_edges SET active=0,updated_at=? WHERE definition_id=? AND subject_key=?`, updatedRaw, definitionID, subjectKey); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`
			DELETE FROM automation_continuity_bindings
			WHERE definition_id=? AND continuity_key=?
			  AND NOT EXISTS (
				SELECT 1 FROM tickets
				WHERE tickets.id=automation_continuity_bindings.ticket_id
			  )
		`, definitionID, subjectKey); err != nil {
			return nil, err
		}
	}
	rows, err = tx.Query(`SELECT subject_key,cycle,accepted_cycle FROM automation_review_request_edges WHERE definition_id=? AND host=? AND active=1 ORDER BY subject_key`, definitionID, host)
	if err != nil {
		return nil, err
	}
	type activeReviewEdge struct {
		subjectKey    string
		cycle         int
		acceptedCycle int
	}
	var activeEdges []activeReviewEdge
	for rows.Next() {
		var edge activeReviewEdge
		if err := rows.Scan(&edge.subjectKey, &edge.cycle, &edge.acceptedCycle); err != nil {
			rows.Close()
			return nil, err
		}
		activeEdges = append(activeEdges, edge)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var candidates []AutomationReviewRequestCandidate
	for _, edge := range activeEdges {
		if edge.acceptedCycle < edge.cycle {
			candidates = append(candidates, AutomationReviewRequestCandidate{SubjectKey: edge.subjectKey, Cycle: edge.cycle})
			continue
		}
		occurrenceKey := fmt.Sprintf("review_requested:%s:%d", edge.subjectKey, edge.cycle)
		var pending int
		if err := tx.QueryRow(`
			SELECT EXISTS(
				SELECT 1
				FROM automation_occurrences o
				JOIN automation_runs r ON r.occurrence_id=o.id
				WHERE o.definition_id=? AND o.provider='github' AND o.occurrence_key=? AND r.state='pending'
			)
		`, definitionID, occurrenceKey).Scan(&pending); err != nil {
			return nil, err
		}
		if pending != 0 {
			candidates = append(candidates, AutomationReviewRequestCandidate{SubjectKey: edge.subjectKey, Cycle: edge.cycle})
		}
	}
	if _, err := tx.Exec(`INSERT INTO automation_provider_cursors(definition_id,provider,scope,observed_at) VALUES(?,'github_review_requested',?,?) ON CONFLICT(definition_id,provider,scope) DO UPDATE SET observed_at=excluded.observed_at WHERE excluded.observed_at >= automation_provider_cursors.observed_at`, definitionID, host, observedRaw); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return candidates, nil
}

func (s *Store) GitHubReviewAutomationRunStillRequested(runID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return false, errors.New("automation persistence unavailable")
	}
	var occurrenceKey, subjectKey string
	var active, cycle, acceptedCycle int
	err := s.db.QueryRow(`
		SELECT o.occurrence_key,o.subject_key,e.active,e.cycle,e.accepted_cycle
		FROM automation_runs r
		JOIN automation_occurrences o ON o.id=r.occurrence_id
		JOIN automation_review_request_edges e
		  ON e.definition_id=r.definition_id AND e.subject_key=o.subject_key
		WHERE r.id=? AND o.provider='github'
	`, runID).Scan(&occurrenceKey, &subjectKey, &active, &cycle, &acceptedCycle)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	wantKey := fmt.Sprintf("review_requested:%s:%d", subjectKey, cycle)
	return active == 1 && acceptedCycle == cycle && occurrenceKey == wantKey, nil
}

// ListWithdrawnGitHubReviewUndeliveredRuns returns the current withdrawn cycle's
// pending run, or its failed run when cancellation still needs to be reconciled.
// Delivery is the handoff to ordinary ticket/session ownership; persisted stable
// IDs alone are not, because they may precede successful launch verification.
func (s *Store) ListWithdrawnGitHubReviewUndeliveredRuns(definitionID, host string) ([]AutomationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, errors.New("automation persistence unavailable")
	}
	rows, err := s.db.Query(`
		SELECT r.id,r.definition_id,r.occurrence_id,r.definition_revision,r.snapshot_json,r.state,r.last_error,r.ticket_id,r.session_id,r.workspace_id,r.pane_id,r.resolved_location_json,r.created_at,r.updated_at,r.delivered_at
		FROM automation_runs r
		JOIN automation_occurrences o ON o.id=r.occurrence_id
		JOIN automation_review_request_edges e
		  ON e.definition_id=r.definition_id AND e.subject_key=o.subject_key
		WHERE r.definition_id=? AND e.host=? AND e.active=0
		  AND (r.state='pending' OR (r.state='failed' AND r.last_error=?))
		  AND o.provider='github'
		  AND o.occurrence_key=('review_requested:' || e.subject_key || ':' || CAST(e.cycle AS TEXT))
		ORDER BY r.created_at
	`, definitionID, host, AutomationReviewWithdrawnError)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationRun
	for rows.Next() {
		run, err := scanAutomationRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

func (s *Store) ClaimGitHubReviewAutomationRun(definitionID, subjectKey string, cycle, expectedRevision int, payloadJSON, snapshotJSON string, observedAt time.Time, reserved AutomationRunReservation) (*AutomationRun, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, false, errors.New("automation persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var active, currentCycle, acceptedCycle int
	if err := tx.QueryRow(`SELECT active,cycle,accepted_cycle FROM automation_review_request_edges WHERE definition_id=? AND subject_key=?`, definitionID, subjectKey).Scan(&active, &currentCycle, &acceptedCycle); err != nil {
		return nil, false, err
	}
	occurrenceKey := fmt.Sprintf("review_requested:%s:%d", subjectKey, cycle)
	if active == 0 || currentCycle != cycle {
		return nil, false, errors.New("review-request edge changed before occurrence claim")
	}
	if acceptedCycle >= cycle {
		var runID string
		err := tx.QueryRow(`SELECT r.id FROM automation_occurrences o JOIN automation_runs r ON r.occurrence_id=o.id WHERE o.definition_id=? AND o.provider='github' AND o.occurrence_key=?`, definitionID, occurrenceKey).Scan(&runID)
		if err != nil {
			return nil, false, err
		}
		tx.Rollback()
		run, err := s.getAutomationRunUnlocked(runID)
		return run, false, err
	}
	var revision, enabled int
	if err := tx.QueryRow(`SELECT revision,enabled FROM automation_definitions WHERE id=? AND deleted_at=''`, definitionID).Scan(&revision, &enabled); err != nil {
		return nil, false, err
	}
	if revision != expectedRevision {
		return nil, false, errors.New("automation definition changed while accepting observation")
	}
	if enabled == 0 {
		return nil, false, fmt.Errorf("automation %q is disabled", definitionID)
	}
	// A later request cycle must not overtake the initial delivery for this
	// subject. The continuity binding can exist before its ticket does; accepting
	// another cycle in that window would make delivery mistake a not-yet-created
	// ticket for a swept one. Leave the edge unaccepted so a later provider
	// refresh retries it after the pending run settles or creates its ticket.
	var undeliveredPredecessor int
	if err := tx.QueryRow(`
		SELECT EXISTS(
			SELECT 1
			FROM automation_runs r
			JOIN automation_occurrences o ON o.id=r.occurrence_id
			WHERE r.definition_id=? AND o.subject_key=? AND r.state='pending'
			  AND NOT EXISTS (SELECT 1 FROM tickets t WHERE t.id=r.ticket_id)
		)
	`, definitionID, subjectKey).Scan(&undeliveredPredecessor); err != nil {
		return nil, false, err
	}
	if undeliveredPredecessor != 0 {
		return nil, false, errors.New("an earlier automation run for this subject has not created its ticket yet")
	}
	ids := reserved
	var createdAt, updatedAt string
	err = tx.QueryRow(`SELECT ticket_id,session_id,workspace_id,pane_id,created_at,updated_at FROM automation_continuity_bindings WHERE definition_id=? AND continuity_key=?`, definitionID, subjectKey).Scan(&ids.TicketID, &ids.SessionID, &ids.WorkspaceID, &ids.PaneID, &createdAt, &updatedAt)
	switch err {
	case sql.ErrNoRows:
		now := formatTicketTime(observedAt)
		if _, err = tx.Exec(`INSERT INTO automation_continuity_bindings(definition_id,continuity_key,ticket_id,session_id,workspace_id,pane_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, definitionID, subjectKey, ids.TicketID, ids.SessionID, ids.WorkspaceID, ids.PaneID, now, now); err != nil {
			return nil, false, err
		}
	case nil:
	default:
		return nil, false, err
	}
	now := formatTicketTime(observedAt)
	if _, err = tx.Exec(`INSERT INTO automation_occurrences(id,definition_id,provider,occurrence_key,subject_key,observed_at,payload_json,created_at) VALUES(?,?, 'github',?,?,?,?,?)`, ids.OccurrenceID, definitionID, occurrenceKey, subjectKey, now, payloadJSON, now); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(`INSERT INTO automation_runs(id,definition_id,occurrence_id,definition_revision,snapshot_json,state,ticket_id,session_id,workspace_id,pane_id,created_at,updated_at) VALUES(?,?,?,?,?,'pending',?,?,?,?,?,?)`, ids.RunID, definitionID, ids.OccurrenceID, revision, snapshotJSON, ids.TicketID, ids.SessionID, ids.WorkspaceID, ids.PaneID, now, now); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(`UPDATE automation_review_request_edges SET accepted_cycle=?,updated_at=? WHERE definition_id=? AND subject_key=? AND active=1 AND cycle=?`, cycle, now, definitionID, subjectKey, cycle); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	run, err := s.getAutomationRunUnlocked(ids.RunID)
	return run, true, err
}

// EnsureAutomationContinuationTicket records a later accepted occurrence on the
// already-bound ticket exactly once. The run remains the forward provenance link;
// the ticket's immutable automation_run_id continues to identify the run that
// originally created the worker.
func (s *Store) EnsureAutomationContinuationTicket(ticketID, sessionID, runID, occurrencePath, author string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var assignee, originRunID string
	if err := tx.QueryRow(`SELECT assignee,COALESCE(automation_run_id,'') FROM tickets WHERE id=?`, ticketID).Scan(&assignee, &originRunID); err != nil {
		return err
	}
	if assignee != sessionID || originRunID == "" {
		return errors.New("continuity ticket does not match its automation binding")
	}
	result, err := tx.Exec(`INSERT OR IGNORE INTO automation_ticket_occurrence_events(run_id,ticket_id,created_at) VALUES(?,?,?)`, runID, ticketID, formatTicketTime(now))
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 1 {
		comment := "Accepted automation occurrence " + runID + " for the existing reviewer. Structured occurrence input: " + occurrencePath
		if _, err := addTicketCommentTx(tx, ticketID, author, comment, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// HasPriorAutomationContinuityRun reports whether this subject binding predates
// the current run. It lets delivery distinguish first-run crash recovery (where
// the ticket may not exist yet) from a later cycle whose bound ticket was removed.
func (s *Store) HasPriorAutomationContinuityRun(definitionID, continuityKey, currentRunID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return false, errors.New("automation persistence unavailable")
	}
	var exists int
	err := s.db.QueryRow(`
		SELECT EXISTS(
			SELECT 1
			FROM automation_runs r
			JOIN automation_occurrences o ON o.id=r.occurrence_id
			WHERE r.definition_id=? AND o.subject_key=? AND r.id<>?
		)
	`, definitionID, continuityKey, currentRunID).Scan(&exists)
	return exists != 0, err
}

func scanAutomationRun(scanner interface{ Scan(...any) error }) (*AutomationRun, error) {
	var r AutomationRun
	var created, updated, delivered string
	err := scanner.Scan(&r.ID, &r.DefinitionID, &r.OccurrenceID, &r.DefinitionRevision, &r.SnapshotJSON, &r.State, &r.LastError, &r.TicketID, &r.SessionID, &r.WorkspaceID, &r.PaneID, &r.ResolvedLocationJSON, &created, &updated, &delivered)
	if err != nil {
		return nil, err
	}
	r.CreatedAt = parseTicketTime(created)
	r.UpdatedAt = parseTicketTime(updated)
	r.DeliveredAt = parseOptionalAutomationTime(delivered)
	return &r, nil
}

const automationRunColumns = `id,definition_id,occurrence_id,definition_revision,snapshot_json,state,last_error,ticket_id,session_id,workspace_id,pane_id,resolved_location_json,created_at,updated_at,delivered_at`

func (s *Store) getAutomationRunUnlocked(id string) (*AutomationRun, error) {
	r, e := scanAutomationRun(s.db.QueryRow(`SELECT `+automationRunColumns+` FROM automation_runs WHERE id=?`, id))
	if e == sql.ErrNoRows {
		return nil, nil
	}
	return r, e
}
func (s *Store) GetAutomationRun(id string) (*AutomationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	return s.getAutomationRunUnlocked(id)
}
func (s *Store) GetManualAutomationRun(definitionID, requestID string) (*AutomationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	var runID string
	err := s.db.QueryRow(`SELECT r.id FROM automation_occurrences o JOIN automation_runs r ON r.occurrence_id=o.id WHERE o.definition_id=? AND o.provider='manual' AND o.occurrence_key=?`, definitionID, "manual:"+requestID).Scan(&runID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.getAutomationRunUnlocked(runID)
}
func (s *Store) ListAutomationRuns(definitionID string) ([]AutomationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, e := s.db.Query(`SELECT `+automationRunColumns+` FROM automation_runs WHERE definition_id=? ORDER BY created_at DESC`, definitionID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []AutomationRun
	for rows.Next() {
		r, e := scanAutomationRun(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}
func (s *Store) ListPendingAutomationRuns() ([]AutomationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, e := s.db.Query(`SELECT ` + automationRunColumns + ` FROM automation_runs WHERE state='pending' ORDER BY created_at`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []AutomationRun
	for rows.Next() {
		r, e := scanAutomationRun(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}
func (s *Store) MarkAutomationRunDelivered(id, resolved string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec(`UPDATE automation_runs SET state='delivered',last_error='',resolved_location_json=?,updated_at=?,delivered_at=? WHERE id=?`, resolved, formatTicketTime(now), formatTicketTime(now), id)
	return e
}
func (s *Store) MarkAutomationRunFailed(id, message string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec(`UPDATE automation_runs SET state='failed',last_error=?,updated_at=? WHERE id=?`, message, formatTicketTime(now), id)
	return e
}

func (s *Store) AutomationOccurrencePayload(id string, out *string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	return s.db.QueryRow(`SELECT payload_json FROM automation_occurrences WHERE id=?`, id).Scan(out)
}

func (s *Store) GetAutomationOccurrence(id string) (*AutomationOccurrence, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, errors.New("automation persistence unavailable")
	}
	var occurrence AutomationOccurrence
	var observedAt, createdAt string
	err := s.db.QueryRow(`SELECT id,definition_id,provider,occurrence_key,subject_key,observed_at,payload_json,created_at FROM automation_occurrences WHERE id=?`, id).Scan(
		&occurrence.ID, &occurrence.DefinitionID, &occurrence.Provider, &occurrence.OccurrenceKey,
		&occurrence.SubjectKey, &observedAt, &occurrence.PayloadJSON, &createdAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	occurrence.ObservedAt = parseTicketTime(observedAt)
	occurrence.CreatedAt = parseTicketTime(createdAt)
	return &occurrence, nil
}
