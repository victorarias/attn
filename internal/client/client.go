package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

// DefaultSocketPath returns the default socket path
func DefaultSocketPath() string {
	return config.SocketPath()
}

// Client communicates with the daemon
type Client struct {
	socketPath string
}

type ListResult struct {
	Sessions   []protocol.Session   `json:"sessions"`
	Workspaces []protocol.Workspace `json:"workspaces"`
}

// New creates a new client
func New(socketPath string) *Client {
	if socketPath == "" {
		socketPath = DefaultSocketPath()
	}
	return &Client{socketPath: socketPath}
}

// send sends a message and receives a response
func (c *Client) send(msg interface{}) (*protocol.Response, error) {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return nil, explainConnectError(c.socketPath, err)
	}
	defer conn.Close()

	// Send message
	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		return nil, fmt.Errorf("send message: %w", err)
	}

	// Receive response
	var resp protocol.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("receive response: %w", err)
	}

	if !resp.Ok {
		errMsg := ""
		if resp.Error != nil {
			errMsg = *resp.Error
		}
		return nil, fmt.Errorf("daemon error: %s", errMsg)
	}

	return &resp, nil
}

// Register registers a new session
func (c *Client) Register(id, label, dir string) error {
	return c.RegisterWithAgent(id, label, dir, "")
}

// RegisterWithAgent registers a new session with an explicit agent.
// agent should be "claude", "codex", or "copilot"; empty preserves daemon default behavior.
func (c *Client) RegisterWithAgent(id, label, dir, agent string) error {
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
	_, err := c.send(msg)
	return err
}

// Unregister removes a session
func (c *Client) Unregister(id string) error {
	msg := protocol.UnregisterMessage{
		Cmd: protocol.CmdUnregister,
		ID:  id,
	}
	_, err := c.send(msg)
	return err
}

// UpdateState updates a session's state
func (c *Client) UpdateState(id, state string) error {
	msg := protocol.StateMessage{
		Cmd:   protocol.CmdState,
		ID:    id,
		State: state,
	}
	_, err := c.send(msg)
	return err
}

// SetSessionResumeID stores the agent-native resume session id for an attn session.
func (c *Client) SetSessionResumeID(id, resumeSessionID string) error {
	msg := protocol.SetSessionResumeIDMessage{
		Cmd:             protocol.CmdSetSessionResumeID,
		ID:              id,
		ResumeSessionID: resumeSessionID,
	}
	_, err := c.send(msg)
	return err
}

// SendStop sends a stop signal with transcript path for classification
func (c *Client) SendStop(id, transcriptPath string) error {
	msg := protocol.StopMessage{
		Cmd:            protocol.CmdStop,
		ID:             id,
		TranscriptPath: transcriptPath,
	}
	_, err := c.send(msg)
	return err
}

// UpdateTodos updates a session's todo list
func (c *Client) UpdateTodos(id string, todos []string) error {
	msg := protocol.TodosMessage{
		Cmd:   protocol.CmdTodos,
		ID:    id,
		Todos: todos,
	}
	_, err := c.send(msg)
	return err
}

type DelegateOptions struct {
	Agent        string
	Label        string
	Yolo         bool
	Placement    string
	WorkspaceID  string
	CWD          string
	WorktreeRepo string
	Worktree     string
	WorktreePath string
	StartingFrom string
}

// Delegate starts another agent with an initial brief and optional placement.
func (c *Client) Delegate(sourceSessionID, brief string, opts DelegateOptions) (*protocol.DelegateResult, error) {
	msg := protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           brief,
	}
	if value := strings.TrimSpace(opts.Agent); value != "" {
		msg.Agent = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(opts.Label); value != "" {
		msg.Label = protocol.Ptr(value)
	}
	if opts.Yolo {
		msg.YoloMode = protocol.Ptr(true)
	}
	if value := strings.TrimSpace(opts.Placement); value != "" {
		msg.Placement = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(opts.WorkspaceID); value != "" {
		msg.WorkspaceID = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(opts.CWD); value != "" {
		msg.Cwd = protocol.Ptr(value)
	}
	if branch := strings.TrimSpace(opts.Worktree); branch != "" {
		msg.Worktree = &protocol.DelegateWorktreeRequest{
			Branch: branch,
		}
		if value := strings.TrimSpace(opts.WorktreeRepo); value != "" {
			msg.Worktree.Repo = protocol.Ptr(value)
		}
		if value := strings.TrimSpace(opts.WorktreePath); value != "" {
			msg.Worktree.Path = protocol.Ptr(value)
		}
		if value := strings.TrimSpace(opts.StartingFrom); value != "" {
			msg.Worktree.StartingFrom = protocol.Ptr(value)
		}
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.DelegateResult == nil {
		return nil, errors.New("daemon returned no delegation result")
	}
	return resp.DelegateResult, nil
}

func (c *Client) ListDispatches(sourceSessionID string) ([]protocol.ChiefOfStaffDispatch, error) {
	resp, err := c.send(protocol.ListDispatchesMessage{
		Cmd:             protocol.CmdListDispatches,
		SourceSessionID: sourceSessionID,
	})
	if err != nil {
		return nil, err
	}
	return resp.ChiefOfStaffDispatches, nil
}

func (c *Client) SubmitDispatchOutcome(
	sourceSessionID, report string,
	structuredReport protocol.DispatchReport,
) (*protocol.ChiefOfStaffDispatch, error) {
	resp, err := c.send(protocol.SubmitDispatchOutcomeMessage{
		Cmd:              protocol.CmdSubmitDispatchOutcome,
		SourceSessionID:  sourceSessionID,
		Report:           report,
		StructuredReport: structuredReport,
	})
	if err != nil {
		return nil, err
	}
	if resp.ChiefOfStaffDispatch == nil {
		return nil, errors.New("daemon returned no dispatch")
	}
	return resp.ChiefOfStaffDispatch, nil
}

// HandoffDispatch writes a dispatched agent's artifact into the Notebook at `to`
// and records a typed dispatch outcome referencing it.
func (c *Client) HandoffDispatch(
	sourceSessionID, to, content, report string,
	structuredReport protocol.DispatchReport,
) (*protocol.ChiefOfStaffDispatch, error) {
	msg := protocol.HandoffDispatchMessage{
		Cmd:              protocol.CmdHandoffDispatch,
		SourceSessionID:  sourceSessionID,
		To:               to,
		Content:          content,
		StructuredReport: structuredReport,
	}
	if strings.TrimSpace(report) != "" {
		msg.Report = &report
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.ChiefOfStaffDispatch == nil {
		return nil, errors.New("daemon returned no dispatch")
	}
	return resp.ChiefOfStaffDispatch, nil
}

func (c *Client) GetDispatch(sourceSessionID string) (*protocol.ChiefOfStaffDispatch, error) {
	resp, err := c.send(protocol.GetDispatchMessage{
		Cmd:             protocol.CmdGetDispatch,
		SourceSessionID: sourceSessionID,
	})
	if err != nil {
		return nil, err
	}
	if resp.ChiefOfStaffDispatch == nil {
		return nil, errors.New("daemon returned no dispatch")
	}
	return resp.ChiefOfStaffDispatch, nil
}

func (c *Client) ResolveDispatchRequest(
	sourceSessionID, dispatchID, response, resolutionLink string,
) (*protocol.ChiefOfStaffDispatch, error) {
	msg := protocol.ResolveDispatchRequestMessage{
		Cmd:             protocol.CmdResolveDispatchRequest,
		SourceSessionID: sourceSessionID,
		DispatchID:      dispatchID,
		Response:        response,
	}
	if value := strings.TrimSpace(resolutionLink); value != "" {
		msg.ResolutionLink = protocol.Ptr(value)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.ChiefOfStaffDispatch == nil {
		return nil, errors.New("daemon returned no dispatch")
	}
	return resp.ChiefOfStaffDispatch, nil
}

func (c *Client) SendDispatchMessage(
	sourceSessionID, dispatchID, content string,
) (*protocol.DispatchMessage, error) {
	resp, err := c.send(protocol.SendDispatchMessage{
		Cmd:             protocol.CmdSendDispatchMessage,
		SourceSessionID: sourceSessionID,
		DispatchID:      dispatchID,
		Content:         content,
	})
	if err != nil {
		return nil, err
	}
	if resp.DispatchMessage == nil {
		return nil, errors.New("daemon returned no dispatch message")
	}
	return resp.DispatchMessage, nil
}

func (c *Client) ListDispatchMessages(
	sourceSessionID, dispatchID string,
	unreadOnly bool,
) ([]protocol.DispatchMessage, error) {
	msg := protocol.ListDispatchMessagesMessage{
		Cmd:             protocol.CmdListDispatchMessages,
		SourceSessionID: sourceSessionID,
	}
	if value := strings.TrimSpace(dispatchID); value != "" {
		msg.DispatchID = protocol.Ptr(value)
	}
	if unreadOnly {
		msg.UnreadOnly = protocol.Ptr(true)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.DispatchMessages, nil
}

func (c *Client) ReadDispatchMessage(
	sourceSessionID, messageID string,
) (*protocol.DispatchMessage, error) {
	resp, err := c.send(protocol.ReadDispatchMessage{
		Cmd:             protocol.CmdReadDispatchMessage,
		SourceSessionID: sourceSessionID,
		MessageID:       messageID,
	})
	if err != nil {
		return nil, err
	}
	if resp.DispatchMessage == nil {
		return nil, errors.New("daemon returned no dispatch message")
	}
	return resp.DispatchMessage, nil
}

func (c *Client) AcknowledgeDispatchMessage(
	sourceSessionID, messageID, acknowledgement string,
) (*protocol.DispatchMessage, error) {
	msg := protocol.AcknowledgeDispatchMessage{
		Cmd:             protocol.CmdAcknowledgeDispatchMessage,
		SourceSessionID: sourceSessionID,
		MessageID:       messageID,
	}
	if value := strings.TrimSpace(acknowledgement); value != "" {
		msg.Acknowledgement = protocol.Ptr(value)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.DispatchMessage == nil {
		return nil, errors.New("daemon returned no dispatch message")
	}
	return resp.DispatchMessage, nil
}

// SetTicketStatus reports the calling agent's work state; the daemon moves the
// session's bound ticket to the matching column and echoes the resolved id and
// status back.
func (c *Client) SetTicketStatus(sourceSessionID, workState, comment string) (*protocol.TicketStatusResult, error) {
	msg := protocol.SetTicketStatusMessage{
		Cmd:             protocol.CmdSetTicketStatus,
		SourceSessionID: sourceSessionID,
		WorkState:       protocol.DispatchWorkState(workState),
	}
	if value := strings.TrimSpace(comment); value != "" {
		msg.Comment = protocol.Ptr(value)
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

// AttachTicket hands a file to the calling session's bound ticket. sourcePath is
// the absolute path of the file the daemon copies into the ticket's store; the
// daemon resolves which ticket from the session and echoes the id and stored
// filename back.
func (c *Client) AttachTicket(sourceSessionID, sourcePath, filename, note string) (*protocol.TicketAttachResult, error) {
	msg := protocol.TicketAttachMessage{
		Cmd:             protocol.CmdTicketAttach,
		SourceSessionID: sourceSessionID,
		SourcePath:      sourcePath,
		Filename:        filename,
	}
	if value := strings.TrimSpace(note); value != "" {
		msg.Note = protocol.Ptr(value)
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

// TicketInbox reads and consumes the calling session's unread ticket events,
// bundled by ticket. Reading advances the session's per-ticket cursors, so a
// second call returns only what landed since.
func (c *Client) TicketInbox(sourceSessionID string) ([]protocol.TicketEventBundle, error) {
	resp, err := c.send(protocol.TicketInboxMessage{
		Cmd:             protocol.CmdTicketInbox,
		SourceSessionID: sourceSessionID,
	})
	if err != nil {
		return nil, err
	}
	if resp.TicketInboxResult == nil {
		return nil, errors.New("daemon returned no ticket inbox result")
	}
	return resp.TicketInboxResult.Bundles, nil
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

// NotebookGuide pulls the canonical notebook operating guidance. When sessionID
// is non-empty, the result's SessionIsChief reflects whether that session holds
// the chief-of-staff role (used by the launch path to choose guidance).
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

// Query returns sessions matching the filter.
func (c *Client) Query(filter string) ([]protocol.Session, error) {
	resp, err := c.queryResponse(filter)
	if err != nil {
		return nil, err
	}
	return resp.Sessions, nil
}

// List returns the decorated sessions and workspace snapshots used by `attn list`.
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

// Heartbeat sends a heartbeat for a session
func (c *Client) Heartbeat(id string) error {
	msg := protocol.HeartbeatMessage{
		Cmd: protocol.CmdHeartbeat,
		ID:  id,
	}
	_, err := c.send(msg)
	return err
}

// ToggleWorkspaceMute toggles a workspace's muted state.
func (c *Client) ToggleWorkspaceMute(workspaceID string) error {
	msg := protocol.MuteWorkspaceMessage{
		Cmd:         protocol.CmdMuteWorkspace,
		WorkspaceID: workspaceID,
	}
	_, err := c.send(msg)
	return err
}

// OpenMarkdown docks a live-reloading markdown tile for the given absolute file
// path. An empty sessionID lets the daemon target the currently selected
// session's workspace.
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

// OpenBrowser docks or retargets the in-app browser tile.
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

// BrowserControl asks the in-app browser to perform an action and
// returns its textual result. Screenshot results are base64-encoded PNG bytes.
func (c *Client) BrowserControl(action, selector, text, sessionID string) (string, error) {
	return c.BrowserCommand(action, "", selector, text, sessionID)
}

// BrowserCommand sends a structured browser automation request. params is a
// JSON object encoded as a string so the protocol can evolve without exposing
// an unauthenticated WebDriver server.
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

// QueryPRs returns PRs matching the filter
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

// ToggleMutePR toggles a PR's muted state
func (c *Client) ToggleMutePR(id string) error {
	msg := protocol.MutePRMessage{
		Cmd: protocol.CmdMutePR,
		ID:  id,
	}
	_, err := c.send(msg)
	return err
}

// ToggleMuteRepo toggles a repo's muted state
func (c *Client) ToggleMuteRepo(repo string) error {
	msg := map[string]string{
		"cmd":  protocol.CmdMuteRepo,
		"repo": repo,
	}
	_, err := c.send(msg)
	return err
}

// SetRepoCollapsed sets a repo's collapsed state
func (c *Client) SetRepoCollapsed(repo string, collapsed bool) error {
	msg := map[string]interface{}{
		"cmd":       protocol.CmdCollapseRepo,
		"repo":      repo,
		"collapsed": collapsed,
	}
	_, err := c.send(msg)
	return err
}

// QueryRepos returns all repo states
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

// QueryAuthors returns all author states
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

// StartReviewLoop starts a session-level review loop.
func (c *Client) StartReviewLoop(sessionID, presetID, prompt string, iterationLimit int) (*protocol.ReviewLoopRun, error) {
	return c.StartReviewLoopWithHandoff(sessionID, presetID, prompt, iterationLimit, nil)
}

// StartReviewLoopWithHandoff starts a review loop with optional structured handoff JSON.
func (c *Client) StartReviewLoopWithHandoff(sessionID, presetID, prompt string, iterationLimit int, handoffPayloadJSON *string) (*protocol.ReviewLoopRun, error) {
	msg := protocol.StartReviewLoopMessage{
		Cmd:            protocol.CmdStartReviewLoop,
		SessionID:      sessionID,
		Prompt:         prompt,
		IterationLimit: iterationLimit,
	}
	if presetID != "" {
		msg.PresetID = &presetID
	}
	if handoffPayloadJSON != nil && strings.TrimSpace(*handoffPayloadJSON) != "" {
		msg.HandoffPayloadJson = handoffPayloadJSON
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.ReviewLoopRun, nil
}

// StopReviewLoop stops a session-level review loop.
func (c *Client) StopReviewLoop(sessionID string) (*protocol.ReviewLoopRun, error) {
	msg := protocol.StopReviewLoopMessage{
		Cmd:       protocol.CmdStopReviewLoop,
		SessionID: sessionID,
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.ReviewLoopRun, nil
}

// GetReviewLoopState returns session-level review loop state.
func (c *Client) GetReviewLoopState(sessionID string) (*protocol.ReviewLoopRun, error) {
	msg := protocol.GetReviewLoopStateMessage{
		Cmd:       protocol.CmdGetReviewLoopState,
		SessionID: sessionID,
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.ReviewLoopRun, nil
}

// GetReviewLoopRun returns a review loop by loop ID.
func (c *Client) GetReviewLoopRun(loopID string) (*protocol.ReviewLoopRun, error) {
	msg := protocol.GetReviewLoopRunMessage{
		Cmd:    protocol.CmdGetReviewLoopRun,
		LoopID: loopID,
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.ReviewLoopRun, nil
}

// SetReviewLoopIterationLimit updates the loop iteration limit.
func (c *Client) SetReviewLoopIterationLimit(sessionID string, iterationLimit int) (*protocol.ReviewLoopRun, error) {
	msg := protocol.SetReviewLoopIterationLimitMessage{
		Cmd:            protocol.CmdSetReviewLoopIterations,
		SessionID:      sessionID,
		IterationLimit: iterationLimit,
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.ReviewLoopRun, nil
}

// AnswerReviewLoop provides a user answer and resumes an awaiting loop.
func (c *Client) AnswerReviewLoop(loopID, interactionID, answer string) (*protocol.ReviewLoopRun, error) {
	msg := protocol.AnswerReviewLoopMessage{
		Cmd:    protocol.CmdAnswerReviewLoop,
		LoopID: loopID,
		Answer: answer,
	}
	if strings.TrimSpace(interactionID) != "" {
		msg.InteractionID = &interactionID
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.ReviewLoopRun, nil
}

// FetchPRDetails requests the daemon to fetch PR details for a PR ID
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

// IsRunning checks if the daemon is running
func (c *Client) IsRunning() bool {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// sendWorkflow sends a workflow command and decodes the daemon's
// WorkflowActionResultMessage reply (the shared reply shape the workflow socket
// handlers write — protocol.Response has no workflow field, so the workflow
// transport uses its own envelope). It is the workflow analogue of send().
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

// WorkflowRunUpsert persists (creates or updates) a workflow run row and returns
// the daemon's hydrated view of it.
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

// WorkflowCallUpsert persists a single agent call (ON CONFLICT(run_id, ordinal)
// updates in place) and returns the daemon's hydrated view of the owning run.
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

// WorkflowRunGet returns the hydrated run (run header + journaled agent calls),
// or (nil, nil) when the run is absent.
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

// WorkflowRunList returns the runs for a session (empty sessionID lists all),
// newest-first. Agent calls are intentionally omitted from list entries.
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

// WorkflowRunCancel marks a run canceled and returns its hydrated view.
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

// explainConnectError wraps a dial failure with profile context and — if
// the *other* profile's daemon happens to be running — a concrete hint
// on how to reach it. This is the single foot-gun everyone hits when
// first adopting ATTN_PROFILE, so we pay it down here.
func explainConnectError(sockPath string, cause error) error {
	profile := config.ProfileLabel()
	base := fmt.Sprintf("connect to daemon at %s (profile=%s): %v",
		config.CollapseHome(sockPath), profile, cause)
	if hint := crossProfileHint(); hint != "" {
		return errors.New(base + "\n  " + hint)
	}
	return errors.New(base)
}

// crossProfileHint returns a one-line suggestion when the *other* profile's
// daemon appears to be running. Returns "" when no such hint is useful.
func crossProfileHint() string {
	current := config.Profile()
	if current == "" {
		// Currently default → probe dev.
		otherSock := config.SocketPathForProfile("dev")
		if socketLive(otherSock) {
			return fmt.Sprintf("hint: a dev daemon is listening at %s — run `eval \"$(attn profile-env dev)\"` to switch this shell",
				config.CollapseHome(otherSock))
		}
		return ""
	}
	// Currently non-default → probe default.
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
