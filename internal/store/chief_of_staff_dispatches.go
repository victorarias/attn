package store

import (
	"database/sql"
	"encoding/json"
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
	cloned.StructuredReport = cloneDispatchReport(dispatch.StructuredReport)
	if dispatch.ConciseSummary != nil {
		cloned.ConciseSummary = protocol.Ptr(protocol.Deref(dispatch.ConciseSummary))
	}
	if dispatch.Actionable != nil {
		cloned.Actionable = protocol.Ptr(protocol.Deref(dispatch.Actionable))
	}
	return &cloned
}

func cloneDispatchReport(report *protocol.DispatchReport) *protocol.DispatchReport {
	if report == nil {
		return nil
	}
	data, err := json.Marshal(report)
	if err != nil {
		return nil
	}
	var cloned protocol.DispatchReport
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil
	}
	return &cloned
}

func encodeDispatchReport(report *protocol.DispatchReport) (string, error) {
	if report == nil {
		return "", nil
	}
	data, err := json.Marshal(report)
	if err != nil {
		return "", fmt.Errorf("encode structured dispatch report: %w", err)
	}
	return string(data), nil
}

func decodeDispatchReport(data string) *protocol.DispatchReport {
	data = strings.TrimSpace(data)
	if data == "" {
		return nil
	}
	var report protocol.DispatchReport
	if err := json.Unmarshal([]byte(data), &report); err != nil {
		return nil
	}
	return &report
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

	structuredReportJSON, err := encodeDispatchReport(dispatch.StructuredReport)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO chief_of_staff_dispatches (
			id, chief_session_id, session_id, workspace_id, brief, label, agent,
			directory, branch, latest_report, structured_report_json, reported_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
		structuredReportJSON,
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
			directory, branch, latest_report, structured_report_json, reported_at,
			created_at, updated_at
		FROM chief_of_staff_dispatches
		WHERE session_id = ?`,
		sessionID,
	)
	return scanChiefOfStaffDispatch(row)
}

func (s *Store) GetChiefOfStaffDispatch(id string) *protocol.ChiefOfStaffDispatch {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	if s.db == nil {
		return cloneChiefOfStaffDispatch(s.chiefDispatches[id])
	}
	row := s.db.QueryRow(`
		SELECT id, chief_session_id, session_id, workspace_id, brief, label, agent,
			directory, branch, latest_report, structured_report_json, reported_at,
			created_at, updated_at
		FROM chief_of_staff_dispatches
		WHERE id = ?`,
		id,
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
			directory, branch, latest_report, structured_report_json, reported_at,
			created_at, updated_at
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
	return s.UpdateChiefOfStaffDispatchReportEnvelope(sessionID, report, nil)
}

func (s *Store) UpdateChiefOfStaffDispatchReportEnvelope(
	sessionID, report string,
	structuredReport *protocol.DispatchReport,
) (*protocol.ChiefOfStaffDispatch, error) {
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
	structuredReport = cloneDispatchReport(structuredReport)
	if structuredReport != nil {
		structuredReport.ReportedAt = now
		artifactIdentity := ""
		if structuredReport.Artifact != nil {
			artifactIdentity = strings.TrimSpace(structuredReport.Artifact.Identity)
		}
		for i := range structuredReport.Verification {
			current := artifactIdentity != "" &&
				strings.TrimSpace(structuredReport.Verification[i].ArtifactIdentity) == artifactIdentity
			structuredReport.Verification[i].Current = protocol.Ptr(current)
		}
	}
	structuredReportJSON, err := encodeDispatchReport(structuredReport)
	if err != nil {
		return nil, err
	}
	if s.db == nil {
		for id, dispatch := range s.chiefDispatches {
			if dispatch.SessionID != sessionID {
				continue
			}
			updated := cloneChiefOfStaffDispatch(dispatch)
			updated.LatestReport = protocol.Ptr(report)
			updated.StructuredReport = structuredReport
			updated.ReportedAt = protocol.Ptr(now)
			updated.UpdatedAt = now
			s.chiefDispatches[id] = updated
			return cloneChiefOfStaffDispatch(updated), nil
		}
		return nil, fmt.Errorf("session %s is not a tracked dispatch", sessionID)
	}

	result, err := s.db.Exec(`
		UPDATE chief_of_staff_dispatches
		SET latest_report = ?, structured_report_json = ?, reported_at = ?, updated_at = ?
		WHERE session_id = ?`,
		report,
		structuredReportJSON,
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
			directory, branch, latest_report, structured_report_json, reported_at,
			created_at, updated_at
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

func (s *Store) ResolveChiefOfStaffDispatchRequest(
	dispatchID, chiefSessionID, response, resolutionLink string,
) (*protocol.ChiefOfStaffDispatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dispatchID = strings.TrimSpace(dispatchID)
	chiefSessionID = strings.TrimSpace(chiefSessionID)
	response = strings.TrimSpace(response)
	resolutionLink = strings.TrimSpace(resolutionLink)
	if dispatchID == "" {
		return nil, fmt.Errorf("dispatch id cannot be empty")
	}
	if chiefSessionID == "" {
		return nil, fmt.Errorf("chief session id cannot be empty")
	}
	if response == "" {
		return nil, fmt.Errorf("response cannot be empty")
	}

	var dispatch *protocol.ChiefOfStaffDispatch
	if s.db == nil {
		dispatch = cloneChiefOfStaffDispatch(s.chiefDispatches[dispatchID])
	} else {
		row := s.db.QueryRow(`
			SELECT id, chief_session_id, session_id, workspace_id, brief, label, agent,
				directory, branch, latest_report, structured_report_json, reported_at,
				created_at, updated_at
			FROM chief_of_staff_dispatches
			WHERE id = ?`,
			dispatchID,
		)
		dispatch = scanChiefOfStaffDispatch(row)
	}
	if dispatch == nil {
		return nil, fmt.Errorf("dispatch %s not found", dispatchID)
	}
	if dispatch.ChiefSessionID != chiefSessionID {
		return nil, fmt.Errorf("dispatch %s is not owned by chief session %s", dispatchID, chiefSessionID)
	}
	if dispatch.StructuredReport == nil || dispatch.StructuredReport.Request == nil {
		return nil, fmt.Errorf("dispatch %s has no active decision request", dispatchID)
	}
	if dispatch.StructuredReport.Request.Status != protocol.DispatchRequestStatusPending {
		return nil, fmt.Errorf("dispatch %s decision request is already resolved", dispatchID)
	}

	now := string(protocol.TimestampNow())
	dispatch.StructuredReport.Request.Status = protocol.DispatchRequestStatusResolved
	dispatch.StructuredReport.Request.Response = protocol.Ptr(response)
	dispatch.StructuredReport.Request.RespondedBy = protocol.Ptr(chiefSessionID)
	dispatch.StructuredReport.Request.RespondedAt = protocol.Ptr(now)
	if resolutionLink != "" {
		dispatch.StructuredReport.Request.ResolutionLink = protocol.Ptr(resolutionLink)
	}
	dispatch.UpdatedAt = now

	if s.db == nil {
		s.chiefDispatches[dispatchID] = cloneChiefOfStaffDispatch(dispatch)
		return cloneChiefOfStaffDispatch(dispatch), nil
	}
	structuredReportJSON, err := encodeDispatchReport(dispatch.StructuredReport)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`
		UPDATE chief_of_staff_dispatches
		SET structured_report_json = ?, updated_at = ?
		WHERE id = ?`,
		structuredReportJSON,
		now,
		dispatchID,
	); err != nil {
		return nil, fmt.Errorf("resolve dispatch request: %w", err)
	}
	return dispatch, nil
}

type dispatchScanner interface {
	Scan(dest ...interface{}) error
}

func scanChiefOfStaffDispatch(scanner dispatchScanner) *protocol.ChiefOfStaffDispatch {
	var (
		dispatch                                               protocol.ChiefOfStaffDispatch
		branch, latestReport, structuredReportJSON, reportedAt sql.NullString
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
		&structuredReportJSON,
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
	if structuredReportJSON.Valid {
		dispatch.StructuredReport = decodeDispatchReport(structuredReportJSON.String)
	}
	if reportedAt.Valid && reportedAt.String != "" {
		dispatch.ReportedAt = protocol.Ptr(reportedAt.String)
	}
	return &dispatch
}
