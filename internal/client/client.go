package client

import (
	"encoding/json"
	"fmt"
	"net"

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
		return nil, fmt.Errorf("connect to daemon: %w", err)
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
// agent should be "claude" or "codex"; empty preserves daemon default behavior.
func (c *Client) RegisterWithAgent(id, label, dir, agent string) error {
	msg := protocol.RegisterMessage{
		Cmd:   protocol.CmdRegister,
		ID:    id,
		Label: protocol.Ptr(label),
		Dir:   dir,
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

// Query returns sessions matching the filter
func (c *Client) Query(filter string) ([]protocol.Session, error) {
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
	return resp.Sessions, nil
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

// ToggleMute toggles a session's muted state
func (c *Client) ToggleMute(id string) error {
	msg := protocol.MuteMessage{
		Cmd: protocol.CmdMute,
		ID:  id,
	}
	_, err := c.send(msg)
	return err
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
