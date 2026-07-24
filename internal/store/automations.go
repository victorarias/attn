package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func parseOptionalAutomationTime(value string) *time.Time {
	if value == "" {
		return nil
	}
	parsed := parseTicketTime(value)
	return &parsed
}

// Automation run states. A run's state transitions are one-way:
// pending -> delivered, pending -> failed, or pending -> cancelled.
const (
	AutomationRunStatePending   = "pending"
	AutomationRunStateDelivered = "delivered"
	AutomationRunStateFailed    = "failed"
	AutomationRunStateCancelled = "cancelled"
)

// Automation run cancel reasons, set alongside AutomationRunStateCancelled.
const (
	AutomationCancelReasonReviewWithdrawn    = "review_withdrawn"
	AutomationCancelReasonDefinitionDisabled = "definition_disabled"
	AutomationCancelReasonDefinitionDeleted  = "definition_deleted"
)

// Automation continuity binding statuses and release reasons. Bindings are
// append-only: a released row is never reactivated or deleted, only
// superseded by a fresh row on the next claim for that (definition,
// continuity_key).
const (
	AutomationBindingStatusActive   = "active"
	AutomationBindingStatusReleased = "released"
)
const (
	AutomationBindingReleasedContractRotated   = "contract_rotated"
	AutomationBindingReleasedTicketSwept       = "ticket_swept"
	AutomationBindingReleasedDefinitionDeleted = "definition_deleted"
)

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
	SnapshotJSON, State, CancelReason        string
	Attempts                                 int
	LastError                                string
	TicketID, SessionID, WorkspaceID, PaneID string
	ResolvedLocationJSON                     string
	CreatedAt, UpdatedAt                     time.Time
	DeliveredAt                              *time.Time
}

// AutomationContinuityBinding is one append-only row recording a continuity
// thread's stable ticket/session/workspace/pane identity. A definition/
// continuity_key pair has at most one active row at a time (enforced by
// idx_automation_bindings_active); releasing it never deletes the row, it
// only flips status and records why, so a later claim for the same key
// appends a fresh active row rather than resurrecting the old one.
type AutomationContinuityBinding struct {
	ID, DefinitionID, ContinuityKey          string
	TicketID, SessionID, WorkspaceID, PaneID string
	Status, ReleasedReason                   string
	ReleasedAt                               *time.Time
	CreatedAt, UpdatedAt                     time.Time
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

func (s *Store) AutomationReviewRequestNeedsClaim(definitionID, subjectKey string, cycle int) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return false, errors.New("automation persistence unavailable")
	}
	var active, currentCycle int
	err := s.db.QueryRow(`SELECT active,cycle FROM automation_review_request_edges WHERE definition_id=? AND subject_key=?`, definitionID, subjectKey).Scan(&active, &currentCycle)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if active != 1 || currentCycle != cycle {
		return false, nil
	}
	occurrenceKey := fmt.Sprintf("review_requested:%s:%d", subjectKey, cycle)
	var state string
	err = s.db.QueryRow(`
		SELECT r.state
		FROM automation_occurrences o
		JOIN automation_runs r ON r.occurrence_id=o.id
		WHERE o.definition_id=? AND o.provider='github' AND o.occurrence_key=?
	`, definitionID, occurrenceKey).Scan(&state)
	if err == sql.ErrNoRows {
		// No run has ever been claimed for this cycle yet.
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return state == AutomationRunStatePending, nil
}

// UpsertAutomationDefinition applies a definition's name/spec_json. `enabled`
// is not a parameter: it has exactly one authority, the enabled COLUMN. A
// brand-new row (including the resurrection of a soft-deleted one) is always
// inserted enabled; an update of a live row leaves its current enabled value
// untouched — apply never toggles enabled, only SetAutomationEnabled does.
func (s *Store) UpsertAutomationDefinition(id, name, specJSON string, now time.Time) (*AutomationDefinition, error) {
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
	var oldSpec, deletedAt string
	// Deliberately not filtered by deleted_at='': a soft-deleted row must be
	// found here too, so applying the same id resurrects it (clears
	// deleted_at) instead of colliding with the PRIMARY KEY on an INSERT.
	err = tx.QueryRow(`SELECT revision, spec_json, enabled, deleted_at FROM automation_definitions WHERE id=?`, id).Scan(&revision, &oldSpec, &oldEnabled, &deletedAt)
	enabled := true
	switch err {
	case sql.ErrNoRows:
		revision = 1
		_, err = tx.Exec(`INSERT INTO automation_definitions(id,name,enabled,revision,spec_json,created_at,updated_at,deleted_at) VALUES(?,?,?,?,?,?,?,'')`, id, name, enabled, revision, specJSON, formatTicketTime(now), formatTicketTime(now))
	case nil:
		wasDeleted := deletedAt != ""
		if wasDeleted {
			// Resurrection always re-enables and always bumps revision, even with
			// an unchanged spec, so the daemon's contract comparison (old revision
			// vs new) can tell resurrection apart from a no-op reapply and always
			// rotate continuity bindings for it.
			revision++
		} else {
			enabled = oldEnabled != 0
			if oldSpec != specJSON {
				revision++
			}
		}
		_, err = tx.Exec(`UPDATE automation_definitions SET name=?, enabled=?, revision=?, spec_json=?, updated_at=?, deleted_at='' WHERE id=?`, name, enabled, revision, specJSON, formatTicketTime(now), id)
		if err == nil && wasDeleted {
			// Re-enabling (via resurrection) begins from the provider's current
			// truth. A request that arrived while disabled/deleted must be eligible
			// for latest catch-up even if an older cycle for the same subject had
			// been active before. Shared with SetAutomationEnabled's own
			// enabled-state transition.
			err = clearAutomationReviewRequestEdgesTx(tx, id, now)
		}
	}
	if err != nil {
		return nil, err
	}
	if enabled {
		// Fence provider snapshots that began before this definition became
		// active (or was reapplied). Observation timestamps are captured before
		// provider fetches, so an in-flight stale refresh cannot launch work under
		// a newly enabled revision. Unlike the edge clear above, this runs on
		// every apply that leaves the definition enabled, not just a transition.
		if err := fenceAutomationProviderCursorsTx(tx, id, now); err != nil {
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

// clearAutomationReviewRequestEdgesTx deactivates every review-request edge for
// a definition, so a fresh provider observation after re-enabling accepts
// latest demand instead of an older, possibly-stale cycle.
func clearAutomationReviewRequestEdgesTx(tx *sql.Tx, definitionID string, now time.Time) error {
	_, err := tx.Exec(`UPDATE automation_review_request_edges SET active=0,updated_at=? WHERE definition_id=?`, formatTicketTime(now), definitionID)
	return err
}

// fenceAutomationProviderCursorsTx records the instant a definition became (or
// remained) enabled as its github_review_requested provider fence. Observation
// timestamps are captured before provider fetches, so an in-flight stale
// refresh started before this fence cannot launch work under the newly
// enabled/reapplied revision.
func fenceAutomationProviderCursorsTx(tx *sql.Tx, definitionID string, now time.Time) error {
	fence := now.UTC().Format(time.RFC3339Nano)
	_, err := tx.Exec(`INSERT INTO automation_provider_cursors(definition_id,provider,scope,observed_at) VALUES(?,'github_review_requested','*',?) ON CONFLICT(definition_id,provider,scope) DO UPDATE SET observed_at=excluded.observed_at`, definitionID, fence)
	return err
}

// SetAutomationEnabled flips a definition's enabled flag and, on an actual
// disabled->enabled transition, performs the same store-side effects Upsert
// does for that transition: clearing review-request edges and fencing
// provider cursors. It is a no-op (changed=false, no side effects, no
// revision bump) when the definition already has the requested state.
// Returns (nil, false, nil) for an unknown or soft-deleted definition.
//
// Unlike UpsertAutomationDefinition, this is not a spec apply — the caller
// (the panel's toggle, or the CLI's enable/disable verbs) supplies only a
// bool, not a new spec. `enabled` has exactly one authority, the enabled
// COLUMN — the spec carries no enabled field at all — so there is nothing
// else to keep in sync here.
//
// revision does NOT bump on this transition: revision guards spec content,
// and enabled is no longer spec content, so a toggle changes nothing a
// concurrent editor's stale-save guard (automationApplyWithGuards, which
// compares only revision) needs to catch.
func (s *Store) SetAutomationEnabled(id string, enabled bool, now time.Time) (*AutomationDefinition, bool, error) {
	s.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()
	if s.db == nil {
		return nil, false, errors.New("automation persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var oldEnabled int
	err = tx.QueryRow(`SELECT enabled FROM automation_definitions WHERE id=? AND deleted_at=''`, id).Scan(&oldEnabled)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	wasEnabled := oldEnabled != 0
	if wasEnabled == enabled {
		tx.Rollback()
		s.mu.Unlock()
		locked = false
		def, getErr := s.GetAutomationDefinition(id)
		return def, false, getErr
	}
	if _, err := tx.Exec(`UPDATE automation_definitions SET enabled=?, updated_at=? WHERE id=?`, enabled, formatTicketTime(now), id); err != nil {
		return nil, false, err
	}
	if enabled {
		if err := clearAutomationReviewRequestEdgesTx(tx, id, now); err != nil {
			return nil, false, err
		}
		if err := fenceAutomationProviderCursorsTx(tx, id, now); err != nil {
			return nil, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	s.mu.Unlock()
	locked = false
	def, getErr := s.GetAutomationDefinition(id)
	return def, true, getErr
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

// GetAutomationDefinitionIncludingDeleted reads a definition regardless of
// soft-delete state, for automationApply's pre-upsert load (to detect
// resurrection and compare the old ContinuationContract, see
// internal/automation) and for callers that need to confirm a definition
// once existed.
func (s *Store) GetAutomationDefinitionIncludingDeleted(id string) (*AutomationDefinition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	d, err := scanAutomationDefinition(s.db.QueryRow(`SELECT id,name,enabled,revision,spec_json,created_at,updated_at,deleted_at FROM automation_definitions WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

const automationContinuityBindingColumns = `id,definition_id,continuity_key,ticket_id,session_id,workspace_id,pane_id,status,released_reason,released_at,created_at,updated_at`

func scanAutomationContinuityBinding(scanner interface{ Scan(...any) error }) (*AutomationContinuityBinding, error) {
	var b AutomationContinuityBinding
	var releasedAt, created, updated string
	if err := scanner.Scan(&b.ID, &b.DefinitionID, &b.ContinuityKey, &b.TicketID, &b.SessionID, &b.WorkspaceID, &b.PaneID, &b.Status, &b.ReleasedReason, &releasedAt, &created, &updated); err != nil {
		return nil, err
	}
	b.ReleasedAt = parseOptionalAutomationTime(releasedAt)
	b.CreatedAt = parseTicketTime(created)
	b.UpdatedAt = parseTicketTime(updated)
	return &b, nil
}

// GetActiveAutomationContinuityBinding returns the active binding row for
// (definitionID, continuityKey), or nil if none is active. There is at most
// one, enforced by idx_automation_bindings_active.
func (s *Store) GetActiveAutomationContinuityBinding(definitionID, continuityKey string) (*AutomationContinuityBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, errors.New("automation persistence unavailable")
	}
	b, err := scanAutomationContinuityBinding(s.db.QueryRow(`SELECT `+automationContinuityBindingColumns+` FROM automation_continuity_bindings WHERE definition_id=? AND continuity_key=? AND status=?`, definitionID, continuityKey, AutomationBindingStatusActive))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return b, err
}

// ReleaseAutomationContinuityBinding releases the active binding row for
// (definitionID, continuityKey), if any — a no-op (not an error) when there
// is none. The row is never deleted: status flips to released and
// released_reason/released_at record why and when, so the next claim for
// this key appends a fresh active row instead of reusing this one.
func (s *Store) ReleaseAutomationContinuityBinding(definitionID, continuityKey, reason string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	_, err := s.db.Exec(
		`UPDATE automation_continuity_bindings SET status=?,released_reason=?,released_at=?,updated_at=? WHERE definition_id=? AND continuity_key=? AND status=?`,
		AutomationBindingStatusReleased, reason, formatTicketTime(now), formatTicketTime(now), definitionID, continuityKey, AutomationBindingStatusActive,
	)
	return err
}

// ReleaseAutomationContinuityBindings releases every active binding row for
// definitionID (see ReleaseAutomationContinuityBinding) — used when a
// definition's contract is deleted wholesale (automationDelete), rather than
// one continuity key at a time.
func (s *Store) ReleaseAutomationContinuityBindings(definitionID, reason string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	_, err := s.db.Exec(
		`UPDATE automation_continuity_bindings SET status=?,released_reason=?,released_at=?,updated_at=? WHERE definition_id=? AND status=?`,
		AutomationBindingStatusReleased, reason, formatTicketTime(now), formatTicketTime(now), definitionID, AutomationBindingStatusActive,
	)
	return err
}

// getOrCreateActiveAutomationContinuityBindingTx resolves the active binding
// for (definitionID, continuityKey), reusing its ticket/session/workspace/
// pane ids into ids when one exists, or appending a fresh active row seeded
// from ids when none does. Bindings are never updated back to active and
// never deleted (see AutomationContinuityBinding's doc comment) — a released
// row is simply superseded by this fresh one.
func getOrCreateActiveAutomationContinuityBindingTx(tx *sql.Tx, definitionID, continuityKey string, ids *AutomationRunReservation, now time.Time) error {
	var createdAt, updatedAt string
	err := tx.QueryRow(
		`SELECT ticket_id,session_id,workspace_id,pane_id,created_at,updated_at FROM automation_continuity_bindings WHERE definition_id=? AND continuity_key=? AND status=?`,
		definitionID, continuityKey, AutomationBindingStatusActive,
	).Scan(&ids.TicketID, &ids.SessionID, &ids.WorkspaceID, &ids.PaneID, &createdAt, &updatedAt)
	switch err {
	case sql.ErrNoRows:
		nowRaw := formatTicketTime(now)
		_, err = tx.Exec(
			`INSERT INTO automation_continuity_bindings(id,definition_id,continuity_key,ticket_id,session_id,workspace_id,pane_id,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			uuid.NewString(), definitionID, continuityKey, ids.TicketID, ids.SessionID, ids.WorkspaceID, ids.PaneID, AutomationBindingStatusActive, nowRaw, nowRaw,
		)
		return err
	case nil:
		return nil
	default:
		return err
	}
}

// AutomationSessionHasContinuityBinding reports whether sessionID is still
// referenced by some ACTIVE continuity binding, checked globally across every
// definition rather than scoped to one. That matches
// automationRunWorktreePath's worktree layout (worktrees/<sessionID>/<repo>),
// which is keyed on session id alone: a bound thread's shared worktree can
// only be identified by session id, not by definition. The daemon's
// automationRunCleanupSafety uses this to protect that shared worktree even
// once the thread's own session row has been garbage collected — the
// session-liveness check above it only catches the case where the row is
// still there. A released binding no longer protects its worktree.
func (s *Store) AutomationSessionHasContinuityBinding(sessionID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if sessionID == "" {
		return false, nil
	}
	if s.db == nil {
		return false, errors.New("automation persistence unavailable")
	}
	var exists int
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM automation_continuity_bindings WHERE session_id=? AND status=?)`, sessionID, AutomationBindingStatusActive).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists != 0, nil
}

// DeleteAutomationReviewRequestEdges removes every review-request edge for a
// definition. Used only by automationDelete: a soft-deleted definition's
// provider-side review-request tracking is fully retired, unlike a
// re-enable (clearAutomationReviewRequestEdgesTx) which only deactivates
// edges so the next observation starts a fresh cycle.
func (s *Store) DeleteAutomationReviewRequestEdges(definitionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	_, err := s.db.Exec(`DELETE FROM automation_review_request_edges WHERE definition_id=?`, definitionID)
	return err
}

// FenceAutomationProviderCursors records now as a definition's
// github_review_requested provider fence, so an in-flight stale observation
// started before this call cannot act on the definition afterward. Mirrors
// the fencing UpsertAutomationDefinition performs inline on every enabled
// apply (fenceAutomationProviderCursorsTx); automationDelete calls this
// directly since a delete isn't part of an apply's own transaction.
func (s *Store) FenceAutomationProviderCursors(definitionID string, now time.Time) error {
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
	if err := fenceAutomationProviderCursorsTx(tx, definitionID, now); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteAutomationDefinition soft-deletes a definition by setting
// deleted_at. Runs, occurrences, tickets, sessions, and on-disk artifacts are
// untouched here — automationDelete fails pending runs and clears
// continuity/review-request state before calling this. Returns an error for
// an unknown or already-deleted id.
func (s *Store) DeleteAutomationDefinition(id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	res, err := s.db.Exec(`UPDATE automation_definitions SET deleted_at=?, updated_at=? WHERE id=? AND deleted_at=''`, formatTicketTime(now), formatTicketTime(now), id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("automation %q not found or already deleted", id)
	}
	return nil
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

// ListAutomationDefinitionIDsIncludingDeleted returns every definition id,
// including soft-deleted ones. Unlike ListAutomationDefinitions (which the
// UI/CLI use and which filters deleted_at=”), the A3 retention sweep and A4
// cleanup both need to reach a deleted definition's runs too — deleting a
// definition retires it but explicitly leaves its runs/artifacts for these
// two mechanisms to eventually clean up (see automationDelete's doc comment).
func (s *Store) ListAutomationDefinitionIDsIncludingDeleted() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT id FROM automation_definitions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
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
	if _, err = tx.Exec(`INSERT INTO automation_runs(id,definition_id,occurrence_id,definition_revision,snapshot_json,state,ticket_id,session_id,workspace_id,pane_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, ids.RunID, definitionID, ids.OccurrenceID, revision, snapshotJSON, AutomationRunStatePending, ids.TicketID, ids.SessionID, ids.WorkspaceID, ids.PaneID, now, now); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	run, e := s.getAutomationRunUnlocked(ids.RunID)
	return run, true, e
}

// ClaimScheduledAutomationRun claims one scheduled occurrence, keyed by the
// intended instant's occurrence key. Idempotent on (definition_id, provider,
// occurrence_key): a second claim for the same key returns the existing run
// without consuming the reservation. expectedRevision guards against the
// observation race where the snapshot was built from a definition revision
// read before this transaction: a mismatch rejects the claim and lets the
// next tick re-read and re-decide, mirroring ClaimGitHubReviewAutomationRun.
// continuityKey is "" for scheduled continuity fresh (every occurrence gets
// its own reservation IDs) or "singleton" for scheduled continuity singleton,
// in which case the reservation's ticket/session/workspace/pane IDs are bound
// once per definition (via the active continuity binding row) and reused by
// every later occurrence, mirroring ClaimGitHubReviewAutomationRun's
// per-subject binding reuse.
func (s *Store) ClaimScheduledAutomationRun(definitionID, occurrenceKey, continuityKey string, expectedRevision int, payloadJSON, snapshotJSON string, observedAt time.Time, reservation AutomationRunReservation) (*AutomationRun, bool, error) {
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
	var existingID string
	err = tx.QueryRow(`SELECT r.id FROM automation_occurrences o JOIN automation_runs r ON r.occurrence_id=o.id WHERE o.definition_id=? AND o.provider='schedule' AND o.occurrence_key=?`, definitionID, occurrenceKey).Scan(&existingID)
	if err == nil {
		tx.Rollback()
		run, e := s.getAutomationRunUnlocked(existingID)
		return run, false, e
	}
	if err != sql.ErrNoRows {
		return nil, false, err
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
	ids := reservation
	if continuityKey != "" {
		// A later occurrence must not overtake an earlier one whose ticket has
		// not been created yet: delivery would otherwise mistake the
		// not-yet-created ticket for one already swept, the same hazard
		// ClaimGitHubReviewAutomationRun guards against per subject.
		var undeliveredPredecessor int
		if err := tx.QueryRow(`
			SELECT EXISTS(
				SELECT 1
				FROM automation_runs r
				JOIN automation_occurrences o ON o.id=r.occurrence_id
				WHERE r.definition_id=? AND o.provider='schedule' AND r.state=?
				  AND NOT EXISTS (SELECT 1 FROM tickets t WHERE t.id=r.ticket_id)
			)
		`, definitionID, AutomationRunStatePending).Scan(&undeliveredPredecessor); err != nil {
			return nil, false, err
		}
		if undeliveredPredecessor != 0 {
			return nil, false, errors.New("an earlier scheduled automation run for this definition has not created its ticket yet")
		}
		if err := getOrCreateActiveAutomationContinuityBindingTx(tx, definitionID, continuityKey, &ids, observedAt); err != nil {
			return nil, false, err
		}
	}
	now := formatTicketTime(observedAt)
	// subject_key is recorded as continuityKey (not always ""): binding
	// lookups elsewhere key off of it for a scheduled singleton's history.
	if _, err = tx.Exec(`INSERT INTO automation_occurrences(id,definition_id,provider,occurrence_key,subject_key,observed_at,payload_json,created_at) VALUES(?,?, 'schedule',?,?,?,?,?)`, ids.OccurrenceID, definitionID, occurrenceKey, continuityKey, now, payloadJSON, now); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(`INSERT INTO automation_runs(id,definition_id,occurrence_id,definition_revision,snapshot_json,state,ticket_id,session_id,workspace_id,pane_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, ids.RunID, definitionID, ids.OccurrenceID, revision, snapshotJSON, AutomationRunStatePending, ids.TicketID, ids.SessionID, ids.WorkspaceID, ids.PaneID, now, now); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	run, e := s.getAutomationRunUnlocked(ids.RunID)
	return run, true, e
}

// GetAutomationScheduleCursor returns the last observed instant recorded by
// SetAutomationScheduleCursor for this definition, ok=false when unset.
func (s *Store) GetAutomationScheduleCursor(definitionID string) (time.Time, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return time.Time{}, false, errors.New("automation persistence unavailable")
	}
	var raw string
	err := s.db.QueryRow(`SELECT observed_at FROM automation_provider_cursors WHERE definition_id=? AND provider='schedule' AND scope='*'`, definitionID).Scan(&raw)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse automation schedule cursor: %w", err)
	}
	return at, true, nil
}

// SetAutomationScheduleCursor records the schedule's cursor instant, on the
// shared automation_provider_cursors table (provider "schedule", scope "*").
func (s *Store) SetAutomationScheduleCursor(definitionID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	_, err := s.db.Exec(`INSERT INTO automation_provider_cursors(definition_id,provider,scope,observed_at) VALUES(?,'schedule','*',?) ON CONFLICT(definition_id,provider,scope) DO UPDATE SET observed_at=excluded.observed_at`, definitionID, at.UTC().Format(time.RFC3339Nano))
	return err
}

// ReconcileAutomationReviewRequests records the provider's complete current
// review-request demand for one host. It returns active edges whose current
// cycle either has no run yet or still owns a pending run, so both
// detail-fetch and delivery failures retry on a later observation without
// inventing a cycle.
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
			_, err = tx.Exec(`INSERT INTO automation_review_request_edges(definition_id,subject_key,host,active,cycle,last_observed_at,updated_at) VALUES(?,?,?,1,1,?,?)`, definitionID, subjectKey, host, observedRaw, updatedRaw)
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
		// The binding stays active as long as its ticket exists (a re-request
		// continues the thread); only when the ticket is genuinely gone does
		// withdrawal release the binding, via ReleaseAutomationContinuityBinding
		// (see automations_github.go's cancellation path) rather than deleting
		// this row.
		if _, err := tx.Exec(`
			UPDATE automation_continuity_bindings
			SET status=?,released_reason=?,released_at=?,updated_at=?
			WHERE definition_id=? AND continuity_key=? AND status=?
			  AND NOT EXISTS (
				SELECT 1 FROM tickets
				WHERE tickets.id=automation_continuity_bindings.ticket_id
			  )
		`, AutomationBindingStatusReleased, AutomationBindingReleasedTicketSwept, updatedRaw, updatedRaw, definitionID, subjectKey, AutomationBindingStatusActive); err != nil {
			return nil, err
		}
	}
	rows, err = tx.Query(`SELECT subject_key,cycle FROM automation_review_request_edges WHERE definition_id=? AND host=? AND active=1 ORDER BY subject_key`, definitionID, host)
	if err != nil {
		return nil, err
	}
	type activeReviewEdge struct {
		subjectKey string
		cycle      int
	}
	var activeEdges []activeReviewEdge
	for rows.Next() {
		var edge activeReviewEdge
		if err := rows.Scan(&edge.subjectKey, &edge.cycle); err != nil {
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
		occurrenceKey := fmt.Sprintf("review_requested:%s:%d", edge.subjectKey, edge.cycle)
		var state string
		err := tx.QueryRow(`
			SELECT r.state
			FROM automation_occurrences o
			JOIN automation_runs r ON r.occurrence_id=o.id
			WHERE o.definition_id=? AND o.provider='github' AND o.occurrence_key=?
		`, definitionID, occurrenceKey).Scan(&state)
		switch err {
		case sql.ErrNoRows:
			// No run has ever been claimed for this cycle: it's a candidate.
			candidates = append(candidates, AutomationReviewRequestCandidate{SubjectKey: edge.subjectKey, Cycle: edge.cycle})
		case nil:
			if state == AutomationRunStatePending {
				candidates = append(candidates, AutomationReviewRequestCandidate{SubjectKey: edge.subjectKey, Cycle: edge.cycle})
			}
		default:
			return nil, err
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
	var active, cycle int
	err := s.db.QueryRow(`
		SELECT o.occurrence_key,o.subject_key,e.active,e.cycle
		FROM automation_runs r
		JOIN automation_occurrences o ON o.id=r.occurrence_id
		JOIN automation_review_request_edges e
		  ON e.definition_id=r.definition_id AND e.subject_key=o.subject_key
		WHERE r.id=? AND o.provider='github'
	`, runID).Scan(&occurrenceKey, &subjectKey, &active, &cycle)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	wantKey := fmt.Sprintf("review_requested:%s:%d", subjectKey, cycle)
	return active == 1 && occurrenceKey == wantKey, nil
}

// ListWithdrawnGitHubReviewUndeliveredRuns returns the current withdrawn
// cycle's pending run, or its cancelled run when cancellation still needs to
// be reconciled (e.g. the daemon exited between recording withdrawal and
// stopping a partially launched PTY). Delivery is the handoff to ordinary
// ticket/session ownership; persisted stable IDs alone are not, because they
// may precede successful launch verification.
func (s *Store) ListWithdrawnGitHubReviewUndeliveredRuns(definitionID, host string) ([]AutomationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, errors.New("automation persistence unavailable")
	}
	rows, err := s.db.Query(`
		SELECT `+automationRunColumnsQualified+`
		FROM automation_runs r
		JOIN automation_occurrences o ON o.id=r.occurrence_id
		JOIN automation_review_request_edges e
		  ON e.definition_id=r.definition_id AND e.subject_key=o.subject_key
		WHERE r.definition_id=? AND e.host=? AND e.active=0
		  AND (r.state=? OR (r.state=? AND r.cancel_reason=?))
		  AND o.provider='github'
		  AND o.occurrence_key=('review_requested:' || e.subject_key || ':' || CAST(e.cycle AS TEXT))
		ORDER BY r.created_at
	`, definitionID, host, AutomationRunStatePending, AutomationRunStateCancelled, AutomationCancelReasonReviewWithdrawn)
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
	var active, currentCycle int
	if err := tx.QueryRow(`SELECT active,cycle FROM automation_review_request_edges WHERE definition_id=? AND subject_key=?`, definitionID, subjectKey).Scan(&active, &currentCycle); err != nil {
		return nil, false, err
	}
	if active == 0 || currentCycle != cycle {
		return nil, false, errors.New("review-request edge changed before occurrence claim")
	}
	occurrenceKey := fmt.Sprintf("review_requested:%s:%d", subjectKey, cycle)
	// Idempotent on (definition_id, provider, occurrence_key): any existing
	// run for this exact cycle — pending, delivered, failed, or cancelled —
	// is returned as-is rather than reclaimed. A pending run is re-delivered
	// by the caller on this same observation (see
	// AutomationReviewRequestNeedsClaim/ReconcileAutomationReviewRequests'
	// candidacy check), not by minting a second run row here.
	var existingID string
	err = tx.QueryRow(`SELECT r.id FROM automation_occurrences o JOIN automation_runs r ON r.occurrence_id=o.id WHERE o.definition_id=? AND o.provider='github' AND o.occurrence_key=?`, definitionID, occurrenceKey).Scan(&existingID)
	if err == nil {
		tx.Rollback()
		run, e := s.getAutomationRunUnlocked(existingID)
		return run, false, e
	}
	if err != sql.ErrNoRows {
		return nil, false, err
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
			WHERE r.definition_id=? AND o.subject_key=? AND r.state=?
			  AND NOT EXISTS (SELECT 1 FROM tickets t WHERE t.id=r.ticket_id)
		)
	`, definitionID, subjectKey, AutomationRunStatePending).Scan(&undeliveredPredecessor); err != nil {
		return nil, false, err
	}
	if undeliveredPredecessor != 0 {
		return nil, false, errors.New("an earlier automation run for this subject has not created its ticket yet")
	}
	ids := reserved
	if err := getOrCreateActiveAutomationContinuityBindingTx(tx, definitionID, subjectKey, &ids, observedAt); err != nil {
		return nil, false, err
	}
	now := formatTicketTime(observedAt)
	if _, err = tx.Exec(`INSERT INTO automation_occurrences(id,definition_id,provider,occurrence_key,subject_key,observed_at,payload_json,created_at) VALUES(?,?, 'github',?,?,?,?,?)`, ids.OccurrenceID, definitionID, occurrenceKey, subjectKey, now, payloadJSON, now); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(`INSERT INTO automation_runs(id,definition_id,occurrence_id,definition_revision,snapshot_json,state,ticket_id,session_id,workspace_id,pane_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, ids.RunID, definitionID, ids.OccurrenceID, revision, snapshotJSON, AutomationRunStatePending, ids.TicketID, ids.SessionID, ids.WorkspaceID, ids.PaneID, now, now); err != nil {
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

func scanAutomationRun(scanner interface{ Scan(...any) error }) (*AutomationRun, error) {
	var r AutomationRun
	var created, updated, delivered string
	err := scanner.Scan(&r.ID, &r.DefinitionID, &r.OccurrenceID, &r.DefinitionRevision, &r.SnapshotJSON, &r.State, &r.CancelReason, &r.Attempts, &r.LastError, &r.TicketID, &r.SessionID, &r.WorkspaceID, &r.PaneID, &r.ResolvedLocationJSON, &created, &updated, &delivered)
	if err != nil {
		return nil, err
	}
	r.CreatedAt = parseTicketTime(created)
	r.UpdatedAt = parseTicketTime(updated)
	r.DeliveredAt = parseOptionalAutomationTime(delivered)
	return &r, nil
}

const automationRunColumns = `id,definition_id,occurrence_id,definition_revision,snapshot_json,state,cancel_reason,attempts,last_error,ticket_id,session_id,workspace_id,pane_id,resolved_location_json,created_at,updated_at,delivered_at`

// automationRunColumnsQualified is automationRunColumns with each column
// prefixed by the automation_runs alias `r`, for queries that join against
// another table (automation_occurrences, automation_review_request_edges)
// which also has an `id` column — an unqualified `id` there is ambiguous.
const automationRunColumnsQualified = `r.id,r.definition_id,r.occurrence_id,r.definition_revision,r.snapshot_json,r.state,r.cancel_reason,r.attempts,r.last_error,r.ticket_id,r.session_id,r.workspace_id,r.pane_id,r.resolved_location_json,r.created_at,r.updated_at,r.delivered_at`

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

// AutomationRunWithOccurrenceKey pairs a run with its occurrence's
// occurrence_key, for surfaces (the WS runs list) that need to show which
// provider occurrence produced the run without exposing its full payload.
type AutomationRunWithOccurrenceKey struct {
	AutomationRun
	OccurrenceKey string
}

// ListAutomationRunsWithOccurrenceKeys returns up to limit runs for
// definitionID, newest first, each carrying its occurrence's occurrence_key
// via one join (mirrors ListAutomationRuns's columns/ordering).
func (s *Store) ListAutomationRunsWithOccurrenceKeys(definitionID string, limit int) ([]AutomationRunWithOccurrenceKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT `+automationRunColumnsQualified+`,o.occurrence_key
		FROM automation_runs r
		JOIN automation_occurrences o ON o.id=r.occurrence_id
		WHERE r.definition_id=?
		ORDER BY r.created_at DESC
		LIMIT ?
	`, definitionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationRunWithOccurrenceKey
	for rows.Next() {
		var r AutomationRun
		var created, updated, delivered, occurrenceKey string
		if err := rows.Scan(&r.ID, &r.DefinitionID, &r.OccurrenceID, &r.DefinitionRevision, &r.SnapshotJSON, &r.State, &r.CancelReason, &r.Attempts, &r.LastError, &r.TicketID, &r.SessionID, &r.WorkspaceID, &r.PaneID, &r.ResolvedLocationJSON, &created, &updated, &delivered, &occurrenceKey); err != nil {
			return nil, err
		}
		r.CreatedAt = parseTicketTime(created)
		r.UpdatedAt = parseTicketTime(updated)
		r.DeliveredAt = parseOptionalAutomationTime(delivered)
		out = append(out, AutomationRunWithOccurrenceKey{AutomationRun: r, OccurrenceKey: occurrenceKey})
	}
	return out, rows.Err()
}

// LatestAutomationRunPerDefinition returns, for every definition that has at
// least one run, its single most-recent run (by created_at, ties broken by
// id) paired with its occurrence's occurrence_key — one query, used to embed
// last_run in the definitions listing instead of the panel issuing one
// automation_runs_get per definition. A definition with zero runs has no
// entry in the returned map.
func (s *Store) LatestAutomationRunPerDefinition() (map[string]AutomationRunWithOccurrenceKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT ` + automationRunColumnsQualified + `,o.occurrence_key
		FROM automation_runs r
		JOIN automation_occurrences o ON o.id=r.occurrence_id
		WHERE r.id IN (
			SELECT id FROM (
				SELECT id, definition_id,
					ROW_NUMBER() OVER (PARTITION BY definition_id ORDER BY created_at DESC, id DESC) AS rn
				FROM automation_runs
			) WHERE rn = 1
		)
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]AutomationRunWithOccurrenceKey)
	for rows.Next() {
		var r AutomationRun
		var created, updated, delivered, occurrenceKey string
		if err := rows.Scan(&r.ID, &r.DefinitionID, &r.OccurrenceID, &r.DefinitionRevision, &r.SnapshotJSON, &r.State, &r.CancelReason, &r.Attempts, &r.LastError, &r.TicketID, &r.SessionID, &r.WorkspaceID, &r.PaneID, &r.ResolvedLocationJSON, &created, &updated, &delivered, &occurrenceKey); err != nil {
			return nil, err
		}
		r.CreatedAt = parseTicketTime(created)
		r.UpdatedAt = parseTicketTime(updated)
		r.DeliveredAt = parseOptionalAutomationTime(delivered)
		out[r.DefinitionID] = AutomationRunWithOccurrenceKey{AutomationRun: r, OccurrenceKey: occurrenceKey}
	}
	return out, rows.Err()
}

func (s *Store) ListPendingAutomationRuns() ([]AutomationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, e := s.db.Query(`SELECT `+automationRunColumns+` FROM automation_runs WHERE state=? ORDER BY created_at`, AutomationRunStatePending)
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
	_, e := s.db.Exec(`UPDATE automation_runs SET state=?,last_error='',resolved_location_json=?,updated_at=?,delivered_at=? WHERE id=?`, AutomationRunStateDelivered, resolved, formatTicketTime(now), formatTicketTime(now), id)
	return e
}
func (s *Store) MarkAutomationRunFailed(id, message string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec(`UPDATE automation_runs SET state=?,last_error=?,updated_at=? WHERE id=?`, AutomationRunStateFailed, message, formatTicketTime(now), id)
	return e
}

// MarkAutomationRunCancelled transitions a run to cancelled, recording why.
// It sets state and cancel_reason only — last_error, resolved_location_json,
// and every other column are left as they were, since cancellation is not a
// delivery failure: it's withdrawal, disable, or delete overtaking a run
// that hadn't been delivered yet.
func (s *Store) MarkAutomationRunCancelled(id, reason string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	_, err := s.db.Exec(`UPDATE automation_runs SET state=?,cancel_reason=?,updated_at=? WHERE id=?`, AutomationRunStateCancelled, reason, formatTicketTime(now), id)
	return err
}

// ListPrunableAutomationRuns returns definitionID's terminal (delivered,
// failed, or cancelled) runs older than olderThan that fall outside the
// newest keep runs (by created_at, across all states — a pending run still
// counts toward keep, so it DOES bump an older terminal run out of
// protection, same as any other run would), and are not the origin run of a
// still-bound continuity thread (tickets.automation_run_id is set once at
// thread creation and never updated, so it always points at the thread's
// oldest run — pruning it would permanently break every later occurrence,
// see automationContinuationOrigin). Session liveness and worktree
// cleanliness are daemon-side concerns this package cannot check; this only
// narrows by count/state/age/origin, per A3's fixed retention policy.
// Cancelled runs share failed's retention window rather than a separate one:
// both are non-delivered terminal outcomes with the same evidentiary value.
func (s *Store) ListPrunableAutomationRuns(definitionID string, keep int, olderThan time.Time) ([]AutomationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT `+automationRunColumns+`
		FROM automation_runs
		WHERE definition_id=? AND state IN (?,?,?) AND created_at<?
		  AND id NOT IN (
			SELECT id FROM automation_runs WHERE definition_id=? ORDER BY created_at DESC LIMIT ?
		  )
		  -- A still-bound continuity thread's origin run is never prunable: see
		  -- this function's doc comment.
		  AND id NOT IN (
			SELECT t.automation_run_id FROM tickets t
			JOIN automation_continuity_bindings b ON b.ticket_id = t.id
			WHERE t.automation_run_id IS NOT NULL AND t.automation_run_id <> ''
		  )
		ORDER BY created_at
	`, definitionID, AutomationRunStateDelivered, AutomationRunStateFailed, AutomationRunStateCancelled, formatTicketTime(olderThan), definitionID, keep)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationRun
	for rows.Next() {
		r, err := scanAutomationRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ListTerminalAutomationRuns returns every delivered/failed/cancelled run for
// definitionID, oldest first, with no count or age gate — unlike
// ListPrunableAutomationRuns, which only surfaces runs outside the retention
// window. A4's explicit "automation cleanup" command uses this: it reclaims
// disk space for every terminal run right now, not just the ones retention
// would eventually get to.
func (s *Store) ListTerminalAutomationRuns(definitionID string) ([]AutomationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT `+automationRunColumns+`
		FROM automation_runs
		WHERE definition_id=? AND state IN (?,?,?)
		ORDER BY created_at
	`, definitionID, AutomationRunStateDelivered, AutomationRunStateFailed, AutomationRunStateCancelled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationRun
	for rows.Next() {
		r, err := scanAutomationRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// DeleteAutomationRun removes a run and its occurrence row in one
// transaction. It does not touch on-disk artifacts (worktrees, occurrence
// JSON) — callers (the A3 retention sweep, A4 cleanup does not call this)
// remove those first. Deleting an already-gone run is a no-op, not an error,
// since sweep candidates are gathered once and acted on afterward.
func (s *Store) DeleteAutomationRun(runID string) error {
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
	var occurrenceID string
	err = tx.QueryRow(`SELECT occurrence_id FROM automation_runs WHERE id=?`, runID).Scan(&occurrenceID)
	if err == sql.ErrNoRows {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM automation_runs WHERE id=?`, runID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM automation_occurrences WHERE id=?`, occurrenceID); err != nil {
		return err
	}
	return tx.Commit()
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
