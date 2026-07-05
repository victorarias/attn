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
	Model        string
	Effort       string
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

// CreateTicket mints a standalone backlog ticket — unbound, starting in todo. The
// daemon derives the slug from the title (or pins an explicit id), records the
// calling session as the author, and echoes the resolved id, status, and title back.
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

// CommentTicket posts a one-shot comment from the calling session onto any ticket
// by id — not just the one bound to the session. The daemon authors the comment as
// the session and notifies the ticket's participants, but commenting does not
// subscribe the caller to the ticket's future activity. Echoes the ticket id back.
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

// PresentOpen opens a new presentation (presentationID == "") or a new round on
// an existing one (presentationID set) from a raw manifest YAML. The daemon is
// the sole authority for parsing and pinning it — this call sends the manifest
// bytes verbatim, never a locally-parsed shape.
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

// PresentFeedback reads a round's reviewer feedback back as markdown. seq <= 0
// means the latest round.
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

// TicketInbox reads and consumes the calling session's unread ticket events,
// bundled by ticket. Reading advances the session's per-ticket cursors, so a
// second call returns only what landed since. The result also carries
// last_user_activity_at, the daemon's most recent observed user-presence
// signal, so a watching agent can decide whether to push or hold.
func (c *Client) TicketInbox(sourceSessionID string) (*protocol.TicketInboxResult, error) {
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
	return resp.TicketInboxResult, nil
}

// TicketList reads the board — every non-archived ticket, newest first, optionally
// filtered by status (or including archived). It is a global read, not scoped to the
// caller: sourceSessionID is passed for command-shape uniformity but the daemon does
// not use it. status == "" matches any status. Rows carry the description but not the
// activity thread (bare rows, like the app's board feed).
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

// ShowTicket reads one ticket's full record — metadata, description, and the
// complete activity thread (full bodies) plus attachments. It is a non-consuming
// read (unlike TicketInbox, it never advances the calling session's unread
// cursor) and, like TicketList, a global read: sourceSessionID is passed for
// command-shape uniformity but the daemon does not use it.
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

// SubscribeTicket opts the calling session into a ticket's notifications. The
// session becomes a participant (nudged about activity, the ticket delivered in its
// inbox) without advancing its cursor, so its first inbox after this delivers the
// ticket's history. The ticket must exist; re-subscribing is idempotent.
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

// UnsubscribeTicket opts the calling session back out of a ticket's notifications.
// It is idempotent — opting out when not subscribed succeeds — and does not require
// the ticket to still exist.
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

// TakeTicket claims a ticket for the calling session. Taking a ticket already
// assigned to someone else requires confirm=true; without it the daemon refuses
// so an agent cannot silently take over another's active work.
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

// AppendJournal appends entry to the notebook's dated daily journal
// (journal/<date>.md) through the daemon's serialized notebook.Store writer —
// the contention-safe alternative to an agent editing the journal file directly,
// which races the daemon's own keeper writes. date defaults (daemon-side) to
// today when empty; sourceSessionID is optional and unused, present only for
// command-shape uniformity with the ticket verbs.
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
