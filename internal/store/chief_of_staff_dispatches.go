package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func cloneChiefOfStaffDispatch(dispatch *protocol.ChiefOfStaffDispatch) *protocol.ChiefOfStaffDispatch {
	if dispatch == nil {
		return nil
	}
	cloned := *dispatch
	if dispatch.Branch != nil {
		cloned.Branch = protocol.Ptr(protocol.Deref(dispatch.Branch))
	}
	if dispatch.LatestReport != nil {
		cloned.LatestReport = protocol.Ptr(protocol.Deref(dispatch.LatestReport))
	}
	if dispatch.ReportedAt != nil {
		cloned.ReportedAt = protocol.Ptr(protocol.Deref(dispatch.ReportedAt))
	}
	return &cloned
}

func (s *Store) AddChiefOfStaffDispatch(dispatch *protocol.ChiefOfStaffDispatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if dispatch == nil || strings.TrimSpace(dispatch.ID) == "" {
		return fmt.Errorf("dispatch id cannot be empty")
	}
	if strings.TrimSpace(dispatch.ChiefSessionID) == "" {
		return fmt.Errorf("chief session id cannot be empty")
	}
	if strings.TrimSpace(dispatch.SessionID) == "" {
		return fmt.Errorf("target session id cannot be empty")
	}
	if s.db == nil {
		if s.chiefDispatches == nil {
			s.chiefDispatches = make(map[string]*protocol.ChiefOfStaffDispatch)
		}
		s.chiefDispatches[dispatch.ID] = cloneChiefOfStaffDispatch(dispatch)
		return nil
	}

	_, err := s.db.Exec(`
		INSERT INTO chief_of_staff_dispatches (
			id, chief_session_id, session_id, workspace_id, brief, label, agent,
			directory, branch, latest_report, reported_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		dispatch.ID,
		dispatch.ChiefSessionID,
		dispatch.SessionID,
		dispatch.WorkspaceID,
		dispatch.Brief,
		dispatch.Label,
		dispatch.Agent,
		dispatch.Directory,
		protocol.Deref(dispatch.Branch),
		protocol.Deref(dispatch.LatestReport),
		protocol.Deref(dispatch.ReportedAt),
		dispatch.CreatedAt,
		dispatch.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert chief of staff dispatch %s: %w", dispatch.ID, err)
	}
	return nil
}

func (s *Store) DeleteChiefOfStaffDispatch(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	if s.db == nil {
		delete(s.chiefDispatches, id)
		return nil
	}
	_, err := s.db.Exec("DELETE FROM chief_of_staff_dispatches WHERE id = ?", id)
	return err
}

func (s *Store) GetChiefOfStaffDispatchBySession(sessionID string) *protocol.ChiefOfStaffDispatch {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	if s.db == nil {
		for _, dispatch := range s.chiefDispatches {
			if dispatch.SessionID == sessionID {
				return cloneChiefOfStaffDispatch(dispatch)
			}
		}
		return nil
	}

	row := s.db.QueryRow(`
		SELECT id, chief_session_id, session_id, workspace_id, brief, label, agent,
			directory, branch, latest_report, reported_at, created_at, updated_at
		FROM chief_of_staff_dispatches
		WHERE session_id = ?`,
		sessionID,
	)
	return scanChiefOfStaffDispatch(row)
}

func (s *Store) ListChiefOfStaffDispatches(chiefSessionID string) []*protocol.ChiefOfStaffDispatch {
	s.mu.RLock()
	defer s.mu.RUnlock()

	chiefSessionID = strings.TrimSpace(chiefSessionID)
	if s.db == nil {
		result := make([]*protocol.ChiefOfStaffDispatch, 0, len(s.chiefDispatches))
		for _, dispatch := range s.chiefDispatches {
			if chiefSessionID != "" && dispatch.ChiefSessionID != chiefSessionID {
				continue
			}
			result = append(result, cloneChiefOfStaffDispatch(dispatch))
		}
		sort.Slice(result, func(i, j int) bool {
			if result[i].CreatedAt == result[j].CreatedAt {
				return result[i].ID > result[j].ID
			}
			return result[i].CreatedAt > result[j].CreatedAt
		})
		return result
	}

	query := `
		SELECT id, chief_session_id, session_id, workspace_id, brief, label, agent,
			directory, branch, latest_report, reported_at, created_at, updated_at
		FROM chief_of_staff_dispatches`
	var (
		rows *sql.Rows
		err  error
	)
	if chiefSessionID == "" {
		rows, err = s.db.Query(query + " ORDER BY created_at DESC, id DESC")
	} else {
		rows, err = s.db.Query(query+" WHERE chief_session_id = ? ORDER BY created_at DESC, id DESC", chiefSessionID)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []*protocol.ChiefOfStaffDispatch
	for rows.Next() {
		if dispatch := scanChiefOfStaffDispatch(rows); dispatch != nil {
			result = append(result, dispatch)
		}
	}
	return result
}

func (s *Store) UpdateChiefOfStaffDispatchReport(sessionID, report string) (*protocol.ChiefOfStaffDispatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessionID = strings.TrimSpace(sessionID)
	report = strings.TrimSpace(report)
	if sessionID == "" {
		return nil, fmt.Errorf("source session id cannot be empty")
	}
	if report == "" {
		return nil, fmt.Errorf("report cannot be empty")
	}
	now := string(protocol.TimestampNow())
	if s.db == nil {
		for id, dispatch := range s.chiefDispatches {
			if dispatch.SessionID != sessionID {
				continue
			}
			updated := cloneChiefOfStaffDispatch(dispatch)
			updated.LatestReport = protocol.Ptr(report)
			updated.ReportedAt = protocol.Ptr(now)
			updated.UpdatedAt = now
			s.chiefDispatches[id] = updated
			return cloneChiefOfStaffDispatch(updated), nil
		}
		return nil, fmt.Errorf("session %s is not a tracked dispatch", sessionID)
	}

	result, err := s.db.Exec(`
		UPDATE chief_of_staff_dispatches
		SET latest_report = ?, reported_at = ?, updated_at = ?
		WHERE session_id = ?`,
		report,
		now,
		now,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("update dispatch report: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("read dispatch report update result: %w", err)
	}
	if rows == 0 {
		return nil, fmt.Errorf("session %s is not a tracked dispatch", sessionID)
	}

	row := s.db.QueryRow(`
		SELECT id, chief_session_id, session_id, workspace_id, brief, label, agent,
			directory, branch, latest_report, reported_at, created_at, updated_at
		FROM chief_of_staff_dispatches
		WHERE session_id = ?`,
		sessionID,
	)
	dispatch := scanChiefOfStaffDispatch(row)
	if dispatch == nil {
		return nil, fmt.Errorf("updated dispatch for session %s could not be read", sessionID)
	}
	return dispatch, nil
}

type dispatchScanner interface {
	Scan(dest ...interface{}) error
}

func scanChiefOfStaffDispatch(scanner dispatchScanner) *protocol.ChiefOfStaffDispatch {
	var (
		dispatch                         protocol.ChiefOfStaffDispatch
		branch, latestReport, reportedAt sql.NullString
	)
	if err := scanner.Scan(
		&dispatch.ID,
		&dispatch.ChiefSessionID,
		&dispatch.SessionID,
		&dispatch.WorkspaceID,
		&dispatch.Brief,
		&dispatch.Label,
		&dispatch.Agent,
		&dispatch.Directory,
		&branch,
		&latestReport,
		&reportedAt,
		&dispatch.CreatedAt,
		&dispatch.UpdatedAt,
	); err != nil {
		return nil
	}
	if branch.Valid && branch.String != "" {
		dispatch.Branch = protocol.Ptr(branch.String)
	}
	if latestReport.Valid && latestReport.String != "" {
		dispatch.LatestReport = protocol.Ptr(latestReport.String)
	}
	if reportedAt.Valid && reportedAt.String != "" {
		dispatch.ReportedAt = protocol.Ptr(reportedAt.String)
	}
	return &dispatch
}
