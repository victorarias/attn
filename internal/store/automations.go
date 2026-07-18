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

type AutomationRunReservation struct {
	RunID, OccurrenceID, TicketID, SessionID, WorkspaceID, PaneID string
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
	var revision int
	var oldSpec string
	err = tx.QueryRow(`SELECT revision, spec_json FROM automation_definitions WHERE id=? AND deleted_at=''`, id).Scan(&revision, &oldSpec)
	switch err {
	case sql.ErrNoRows:
		revision = 1
		_, err = tx.Exec(`INSERT INTO automation_definitions(id,name,enabled,revision,spec_json,created_at,updated_at,deleted_at) VALUES(?,?,?,?,?,?,?,'')`, id, name, enabled, revision, specJSON, formatTicketTime(now), formatTicketTime(now))
	case nil:
		if oldSpec != specJSON {
			revision++
		}
		_, err = tx.Exec(`UPDATE automation_definitions SET name=?, enabled=?, revision=?, spec_json=?, updated_at=? WHERE id=?`, name, enabled, revision, specJSON, formatTicketTime(now), id)
	}
	if err != nil {
		return nil, err
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

func (s *Store) ClaimManualAutomationRun(definitionID, requestID, payloadJSON string, expectedRevision int, snapshotJSON string, observedAt time.Time, ids AutomationRunReservation) (*AutomationRun, bool, error) {
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
	if _, err = tx.Exec(`INSERT INTO automation_occurrences(id,definition_id,provider,occurrence_key,subject_key,observed_at,payload_json,created_at) VALUES(?,?, 'manual',?, '',?,?,?)`, ids.OccurrenceID, definitionID, key, now, payloadJSON, now); err != nil {
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
