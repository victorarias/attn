package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const workspaceContextFilename = "context.md"

type workspaceContextCheckoutMetadata struct {
	WorkspaceID   string `json:"workspace_id"`
	SessionID     string `json:"session_id"`
	Revision      int    `json:"revision"`
	CanonicalHash string `json:"canonical_hash"`
	CheckedOutAt  string `json:"checked_out_at"`
}

func contextContentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func workspaceContextCheckoutDir(dataRoot, sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return filepath.Join(dataRoot, "workspace-contexts", hex.EncodeToString(sum[:8]))
}

func workspaceContextCheckoutPaths(dataRoot, sessionID string) (string, string) {
	dir := workspaceContextCheckoutDir(dataRoot, sessionID)
	return filepath.Join(dir, workspaceContextFilename), filepath.Join(dir, "checkout.json")
}

func writeWorkspaceContextFile(path string, content []byte) error {
	tempPath, err := stageWorkspaceContextFile(path, content)
	if err != nil {
		return err
	}
	defer os.Remove(tempPath)
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace workspace context: %w", err)
	}
	return nil
}

func stageWorkspaceContextFile(path string, content []byte) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create workspace context directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".context-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temporary workspace context: %w", err)
	}
	tempPath := temp.Name()
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		os.Remove(tempPath)
		return "", fmt.Errorf("set workspace context permissions: %w", err)
	}
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		os.Remove(tempPath)
		return "", fmt.Errorf("write workspace context: %w", err)
	}
	if err := temp.Close(); err != nil {
		os.Remove(tempPath)
		return "", fmt.Errorf("close workspace context: %w", err)
	}
	return tempPath, nil
}

func writeWorkspaceContextMetadata(path string, metadata workspaceContextCheckoutMetadata) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workspace context checkout metadata: %w", err)
	}
	data = append(data, '\n')
	return writeWorkspaceContextFile(path, data)
}

func readWorkspaceContextMetadata(path string) (*workspaceContextCheckoutMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var metadata workspaceContextCheckoutMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("decode workspace context checkout metadata: %w", err)
	}
	return &metadata, nil
}

func (d *Daemon) resolveWorkspaceContextSource(sourceSessionID string) (*protocol.Session, error) {
	sourceSessionID = strings.TrimSpace(sourceSessionID)
	if sourceSessionID == "" {
		return nil, errors.New("source_session_id is required")
	}
	session := d.store.Get(sourceSessionID)
	if session == nil {
		return nil, fmt.Errorf("source session not found: %s", sourceSessionID)
	}
	if endpointID := strings.TrimSpace(protocol.Deref(session.EndpointID)); endpointID != "" {
		return nil, fmt.Errorf("workspace context for remote session %s on endpoint %s is not supported", sourceSessionID, endpointID)
	}
	if strings.TrimSpace(session.WorkspaceID) == "" || d.store.GetWorkspace(session.WorkspaceID) == nil {
		return nil, fmt.Errorf("source session has no local workspace")
	}
	return session, nil
}

func workspaceContextResult(session *protocol.Session, canonical *protocol.WorkspaceContext, path string, metadata *workspaceContextCheckoutMetadata, content []byte) *protocol.WorkspaceContextResult {
	modified := contextContentHash(content) != metadata.CanonicalHash
	result := &protocol.WorkspaceContextResult{
		WorkspaceID:       session.WorkspaceID,
		SessionID:         session.ID,
		Path:              path,
		Revision:          metadata.Revision,
		CanonicalRevision: canonical.Revision,
		Modified:          modified,
		Stale:             metadata.Revision != canonical.Revision,
	}
	if canonical.UpdatedBySessionID != "" {
		result.UpdatedBySessionID = protocol.Ptr(canonical.UpdatedBySessionID)
	}
	if canonical.UpdatedAt != "" {
		result.UpdatedAt = protocol.Ptr(canonical.UpdatedAt)
	}
	return result
}

func (d *Daemon) checkoutWorkspaceContext(msg *protocol.WorkspaceContextCheckoutMessage) (*protocol.WorkspaceContextResult, error) {
	session, err := d.resolveWorkspaceContextSource(msg.SourceSessionID)
	if err != nil {
		return nil, err
	}
	canonical, err := d.store.GetWorkspaceContext(session.WorkspaceID)
	if err != nil {
		return nil, err
	}
	d.workspaceContextCheckoutMu.Lock()
	defer d.workspaceContextCheckoutMu.Unlock()
	contextPath, metadataPath := workspaceContextCheckoutPaths(d.dataRoot, session.ID)
	localContent, contentErr := os.ReadFile(contextPath)
	metadata, metadataErr := readWorkspaceContextMetadata(metadataPath)
	if contentErr != nil && !os.IsNotExist(contentErr) {
		return nil, fmt.Errorf("read workspace context checkout: %w", contentErr)
	}
	if metadataErr != nil && !os.IsNotExist(metadataErr) && !protocol.Deref(msg.Force) {
		return nil, fmt.Errorf("workspace context checkout metadata is invalid; local file preserved; use `show --force` to replace it: %w", metadataErr)
	}
	validCheckout := contentErr == nil &&
		metadataErr == nil &&
		metadata.SessionID == session.ID &&
		metadata.WorkspaceID == session.WorkspaceID
	if !protocol.Deref(msg.Force) &&
		(contentErr == nil || metadataErr == nil) &&
		!validCheckout {
		return nil, errors.New("workspace context checkout is incomplete or belongs to another session; local files preserved; use `show --force` to replace them")
	}

	if validCheckout && !protocol.Deref(msg.Force) {
		modified := contextContentHash(localContent) != metadata.CanonicalHash
		if modified {
			return workspaceContextResult(session, canonical, contextPath, metadata, localContent), nil
		}
		if metadata.Revision == canonical.Revision {
			return workspaceContextResult(session, canonical, contextPath, metadata, localContent), nil
		}
	}

	content := []byte(canonical.Content)
	metadata = &workspaceContextCheckoutMetadata{
		WorkspaceID:   session.WorkspaceID,
		SessionID:     session.ID,
		Revision:      canonical.Revision,
		CanonicalHash: contextContentHash(content),
		CheckedOutAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeWorkspaceContextFile(contextPath, content); err != nil {
		return nil, err
	}
	if err := writeWorkspaceContextMetadata(metadataPath, *metadata); err != nil {
		return nil, err
	}
	return workspaceContextResult(session, canonical, contextPath, metadata, content), nil
}

func (d *Daemon) workspaceContextStatus(msg *protocol.WorkspaceContextStatusMessage) (*protocol.WorkspaceContextResult, error) {
	session, err := d.resolveWorkspaceContextSource(msg.SourceSessionID)
	if err != nil {
		return nil, err
	}
	canonical, err := d.store.GetWorkspaceContext(session.WorkspaceID)
	if err != nil {
		return nil, err
	}
	d.workspaceContextCheckoutMu.Lock()
	defer d.workspaceContextCheckoutMu.Unlock()
	contextPath, metadataPath := workspaceContextCheckoutPaths(d.dataRoot, session.ID)
	content, err := os.ReadFile(contextPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("workspace context is not checked out; run `attn workspace context show`")
		}
		return nil, fmt.Errorf("read workspace context checkout: %w", err)
	}
	metadata, err := readWorkspaceContextMetadata(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("read workspace context checkout metadata: %w", err)
	}
	if metadata.SessionID != session.ID || metadata.WorkspaceID != session.WorkspaceID {
		return nil, errors.New("workspace context checkout does not match the source session")
	}
	return workspaceContextResult(session, canonical, contextPath, metadata, content), nil
}

func (d *Daemon) updateWorkspaceContext(msg *protocol.WorkspaceContextUpdateMessage) (*protocol.WorkspaceContextResult, bool, error) {
	session, err := d.resolveWorkspaceContextSource(msg.SourceSessionID)
	if err != nil {
		return nil, false, err
	}
	d.workspaceContextCheckoutMu.Lock()
	defer d.workspaceContextCheckoutMu.Unlock()
	contextPath, metadataPath := workspaceContextCheckoutPaths(d.dataRoot, session.ID)
	content, err := os.ReadFile(contextPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, errors.New("workspace context is not checked out; run `attn workspace context show`")
		}
		return nil, false, fmt.Errorf("read workspace context checkout: %w", err)
	}
	metadata, err := readWorkspaceContextMetadata(metadataPath)
	if err != nil {
		return nil, false, fmt.Errorf("read workspace context checkout metadata: %w", err)
	}
	if metadata.SessionID != session.ID || metadata.WorkspaceID != session.WorkspaceID {
		return nil, false, errors.New("workspace context checkout does not match the source session")
	}
	canonical, changed, err := d.store.UpdateWorkspaceContext(
		session.WorkspaceID,
		string(content),
		session.ID,
		metadata.Revision,
	)
	if err != nil {
		if errors.Is(err, store.ErrWorkspaceContextConflict) {
			return nil, false, fmt.Errorf("%w; run `attn workspace context status`, then reconcile or use `show --force` to discard local edits", err)
		}
		return nil, false, err
	}
	metadata.Revision = canonical.Revision
	metadata.CanonicalHash = contextContentHash(content)
	metadata.CheckedOutAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := writeWorkspaceContextMetadata(metadataPath, *metadata); err != nil {
		return nil, false, err
	}
	result := workspaceContextResult(session, canonical, contextPath, metadata, content)
	if changed {
		d.broadcastWorkspaceContextChanged(canonical)
		d.enqueueWorkspaceContextCompaction(canonical)
		// A content-changing context write is a daily-narrate activity event: it marks
		// the workspace active so the nightly daily-narrate cron narrates it even on a
		// day with no session end. A no-op update (changed == false) does NOT mark
		// activity — there is nothing new for the daily backstop to narrate. The
		// janitor's compaction write is NOT an activity signal (it only reshapes
		// existing content), so this is hooked at the agent/user write chokepoint, not
		// the janitor apply path.
		d.markNotebookWorkspaceActivity(session.WorkspaceID)
	}
	return result, changed, nil
}

func (d *Daemon) handleWorkspaceContextCheckout(conn net.Conn, msg *protocol.WorkspaceContextCheckoutMessage) {
	result, err := d.checkoutWorkspaceContext(msg)
	d.sendWorkspaceContextResponse(conn, result, err)
}

func (d *Daemon) handleWorkspaceContextUpdate(conn net.Conn, msg *protocol.WorkspaceContextUpdateMessage) {
	result, _, err := d.updateWorkspaceContext(msg)
	d.sendWorkspaceContextResponse(conn, result, err)
}

func (d *Daemon) handleWorkspaceContextStatus(conn net.Conn, msg *protocol.WorkspaceContextStatusMessage) {
	result, err := d.workspaceContextStatus(msg)
	d.sendWorkspaceContextResponse(conn, result, err)
}

func (d *Daemon) handleWorkspaceContextList(conn net.Conn) {
	contexts, err := d.store.ListWorkspaceContexts()
	if err != nil {
		d.sendError(conn, "workspace context: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                true,
		WorkspaceContexts: contexts,
	})
}

func (d *Daemon) handleWorkspaceContextCompact(conn net.Conn, msg *protocol.WorkspaceContextCompactMessage) {
	result, err := d.compactWorkspaceContextForSession(context.Background(), msg.SourceSessionID)
	d.sendWorkspaceContextMaintenanceResponse(conn, result, err)
}

func (d *Daemon) handleWorkspaceContextRollback(conn net.Conn, msg *protocol.WorkspaceContextRollbackMessage) {
	result, err := d.rollbackWorkspaceContextForSession(msg.SourceSessionID)
	d.sendWorkspaceContextMaintenanceResponse(conn, result, err)
}

func (d *Daemon) sendWorkspaceContextResponse(conn net.Conn, result *protocol.WorkspaceContextResult, err error) {
	if err != nil {
		d.sendError(conn, "workspace context: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                     true,
		WorkspaceContextResult: result,
	})
}

func (d *Daemon) sendWorkspaceContextMaintenanceResponse(
	conn net.Conn,
	result *protocol.WorkspaceContextMaintenanceResult,
	err error,
) {
	if err != nil {
		d.sendError(conn, "workspace context: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                                true,
		WorkspaceContextMaintenanceResult: result,
	})
}

func (d *Daemon) sendWorkspaceContextWSResult(client *wsClient, action string, result *protocol.WorkspaceContextResult, err error) {
	response := protocol.WorkspaceContextResultMessage{
		Event:   protocol.EventWorkspaceContextResult,
		Action:  action,
		Success: err == nil,
		Result:  result,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, response)
}

func (d *Daemon) sendWorkspaceContextListWSResult(client *wsClient, requestID string) {
	contexts, err := d.store.ListWorkspaceContexts()
	response := protocol.WorkspaceContextListResultMessage{
		Event:     protocol.EventWorkspaceContextListResult,
		RequestID: requestID,
		Success:   err == nil,
		Contexts:  contexts,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, response)
}
