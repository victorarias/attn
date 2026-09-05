package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func DefaultSocketPath() string {
	return config.SocketPath()
}

type Client struct {
	socketPath string
}

type automationResult struct {
	Success bool    `json:"success"`
	Error   *string `json:"error,omitempty"`
}

func (c *Client) sendAutomation(msg any, out any) error {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return explainConnectError(c.socketPath, err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		return err
	}
	var raw json.RawMessage
	if err := json.NewDecoder(conn).Decode(&raw); err != nil {
		return err
	}
	var probe automationResult
	if err := json.Unmarshal(raw, &probe); err != nil {
		return err
	}
	if !probe.Success {
		return fmt.Errorf("daemon error: %s", protocol.Deref(probe.Error))
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) AutomationApply(raw string) (*protocol.AutomationApplyResultMessage, error) {
	var result protocol.AutomationApplyResultMessage
	if err := c.sendAutomation(protocol.AutomationApplyMessage{Cmd: protocol.CmdAutomationApply, DefinitionYaml: raw}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// AutomationValidate runs the same seam apply persists through, without
// persisting. A non-nil error IS the validation message, not a transport error.
func (c *Client) AutomationValidate(raw string) error {
	var result protocol.AutomationValidateResultMessage
	return c.sendAutomation(protocol.AutomationValidateMessage{Cmd: protocol.CmdAutomationValidate, DefinitionYaml: raw}, &result)
}

func (c *Client) AutomationDefinitions() (*protocol.AutomationDefinitionsResultMessage, error) {
	var result protocol.AutomationDefinitionsResultMessage
	if err := c.sendAutomation(protocol.AutomationDefinitionsGetMessage{Cmd: protocol.CmdAutomationDefinitionsGet}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AutomationDefinition(id string) (*protocol.AutomationDefinitionResultMessage, error) {
	var result protocol.AutomationDefinitionResultMessage
	if err := c.sendAutomation(protocol.AutomationDefinitionGetMessage{Cmd: protocol.CmdAutomationDefinitionGet, DefinitionID: id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AutomationRun(id, requestID, input string) (*protocol.AutomationRunResultMessage, error) {
	var result protocol.AutomationRunResultMessage
	if err := c.sendAutomation(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: id, RequestID: requestID, InputJson: protocol.Ptr(input)}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AutomationRunPullRequest(id, requestID, prURL string) (*protocol.AutomationRunResultMessage, error) {
	var result protocol.AutomationRunResultMessage
	if err := c.sendAutomation(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: id, RequestID: requestID, PRURL: protocol.Ptr(prURL)}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AutomationRuns(id string) (*protocol.AutomationRunsResultMessage, error) {
	var result protocol.AutomationRunsResultMessage
	if err := c.sendAutomation(protocol.AutomationRunsGetMessage{Cmd: protocol.CmdAutomationRunsGet, DefinitionID: id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AutomationSetEnabled(id string, enabled bool) (*protocol.AutomationSetEnabledResultMessage, error) {
	var result protocol.AutomationSetEnabledResultMessage
	if err := c.sendAutomation(protocol.AutomationSetEnabledMessage{Cmd: protocol.CmdAutomationSetEnabled, DefinitionID: id, Enabled: enabled}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AutomationDelete(id string) error {
	var result protocol.AutomationDeleteResultMessage
	return c.sendAutomation(protocol.AutomationDeleteMessage{Cmd: protocol.CmdAutomationDelete, DefinitionID: id}, &result)
}

func (c *Client) AutomationCleanup(id string) (*protocol.AutomationCleanupResultMessage, error) {
	var result protocol.AutomationCleanupResultMessage
	if err := c.sendAutomation(protocol.AutomationCleanupMessage{Cmd: protocol.CmdAutomationCleanup, DefinitionID: id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type ListResult struct {
	Sessions   []protocol.Session   `json:"sessions"`
	Workspaces []protocol.Workspace `json:"workspaces"`
}

func New(socketPath string) *Client {
	if socketPath == "" {
		socketPath = DefaultSocketPath()
	}
	return &Client{socketPath: socketPath}
}

func (c *Client) send(msg interface{}) (*protocol.Response, error) {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return nil, explainConnectError(c.socketPath, err)
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		return nil, fmt.Errorf("send message: %w", err)
	}

	var resp protocol.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("receive response: %w", err)
	}

	if !resp.Ok {
		return nil, &DaemonError{
			Code:     protocol.Deref(resp.ErrorCode),
			Message:  protocol.Deref(resp.Error),
			Conflict: resp.ErrorConflict,
		}
	}

	return &resp, nil
}

type DaemonError struct {
	Code     string
	Message  string
	Conflict *protocol.DocumentConflict
}

func (e *DaemonError) Error() string { return fmt.Sprintf("daemon error: %s", e.Message) }

func ErrorCode(err error) string {
	var daemonErr *DaemonError
	if errors.As(err, &daemonErr) {
		return daemonErr.Code
	}
	return ""
}

func (c *Client) Register(id, label, dir string) error {
	return c.RegisterWithAgent(id, label, dir, "")
}

func (c *Client) RegisterWithAgent(id, label, dir, agent string) error {
	return c.RegisterAsMember(id, label, dir, agent, "")
}

// RegisterAsMember refuses the whole registration when the member cannot be bound;
// callers must treat that as launch-fatal, or an identity is silently dropped.
func (c *Client) RegisterAsMember(id, label, dir, agent, member string) error {
	msg := protocol.RegisterMessage{
		Cmd:         protocol.CmdRegister,
		ID:          id,
		Label:       protocol.Ptr(label),
		Dir:         dir,
		WorkspaceID: "workspace-" + id,
	}
	if agent != "" {
		normalized := protocol.NormalizeSessionAgentString(agent, string(protocol.SessionAgentCodex))
		msg.Agent = protocol.Ptr(normalized)
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) Unregister(id string) error {
	msg := protocol.UnregisterMessage{
		Cmd: protocol.CmdUnregister,
		ID:  id,
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) UpdateState(id, state string) error {
	return c.UpdateStateFromHook(id, state, "")
}

func (c *Client) UpdateStateFromHook(id, state, permissionMode string) error {
	return c.UpdateStateFromHookEvidence(id, state, permissionMode, "", "")
}

func (c *Client) UpdateStateFromHookEvidence(id, state, permissionMode, hookEvent, prompt string) error {
	msg := protocol.StateMessage{
		Cmd:   protocol.CmdState,
		ID:    id,
		State: state,
	}
	if strings.TrimSpace(permissionMode) != "" {
		msg.PermissionMode = protocol.Ptr(permissionMode)
	}
	if strings.TrimSpace(hookEvent) != "" {
		msg.HookEvent = protocol.Ptr(strings.TrimSpace(hookEvent))
	}
	if strings.TrimSpace(prompt) != "" {
		msg.Prompt = protocol.Ptr(prompt)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) RecordNotification(id, notificationType, message string) error {
	msg := protocol.HookNotificationMessage{
		Cmd:              protocol.CmdHookNotification,
		ID:               id,
		NotificationType: notificationType,
	}
	if strings.TrimSpace(message) != "" {
		msg.Message = protocol.Ptr(message)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) RecordStopFailure(id, errorType, message string) error {
	msg := protocol.HookStopFailureMessage{
		Cmd:       protocol.CmdHookStopFailure,
		ID:        id,
		ErrorType: errorType,
	}
	if strings.TrimSpace(message) != "" {
		msg.ErrorMessage = protocol.Ptr(message)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) RecordCompaction(id string, active bool, trigger string) error {
	msg := protocol.HookCompactionMessage{
		Cmd:    protocol.CmdHookCompaction,
		ID:     id,
		Active: active,
	}
	if strings.TrimSpace(trigger) != "" {
		msg.Trigger = protocol.Ptr(trigger)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) ObserveAgentConversation(id, nativeID, transcriptPath string) error {
	msg := protocol.SetSessionResumeIDMessage{
		Cmd:             protocol.CmdSetSessionResumeID,
		ID:              id,
		ResumeSessionID: nativeID,
	}
	if strings.TrimSpace(transcriptPath) != "" {
		msg.TranscriptPath = protocol.Ptr(transcriptPath)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) SessionInstructions(targetSessionID, question string) (*protocol.SessionInstructionsResult, error) {
	resp, err := c.send(protocol.SessionInstructionsMessage{
		Cmd:             protocol.CmdSessionInstructions,
		TargetSessionID: targetSessionID,
		Question:        question,
	})
	if err != nil {
		return nil, err
	}
	if resp.SessionInstructionsResult == nil {
		return nil, errors.New("daemon returned no session instructions result")
	}
	return resp.SessionInstructionsResult, nil
}

type SessionListOptions struct {
	Closed bool
	All    bool
	Limit  int
	Before string
}

func (c *Client) SessionList(opts SessionListOptions) (*protocol.SessionListResult, error) {
	msg := protocol.SessionListMessage{Cmd: protocol.CmdSessionList}
	if opts.Closed {
		msg.Closed = protocol.Ptr(true)
	}
	if opts.All {
		msg.All = protocol.Ptr(true)
	}
	if opts.Limit > 0 {
		msg.Limit = protocol.Ptr(opts.Limit)
	}
	if before := strings.TrimSpace(opts.Before); before != "" {
		msg.Before = protocol.Ptr(before)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SessionListResult == nil {
		return nil, errors.New("daemon returned no session list result")
	}
	return resp.SessionListResult, nil
}

func (c *Client) SessionShow(sessionID string) (*protocol.SessionShowResult, error) {
	resp, err := c.send(protocol.SessionShowMessage{
		Cmd:       protocol.CmdSessionShow,
		SessionID: strings.TrimSpace(sessionID),
	})
	if err != nil {
		return nil, err
	}
	if resp.SessionShowResult == nil {
		return nil, errors.New("daemon returned no session show result")
	}
	return resp.SessionShowResult, nil
}

func (c *Client) SessionTranscript(targetSessionID, afterCursor string) (*protocol.SessionTranscriptResult, error) {
	msg := protocol.SessionTranscriptMessage{
		Cmd:             protocol.CmdSessionTranscript,
		TargetSessionID: targetSessionID,
	}
	if strings.TrimSpace(afterCursor) != "" {
		msg.AfterCursor = protocol.Ptr(strings.TrimSpace(afterCursor))
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SessionTranscriptResult == nil {
		return nil, errors.New("daemon returned no session transcript result")
	}
	return resp.SessionTranscriptResult, nil
}

func (c *Client) StateExplain(targetSessionID string) (*protocol.StateExplainResult, error) {
	resp, err := c.send(protocol.StateExplainMessage{
		Cmd:             protocol.CmdStateExplain,
		TargetSessionID: targetSessionID,
	})
	if err != nil {
		return nil, err
	}
	if resp.StateExplainResult == nil {
		return nil, errors.New("daemon returned no state explain result")
	}
	return resp.StateExplainResult, nil
}

func (c *Client) AgentPeek(targetSessionID string) (*protocol.AgentPeekResult, error) {
	resp, err := c.send(protocol.AgentPeekMessage{
		Cmd:             protocol.CmdAgentPeek,
		TargetSessionID: targetSessionID,
	})
	if err != nil {
		return nil, err
	}
	if resp.AgentPeekResult == nil {
		return nil, errors.New("daemon returned no agent peek result")
	}
	return resp.AgentPeekResult, nil
}

func (c *Client) AgentMsg(target, sourceSessionID, content string) (*protocol.AgentMsgResult, error) {
	msg := protocol.AgentMsgMessage{
		Cmd:             protocol.CmdAgentMsg,
		TargetSessionID: target,
		SourceSessionID: sourceSessionID,
		Content:         content,
	}
	if garden.ValidateID(target) == nil {
		msg.TargetSessionID = ""
		msg.TargetSeedID = protocol.Ptr(target)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.AgentMsgResult == nil {
		return nil, errors.New("daemon returned no agent msg result")
	}
	return resp.AgentMsgResult, nil
}

func (c *Client) AgentClose(target, sourceSessionID, reason string) (*protocol.AgentCloseResult, error) {
	msg := protocol.AgentCloseMessage{
		Cmd:             protocol.CmdAgentClose,
		TargetSessionID: target,
		SourceSessionID: sourceSessionID,
		Reason:          reason,
	}
	if garden.ValidateID(target) == nil {
		msg.TargetSessionID = ""
		msg.TargetSeedID = protocol.Ptr(target)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.AgentCloseResult == nil {
		return nil, errors.New("daemon returned no agent close result")
	}
	return resp.AgentCloseResult, nil
}

func (c *Client) AgentInbox(messageID, recipientSessionID string) (*protocol.AgentPeerMessage, error) {
	resp, err := c.send(protocol.AgentInboxMessage{
		Cmd: protocol.CmdAgentInbox, MessageID: protocol.Ptr(messageID),
		RecipientSessionID: recipientSessionID,
	})
	if err != nil {
		return nil, err
	}
	if resp.AgentInboxResult == nil {
		return nil, errors.New("daemon returned no agent inbox result")
	}
	return resp.AgentInboxResult, nil
}

func (c *Client) AgentInboxBatch(recipientSessionID string, limit int) (*protocol.AgentInboxBatchResult, error) {
	resp, err := c.send(protocol.AgentInboxMessage{
		Cmd: protocol.CmdAgentInbox, RecipientSessionID: recipientSessionID,
		Limit: protocol.Ptr(limit),
	})
	if err != nil {
		return nil, err
	}
	if resp.AgentInboxBatchResult == nil {
		return nil, errors.New("daemon returned no agent inbox batch result")
	}
	return resp.AgentInboxBatchResult, nil
}

func (c *Client) AgentMsgStatus(messageID, senderSessionID string) (*protocol.AgentPeerMessage, error) {
	resp, err := c.send(protocol.AgentMsgStatusMessage{
		Cmd: protocol.CmdAgentMsgStatus, MessageID: messageID,
		SenderSessionID: senderSessionID,
	})
	if err != nil {
		return nil, err
	}
	if resp.AgentMsgStatusResult == nil {
		return nil, errors.New("daemon returned no agent msg status result")
	}
	return resp.AgentMsgStatusResult, nil
}

// StopFacts is what the Stop hook observed about whether the turn finished; the
// daemon decides what it means. The zero value reads as a terminal stop.
type StopFacts struct {
	BackgroundTasks     []protocol.StopBackgroundTask
	PendingSessionCrons int
}

func (c *Client) SendStop(id, transcriptPath string, facts StopFacts) error {
	msg := protocol.StopMessage{
		Cmd:                 protocol.CmdStop,
		ID:                  id,
		TranscriptPath:      transcriptPath,
		BackgroundTasks:     facts.BackgroundTasks,
		PendingSessionCrons: protocol.Ptr(facts.PendingSessionCrons),
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) UpdateTodos(id string, todos []string) error {
	msg := protocol.TodosMessage{
		Cmd:   protocol.CmdTodos,
		ID:    id,
		Todos: todos,
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) RecordFilesEdited(id string, paths []string) error {
	msg := protocol.FilesEditedMessage{
		Cmd:   protocol.CmdFilesEdited,
		ID:    id,
		Paths: paths,
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) RecordPullRequestCreated(id, url string) error {
	msg := protocol.PullRequestCreatedMessage{
		Cmd: protocol.CmdPullRequestCreated,
		ID:  id,
		URL: url,
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) ForgetSessionPullRequest(id, url string) error {
	msg := protocol.PullRequestForgetMessage{
		Cmd: protocol.CmdPullRequestForget,
		ID:  id,
		URL: url,
	}
	_, err := c.send(msg)
	return err
}

type DelegateOptions struct {
	RequestID          string
	TicketID           string
	Confirm            bool
	Agent              string
	Model              string
	Effort             string
	Label              string
	Yolo               bool
	Placement          string
	Plot               string
	WorkspaceID        string
	CWD                string
	WorktreeRepo       string
	Worktree           string
	WorktreePath       string
	StartingFrom       string
	NoWorktree         bool
	AllowWorktreeReuse bool
	Handover           *protocol.SeedHandoverRequest
}

func (c *Client) StartDelegation(sourceSessionID, brief string, opts DelegateOptions) (*protocol.DelegationOperation, error) {
	requestID := strings.TrimSpace(opts.RequestID)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	msg := protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		RequestID:       requestID,
		SourceSessionID: sourceSessionID,
		Brief:           strings.TrimSpace(brief),
		Handover:        opts.Handover,
	}
	if value := strings.TrimSpace(opts.TicketID); value != "" {
		msg.TicketID = protocol.Ptr(value)
	}
	if opts.Confirm {
		msg.Confirm = protocol.Ptr(true)
	}
	if value := strings.TrimSpace(opts.Agent); value != "" {
		msg.Agent = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(opts.Model); value != "" {
		msg.Model = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(opts.Effort); value != "" {
		msg.Effort = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(opts.Label); value != "" {
		msg.Label = protocol.Ptr(value)
	}
	if opts.Yolo {
		msg.YoloMode = protocol.Ptr(true)
	}
	if opts.AllowWorktreeReuse {
		msg.AllowWorktreeReuse = protocol.Ptr(true)
	}
	if value := strings.TrimSpace(opts.Placement); value != "" {
		msg.Placement = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(opts.Plot); value != "" {
		msg.Plot = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(opts.WorkspaceID); value != "" {
		msg.WorkspaceID = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(opts.CWD); value != "" {
		msg.Cwd = protocol.Ptr(value)
	}
	branch := strings.TrimSpace(opts.Worktree)
	worktreeRepo := strings.TrimSpace(opts.WorktreeRepo)
	worktreePath := strings.TrimSpace(opts.WorktreePath)
	startingFrom := strings.TrimSpace(opts.StartingFrom)
	worktreeConfigured := branch != "" || worktreeRepo != "" || worktreePath != "" || startingFrom != ""
	if opts.NoWorktree && worktreeConfigured {
		return nil, errors.New("no worktree cannot be combined with worktree options")
	}
	if !opts.NoWorktree && opts.Handover == nil {
		msg.Worktree = &protocol.DelegateWorktreeRequest{
			Branch: branch,
		}
		if worktreeRepo != "" {
			msg.Worktree.Repo = protocol.Ptr(worktreeRepo)
		}
		if worktreePath != "" {
			msg.Worktree.Path = protocol.Ptr(worktreePath)
		}
		if startingFrom != "" {
			msg.Worktree.StartingFrom = protocol.Ptr(startingFrom)
		}
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.DelegationOperation == nil {
		return nil, errors.New("daemon returned no delegation operation")
	}
	return resp.DelegationOperation, nil
}

func (c *Client) DelegationStatus(id string) (*protocol.DelegationOperation, error) {
	resp, err := c.send(protocol.DelegateStatusMessage{Cmd: protocol.CmdDelegateStatus, ID: strings.TrimSpace(id)})
	if err != nil {
		return nil, err
	}
	if resp.DelegationOperation == nil {
		return nil, errors.New("daemon returned no delegation operation")
	}
	return resp.DelegationOperation, nil
}

func (c *Client) Delegate(sourceSessionID, brief string, opts DelegateOptions) (*protocol.DelegateResult, error) {
	op, err := c.StartDelegation(sourceSessionID, brief, opts)
	if err != nil {
		return nil, err
	}
	for op.State == protocol.DelegationOperationStateAccepted || op.State == protocol.DelegationOperationStatePreparing {
		time.Sleep(100 * time.Millisecond)
		op, err = c.DelegationStatus(op.OperationID)
		if err != nil {
			return nil, err
		}
	}
	if op.State == protocol.DelegationOperationStateFailed {
		return nil, fmt.Errorf("delegation failed: %s", protocol.Deref(op.Error))
	}
	if op.Result == nil {
		return nil, errors.New("completed delegation has no result")
	}
	return op.Result, nil
}

func (c *Client) SetTicketStatus(sourceSessionID, workState, comment, ticketID string) (*protocol.TicketStatusResult, error) {
	msg := protocol.SetTicketStatusMessage{
		Cmd:             protocol.CmdSetTicketStatus,
		SourceSessionID: sourceSessionID,
		WorkState:       protocol.DispatchWorkState(workState),
	}
	if value := strings.TrimSpace(comment); value != "" {
		msg.Comment = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(ticketID); value != "" {
		msg.TicketID = protocol.Ptr(value)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.TicketStatusResult == nil {
		return nil, errors.New("daemon returned no ticket status result")
	}
	return resp.TicketStatusResult, nil
}

func (c *Client) AttachTicket(sourceSessionID string, files []protocol.TicketAttachFile, ticketID, state, comment string) (*protocol.TicketAttachResult, error) {
	msg := protocol.TicketAttachMessage{Cmd: protocol.CmdTicketAttach, SourceSessionID: sourceSessionID, Files: files}
	if value := strings.TrimSpace(ticketID); value != "" {
		msg.TicketID = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(state); value != "" {
		workState := protocol.DispatchWorkState(value)
		msg.State = &workState
	}
	if value := strings.TrimSpace(comment); value != "" {
		msg.Comment = protocol.Ptr(value)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.TicketAttachResult == nil {
		return nil, errors.New("daemon returned no ticket attach result")
	}
	return resp.TicketAttachResult, nil
}

func (c *Client) CreateTicket(sourceSessionID, title, description, id string) (*protocol.TicketCreateResult, error) {
	msg := protocol.TicketCreateMessage{
		Cmd:             protocol.CmdTicketCreate,
		SourceSessionID: sourceSessionID,
		Title:           title,
	}
	if value := strings.TrimSpace(description); value != "" {
		msg.Description = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(id); value != "" {
		msg.ID = protocol.Ptr(value)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.TicketCreateResult == nil {
		return nil, errors.New("daemon returned no ticket create result")
	}
	return resp.TicketCreateResult, nil
}

func (c *Client) CommentTicket(sourceSessionID, ticketID, comment string) (*protocol.TicketCommentResult, error) {
	resp, err := c.send(protocol.TicketCommentMessage{
		Cmd:             protocol.CmdTicketComment,
		SourceSessionID: sourceSessionID,
		TicketID:        ticketID,
		Comment:         comment,
	})
	if err != nil {
		return nil, err
	}
	if resp.TicketCommentResult == nil {
		return nil, errors.New("daemon returned no ticket comment result")
	}
	return resp.TicketCommentResult, nil
}

func (c *Client) PresentOpen(sourceSessionID, manifestYAML, presentationID string) (*protocol.PresentOpenResult, error) {
	msg := protocol.PresentOpenMessage{
		Cmd:             protocol.CmdPresentOpen,
		SourceSessionID: sourceSessionID,
		ManifestYaml:    manifestYAML,
	}
	if value := strings.TrimSpace(presentationID); value != "" {
		msg.PresentationID = protocol.Ptr(value)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.PresentOpenResult == nil {
		return nil, errors.New("daemon returned no present open result")
	}
	return resp.PresentOpenResult, nil
}

func (c *Client) PresentFeedback(presentationID string, seq int) (*protocol.PresentFeedbackResult, error) {
	msg := protocol.PresentFeedbackMessage{
		Cmd:            protocol.CmdPresentFeedback,
		PresentationID: presentationID,
	}
	if seq > 0 {
		msg.Seq = protocol.Ptr(seq)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.PresentFeedbackResult == nil {
		return nil, errors.New("daemon returned no present feedback result")
	}
	return resp.PresentFeedbackResult, nil
}

// TicketInbox CONSUMES unread events: reading advances the per-ticket cursors, so a
// second call returns only what landed since.
func (c *Client) TicketInbox(sourceSessionID string) (*protocol.TicketInboxResult, error) {
	return c.ticketInbox(sourceSessionID, protocol.TicketInboxModeExplicit, 0)
}

func (c *Client) TicketInboxWatch(sourceSessionID string, interval time.Duration) (*protocol.TicketInboxResult, error) {
	return c.ticketInbox(sourceSessionID, protocol.TicketInboxModeWatch, interval)
}

func (c *Client) ticketInbox(sourceSessionID string, mode protocol.TicketInboxMode, interval time.Duration) (*protocol.TicketInboxResult, error) {
	message := protocol.TicketInboxMessage{
		Cmd:             protocol.CmdTicketInbox,
		SourceSessionID: sourceSessionID,
		Mode:            protocol.Ptr(mode),
	}
	if mode == protocol.TicketInboxModeWatch && interval > 0 {
		message.WatchIntervalMs = protocol.Ptr(strconv.FormatInt(interval.Milliseconds(), 10))
	}
	resp, err := c.send(message)
	if err != nil {
		return nil, err
	}
	if resp.TicketInboxResult == nil {
		return nil, errors.New("daemon returned no ticket inbox result")
	}
	return resp.TicketInboxResult, nil
}

func (c *Client) TicketList(sourceSessionID, status string, includeArchived bool) ([]protocol.Ticket, error) {
	msg := protocol.TicketListMessage{Cmd: protocol.CmdTicketList}
	if sourceSessionID != "" {
		msg.SourceSessionID = &sourceSessionID
	}
	if status != "" {
		msg.Status = &status
	}
	if includeArchived {
		msg.IncludeArchived = &includeArchived
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.TicketListResult == nil {
		return nil, errors.New("daemon returned no ticket list result")
	}
	return resp.TicketListResult.Tickets, nil
}

func (c *Client) ShowTicket(sourceSessionID, ticketID string) (*protocol.Ticket, error) {
	msg := protocol.TicketShowMessage{Cmd: protocol.CmdTicketShow, TicketID: ticketID}
	if sourceSessionID != "" {
		msg.SourceSessionID = &sourceSessionID
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.TicketShowResult == nil {
		return nil, errors.New("daemon returned no ticket show result")
	}
	return &resp.TicketShowResult.Ticket, nil
}

func (c *Client) SubscribeTicket(sourceSessionID, ticketID string) (*protocol.TicketSubscribeResult, error) {
	resp, err := c.send(protocol.TicketSubscribeMessage{
		Cmd:             protocol.CmdTicketSubscribe,
		SourceSessionID: sourceSessionID,
		TicketID:        ticketID,
	})
	if err != nil {
		return nil, err
	}
	if resp.TicketSubscribeResult == nil {
		return nil, errors.New("daemon returned no ticket subscribe result")
	}
	return resp.TicketSubscribeResult, nil
}

func (c *Client) UnsubscribeTicket(sourceSessionID, ticketID string) (*protocol.TicketUnsubscribeResult, error) {
	resp, err := c.send(protocol.TicketUnsubscribeMessage{
		Cmd:             protocol.CmdTicketUnsubscribe,
		SourceSessionID: sourceSessionID,
		TicketID:        ticketID,
	})
	if err != nil {
		return nil, err
	}
	if resp.TicketUnsubscribeResult == nil {
		return nil, errors.New("daemon returned no ticket unsubscribe result")
	}
	return resp.TicketUnsubscribeResult, nil
}

func (c *Client) TakeTicket(sourceSessionID, ticketID string, confirm bool) (*protocol.TicketTakeResult, error) {
	msg := protocol.TicketTakeMessage{
		Cmd:             protocol.CmdTicketTake,
		SourceSessionID: sourceSessionID,
		TicketID:        ticketID,
	}
	if confirm {
		msg.Confirm = protocol.Ptr(true)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.TicketTakeResult == nil {
		return nil, errors.New("daemon returned no ticket take result")
	}
	return resp.TicketTakeResult, nil
}

func (c *Client) CheckoutWorkspaceContext(sourceSessionID string, force bool) (*protocol.WorkspaceContextResult, error) {
	msg := protocol.WorkspaceContextCheckoutMessage{
		Cmd:             protocol.CmdWorkspaceContextCheckout,
		SourceSessionID: sourceSessionID,
	}
	if force {
		msg.Force = protocol.Ptr(true)
	}
	return c.workspaceContextResult(msg)
}

func (c *Client) UpdateWorkspaceContext(sourceSessionID string) (*protocol.WorkspaceContextResult, error) {
	return c.workspaceContextResult(protocol.WorkspaceContextUpdateMessage{
		Cmd:             protocol.CmdWorkspaceContextUpdate,
		SourceSessionID: sourceSessionID,
	})
}

func (c *Client) WorkspaceContextStatus(sourceSessionID string) (*protocol.WorkspaceContextResult, error) {
	return c.workspaceContextResult(protocol.WorkspaceContextStatusMessage{
		Cmd:             protocol.CmdWorkspaceContextStatus,
		SourceSessionID: sourceSessionID,
	})
}

func (c *Client) CompactWorkspaceContext(sourceSessionID string) (*protocol.WorkspaceContextMaintenanceResult, error) {
	return c.workspaceContextMaintenanceResult(protocol.WorkspaceContextCompactMessage{
		Cmd:             protocol.CmdWorkspaceContextCompact,
		SourceSessionID: sourceSessionID,
	})
}

func (c *Client) RollbackWorkspaceContext(sourceSessionID string) (*protocol.WorkspaceContextMaintenanceResult, error) {
	return c.workspaceContextMaintenanceResult(protocol.WorkspaceContextRollbackMessage{
		Cmd:             protocol.CmdWorkspaceContextRollback,
		SourceSessionID: sourceSessionID,
	})
}

func (c *Client) workspaceContextResult(msg interface{}) (*protocol.WorkspaceContextResult, error) {
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.WorkspaceContextResult == nil {
		return nil, errors.New("daemon returned no workspace context result")
	}
	return resp.WorkspaceContextResult, nil
}

func (c *Client) workspaceContextMaintenanceResult(msg interface{}) (*protocol.WorkspaceContextMaintenanceResult, error) {
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.WorkspaceContextMaintenanceResult == nil {
		return nil, errors.New("daemon returned no workspace context maintenance result")
	}
	return resp.WorkspaceContextMaintenanceResult, nil
}

func (c *Client) NotebookGuide(sessionID string) (*protocol.NotebookGuideResult, error) {
	msg := protocol.NotebookGuideMessage{Cmd: protocol.CmdNotebookGuide}
	if sessionID != "" {
		msg.SessionID = protocol.Ptr(sessionID)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.NotebookGuide == nil {
		return nil, errors.New("daemon returned no notebook guide result")
	}
	return resp.NotebookGuide, nil
}

// AppendJournal goes through the daemon's serialized notebook.Store writer: editing
// journal/<date>.md directly races the keeper's own writes. Empty date means today.
func (c *Client) AppendJournal(sourceSessionID, date, entry string) (*protocol.JournalAppendResult, error) {
	msg := protocol.JournalAppendMessage{Cmd: protocol.CmdJournalAppend, Entry: entry}
	if sourceSessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sourceSessionID)
	}
	if date != "" {
		msg.Date = protocol.Ptr(date)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.JournalAppendResult == nil {
		return nil, errors.New("daemon returned no journal append result")
	}
	return resp.JournalAppendResult, nil
}

func (c *Client) ActivityStatus() (*protocol.ActivityStatusResult, error) {
	resp, err := c.send(protocol.ActivityStatusMessage{Cmd: protocol.CmdActivityStatus})
	if err != nil {
		return nil, err
	}
	if resp.ActivityStatusResult == nil {
		return nil, errors.New("daemon returned no activity status")
	}
	return resp.ActivityStatusResult, nil
}

func (c *Client) ClearSessionActivity(sessionID string) error {
	_, err := c.send(protocol.ClearSessionActivityMessage{
		Cmd: protocol.CmdClearSessionActivity,
		ID:  sessionID,
	})
	return err
}

func (c *Client) queryResponse(filter string) (*protocol.Response, error) {
	var filterPtr *string
	if filter != "" {
		filterPtr = &filter
	}
	msg := protocol.QueryMessage{
		Cmd:    protocol.CmdQuery,
		Filter: filterPtr,
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) Query(filter string) ([]protocol.Session, error) {
	resp, err := c.queryResponse(filter)
	if err != nil {
		return nil, err
	}
	return resp.Sessions, nil
}

func (c *Client) List(filter string) (*ListResult, error) {
	resp, err := c.queryResponse(filter)
	if err != nil {
		return nil, err
	}
	sessions := resp.Sessions
	if sessions == nil {
		sessions = []protocol.Session{}
	}
	workspaces := resp.Workspaces
	if workspaces == nil {
		workspaces = []protocol.Workspace{}
	}
	return &ListResult{
		Sessions:   sessions,
		Workspaces: workspaces,
	}, nil
}

func (c *Client) Heartbeat(id string) error {
	msg := protocol.HeartbeatMessage{
		Cmd: protocol.CmdHeartbeat,
		ID:  id,
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) ToggleWorkspaceMute(workspaceID string) error {
	msg := protocol.MuteWorkspaceMessage{
		Cmd:         protocol.CmdMuteWorkspace,
		WorkspaceID: workspaceID,
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) OpenMarkdown(path, sessionID string) error {
	msg := protocol.OpenMarkdownMessage{
		Cmd:  protocol.CmdOpenMarkdown,
		Path: path,
	}
	if sessionID != "" {
		msg.SessionID = protocol.Ptr(sessionID)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) OpenSeed(seedID, sessionID string) error {
	msg := protocol.OpenSeedMessage{Cmd: protocol.CmdOpenSeed, SeedID: seedID}
	if sessionID != "" {
		msg.SessionID = protocol.Ptr(sessionID)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) OpenSentFiles(sessionID string, paths []string) error {
	msg := protocol.OpenSentFilesMessage{
		Cmd:   protocol.CmdOpenSentFiles,
		Paths: paths,
	}
	if sessionID != "" {
		msg.SessionID = protocol.Ptr(sessionID)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) OpenBrowser(url, sessionID string) error {
	msg := protocol.OpenBrowserMessage{
		Cmd: protocol.CmdOpenBrowser,
		URL: url,
	}
	if sessionID != "" {
		msg.SessionID = protocol.Ptr(sessionID)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) BrowserControl(action, selector, text, sessionID string) (string, error) {
	return c.BrowserCommand(action, "", selector, text, sessionID)
}

func (c *Client) BrowserCommand(action, params, selector, text, sessionID string) (string, error) {
	msg := protocol.BrowserControlMessage{
		Cmd:    protocol.CmdBrowserControl,
		Action: action,
	}
	if params != "" {
		msg.Params = protocol.Ptr(params)
	}
	if selector != "" {
		msg.Selector = protocol.Ptr(selector)
	}
	if text != "" || action == "type" {
		msg.Text = protocol.Ptr(text)
	}
	if sessionID != "" {
		msg.SessionID = protocol.Ptr(sessionID)
	}
	resp, err := c.send(msg)
	if err != nil {
		return "", err
	}
	return protocol.Deref(resp.Data), nil
}

func (c *Client) QueryPRs(filter string) ([]protocol.PR, error) {
	var filterPtr *string
	if filter != "" {
		filterPtr = &filter
	}
	msg := protocol.QueryPRsMessage{
		Cmd:    protocol.CmdQueryPRs,
		Filter: filterPtr,
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.Prs, nil
}

func (c *Client) ToggleMutePR(id string) error {
	msg := protocol.MutePRMessage{
		Cmd: protocol.CmdMutePR,
		ID:  id,
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) ToggleMuteRepo(repo string) error {
	msg := map[string]string{
		"cmd":  protocol.CmdMuteRepo,
		"repo": repo,
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) SetRepoCollapsed(repo string, collapsed bool) error {
	msg := map[string]interface{}{
		"cmd":       protocol.CmdCollapseRepo,
		"repo":      repo,
		"collapsed": collapsed,
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) QueryRepos() ([]protocol.RepoState, error) {
	msg := map[string]string{
		"cmd": protocol.CmdQueryRepos,
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.Repos, nil
}

func (c *Client) QueryAuthors() ([]protocol.AuthorState, error) {
	msg := map[string]string{
		"cmd": protocol.CmdQueryAuthors,
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.Authors, nil
}

func (c *Client) FetchPRDetails(id string) ([]protocol.PR, error) {
	msg := protocol.FetchPRDetailsMessage{
		Cmd: protocol.CmdFetchPRDetails,
		ID:  id,
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.Prs, nil
}

func (c *Client) IsRunning() bool {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (c *Client) sendWorkflow(msg interface{}) (*protocol.WorkflowActionResultMessage, error) {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return nil, explainConnectError(c.socketPath, err)
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		return nil, fmt.Errorf("send message: %w", err)
	}

	var result protocol.WorkflowActionResultMessage
	if err := json.NewDecoder(conn).Decode(&result); err != nil {
		return nil, fmt.Errorf("receive response: %w", err)
	}

	if !result.Success {
		errMsg := ""
		if result.Error != nil {
			errMsg = *result.Error
		}
		return nil, fmt.Errorf("daemon error: %s", errMsg)
	}

	return &result, nil
}

func (c *Client) WorkflowRunUpsert(run *protocol.WorkflowRun) (*protocol.WorkflowRun, error) {
	if run == nil {
		return nil, errors.New("workflow run upsert: run is nil")
	}
	result, err := c.sendWorkflow(protocol.WorkflowRunUpsertMessage{
		Cmd: protocol.CmdWorkflowRunUpsert,
		Run: *run,
	})
	if err != nil {
		return nil, err
	}
	return result.Run, nil
}

func (c *Client) WorkflowCallUpsert(runID string, call *protocol.WorkflowAgentCall) (*protocol.WorkflowRun, error) {
	if call == nil {
		return nil, errors.New("workflow call upsert: call is nil")
	}
	result, err := c.sendWorkflow(protocol.WorkflowCallUpsertMessage{
		Cmd:   protocol.CmdWorkflowCallUpsert,
		RunID: runID,
		Call:  *call,
	})
	if err != nil {
		return nil, err
	}
	return result.Run, nil
}

func (c *Client) WorkflowRunGet(runID string) (*protocol.WorkflowRun, error) {
	result, err := c.sendWorkflow(protocol.WorkflowRunGetMessage{
		Cmd:   protocol.CmdWorkflowRunGet,
		RunID: runID,
	})
	if err != nil {
		return nil, err
	}
	return result.Run, nil
}

func (c *Client) WorkflowRunList(sessionID string) ([]protocol.WorkflowRun, error) {
	msg := protocol.WorkflowRunListMessage{
		Cmd: protocol.CmdWorkflowRunList,
	}
	if sessionID != "" {
		msg.SessionID = protocol.Ptr(sessionID)
	}
	result, err := c.sendWorkflow(msg)
	if err != nil {
		return nil, err
	}
	return result.Runs, nil
}

func (c *Client) WorkflowRunCancel(runID string) (*protocol.WorkflowRun, error) {
	result, err := c.sendWorkflow(protocol.WorkflowRunCancelMessage{
		Cmd:   protocol.CmdWorkflowRunCancel,
		RunID: runID,
	})
	if err != nil {
		return nil, err
	}
	return result.Run, nil
}

func explainConnectError(sockPath string, cause error) error {
	profile := config.ProfileLabel()
	base := fmt.Sprintf("connect to daemon at %s (profile=%s): %v",
		config.CollapseHome(sockPath), profile, cause)
	if hint := crossProfileHint(); hint != "" {
		return errors.New(base + "\n  " + hint)
	}
	return errors.New(base)
}

// crossProfileHint is "" unless the OTHER profile's daemon appears to be running.
func crossProfileHint() string {
	current := config.Profile()
	if current == "" {
		otherSock := config.SocketPathForProfile("dev")
		if socketLive(otherSock) {
			return fmt.Sprintf("hint: a dev daemon is listening at %s — run `eval \"$(attn profile-env dev)\"` to switch this shell",
				config.CollapseHome(otherSock))
		}
		return ""
	}
	otherSock := config.SocketPathForProfile("")
	if socketLive(otherSock) {
		return fmt.Sprintf("hint: the default daemon is listening at %s — run `eval \"$(attn profile-env --unset)\"` to switch this shell",
			config.CollapseHome(otherSock))
	}
	return ""
}

func socketLive(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	if _, err := os.Stat(path); err != nil {
		return false
	}
	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
