package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/claude-manager/internal/protocol"
)

// Valid setting keys
const (
	SettingProjectsDirectory = "projects_directory"
)

// wsClient represents a connected WebSocket client
type wsClient struct {
	conn      *websocket.Conn
	send      chan []byte
	slowCount int // tracks consecutive failed sends

	// Git status subscription state
	gitStatusDir    string
	gitStatusTicker *time.Ticker
	gitStatusStop   chan struct{}
	gitStatusHash   string // hash of last sent status for dedup
	gitStatusMu     sync.Mutex
}

// stopGitStatusPoll stops any active git status polling for this client
func (c *wsClient) stopGitStatusPoll() {
	c.gitStatusMu.Lock()
	defer c.gitStatusMu.Unlock()

	if c.gitStatusTicker != nil {
		c.gitStatusTicker.Stop()
		c.gitStatusTicker = nil
	}
	if c.gitStatusStop != nil {
		close(c.gitStatusStop)
		c.gitStatusStop = nil
	}
	c.gitStatusDir = ""
	c.gitStatusHash = ""
}

// BroadcastListener is called for each broadcast event (for testing)
type BroadcastListener func(event *protocol.WebSocketEvent)

// wsHub manages all WebSocket connections
type wsHub struct {
	clients           map[*wsClient]bool
	broadcast         chan []byte
	register          chan *wsClient
	unregister        chan *wsClient
	mu                sync.RWMutex
	logf              func(format string, args ...interface{})
	broadcastListener BroadcastListener // Optional listener for testing
}

const maxSlowCount = 3 // disconnect after this many consecutive failed sends

func newWSHub() *wsHub {
	return &wsHub{
		clients:    make(map[*wsClient]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *wsClient),
		unregister: make(chan *wsClient),
		logf:       func(format string, args ...interface{}) {}, // no-op by default
	}
}

func (h *wsHub) run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				// Cleanup git status subscription
				client.stopGitStatusPoll()
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.Lock()
			var toRemove []*wsClient
			for client := range h.clients {
				select {
				case client.send <- message:
					client.slowCount = 0 // reset on successful send
				default:
					// Client buffer full
					client.slowCount++
					if client.slowCount >= maxSlowCount {
						h.logf("WebSocket client too slow (%d missed), disconnecting", client.slowCount)
						toRemove = append(toRemove, client)
					} else {
						h.logf("WebSocket client slow (%d/%d missed)", client.slowCount, maxSlowCount)
					}
				}
			}
			// Remove slow clients outside the iteration
			for _, client := range toRemove {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
		}
	}
}

// Broadcast sends an event to all connected clients
func (h *wsHub) Broadcast(event *protocol.WebSocketEvent) {
	// Call listener if set (for testing)
	if h.broadcastListener != nil {
		h.broadcastListener(event)
	}

	data, err := json.Marshal(event)
	if err != nil {
		h.logf("WebSocket broadcast marshal error: %v", err)
		return
	}
	select {
	case h.broadcast <- data:
		// Message queued for broadcast
	default:
		// Broadcast channel full - this indicates the hub is overwhelmed
		h.logf("WebSocket broadcast channel full, dropping %s event", event.Event)
	}
}

// ClientCount returns number of connected clients
func (h *wsHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// handleWS handles WebSocket connections
func (d *Daemon) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"}, // Allow all origins for local app
	})
	if err != nil {
		d.logf("WebSocket accept error: %v", err)
		return
	}

	client := &wsClient{
		conn: conn,
		send: make(chan []byte, 256),
	}

	d.wsHub.register <- client
	d.logf("WebSocket client connected (%d total)", d.wsHub.ClientCount())

	// Send initial state
	d.sendInitialState(client)

	// Start ping keepalive (detects dead connections, keeps proxies happy)
	done := make(chan struct{})
	go d.wsPingLoop(client, done)

	// Handle client lifecycle
	go d.wsWritePump(client)
	d.wsReadPump(client)

	// Signal ping loop to stop when read pump exits
	close(done)
}

func (d *Daemon) sendInitialState(client *wsClient) {
	// Convert settings to interface{} map for generated type
	settings := make(map[string]interface{})
	for k, v := range d.store.GetAllSettings() {
		settings[k] = v
	}
	event := &protocol.WebSocketEvent{
		Event:           protocol.EventInitialState,
		ProtocolVersion: protocol.Ptr(protocol.ProtocolVersion),
		Sessions:        protocol.SessionsToValues(d.store.List("")),
		Prs:             protocol.PRsToValues(d.store.ListPRs("")),
		Repos:           protocol.RepoStatesToValues(d.store.ListRepoStates()),
		Settings:        settings,
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	select {
	case client.send <- data:
	default:
	}

	// Fetch details for all PRs in background (app launch)
	go d.fetchAllPRDetails()
}

func (d *Daemon) wsWritePump(client *wsClient) {
	defer func() {
		client.conn.Close(websocket.StatusNormalClosure, "")
	}()

	for message := range client.send {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := client.conn.Write(ctx, websocket.MessageText, message)
		cancel()
		if err != nil {
			return
		}
	}
}

// wsPingLoop sends periodic pings to keep the connection alive and detect dead clients
func (d *Daemon) wsPingLoop(client *wsClient, done <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := client.conn.Ping(ctx)
			cancel()
			if err != nil {
				d.logf("WebSocket ping failed: %v", err)
				client.conn.Close(websocket.StatusGoingAway, "ping timeout")
				return
			}
		}
	}
}

func (d *Daemon) wsReadPump(client *wsClient) {
	defer func() {
		d.wsHub.unregister <- client
		client.conn.Close(websocket.StatusNormalClosure, "")
		d.logf("WebSocket client disconnected (%d remaining)", d.wsHub.ClientCount())
	}()

	for {
		// No read timeout - clients don't send data regularly.
		// Connection liveness is detected by ping loop.
		// If ping fails, it closes the connection which unblocks this Read().
		_, data, err := client.conn.Read(context.Background())
		if err != nil {
			d.logf("WebSocket read error: %v", err)
			return
		}
		d.logf("WebSocket raw data received: %s", string(data))
		// Handle client messages
		go d.handleClientMessage(client, data)
	}
}

func (d *Daemon) handleClientMessage(client *wsClient, data []byte) {
	d.logf("WebSocket received: %s", string(data))
	cmd, msg, err := protocol.ParseMessage(data)
	if err != nil {
		d.logf("WebSocket parse error: %v", err)
		return
	}
	d.logf("WebSocket parsed cmd: %s", cmd)

	switch cmd {
	case protocol.CmdApprovePR:
		appMsg := msg.(*protocol.ApprovePRMessage)
		d.logf("Processing approve for %s#%d", appMsg.Repo, appMsg.Number)
		go func() {
			err := d.ghClient.ApprovePR(appMsg.Repo, appMsg.Number)
			result := protocol.PRActionResultMessage{
				Event:   protocol.EventPRActionResult,
				Action:  "approve",
				Repo:    appMsg.Repo,
				Number:  appMsg.Number,
				Success: err == nil,
			}
			if err != nil {
				result.Error = protocol.Ptr(err.Error())
				d.logf("Approve failed for %s#%d: %v", appMsg.Repo, appMsg.Number, err)
			} else {
				d.logf("Approve succeeded for %s#%d", appMsg.Repo, appMsg.Number)
				// Track approval interaction
				prID := fmt.Sprintf("%s#%d", appMsg.Repo, appMsg.Number)
				d.store.MarkPRApproved(prID)
				d.store.SetPRHot(prID)
				go d.fetchPRDetailsImmediate(prID)
			}
			d.sendToClient(client, result)
			d.logf("Sent approve result to client")
			// Trigger PR refresh after action
			d.RefreshPRs()
		}()

	case protocol.CmdMergePR:
		mergeMsg := msg.(*protocol.MergePRMessage)
		go func() {
			err := d.ghClient.MergePR(mergeMsg.Repo, mergeMsg.Number, mergeMsg.Method)
			result := protocol.PRActionResultMessage{
				Event:   protocol.EventPRActionResult,
				Action:  "merge",
				Repo:    mergeMsg.Repo,
				Number:  mergeMsg.Number,
				Success: err == nil,
			}
			if err != nil {
				result.Error = protocol.Ptr(err.Error())
			}
			d.sendToClient(client, result)
			// Trigger PR refresh after action
			d.RefreshPRs()
		}()

	case protocol.CmdMutePR:
		muteMsg := msg.(*protocol.MutePRMessage)
		// Check if we're unmuting (PR was muted before)
		pr := d.store.GetPR(muteMsg.ID)
		wasMuted := pr != nil && pr.Muted

		d.store.ToggleMutePR(muteMsg.ID)

		// If unmuting, set hot and fetch details
		if wasMuted {
			d.store.SetPRHot(muteMsg.ID)
			go d.fetchPRDetailsImmediate(muteMsg.ID)
		}
		d.broadcastPRs()

	case protocol.CmdMuteRepo:
		muteMsg := msg.(*protocol.MuteRepoMessage)
		// Check if we're unmuting
		repoState := d.store.GetRepoState(muteMsg.Repo)
		wasMuted := repoState != nil && repoState.Muted

		d.store.ToggleMuteRepo(muteMsg.Repo)

		// If unmuting, set all repo PRs hot and fetch details
		if wasMuted {
			prs := d.store.ListPRsByRepo(muteMsg.Repo)
			for _, pr := range prs {
				d.store.SetPRHot(pr.ID)
				go d.fetchPRDetailsImmediate(pr.ID)
			}
			// Only broadcast PRs if there are PRs to update
			if len(prs) > 0 {
				d.broadcastPRs()
			}
		}
		d.broadcastRepoStates()

	case protocol.CmdRefreshPRs:
		d.logf("Refreshing PRs on request")
		go func() {
			err := d.doRefreshPRsWithResult()
			result := protocol.RefreshPRsResultMessage{
				Event:   protocol.EventRefreshPRsResult,
				Success: err == nil,
			}
			if err != nil {
				result.Error = protocol.Ptr(err.Error())
				d.logf("Refresh PRs failed: %v", err)
			} else {
				d.logf("Refresh PRs succeeded")
			}
			d.sendToClient(client, result)
		}()

	case protocol.CmdClearSessions:
		d.logf("Clearing all sessions")
		d.store.ClearSessions()
		// Broadcast empty sessions list to all clients
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:    protocol.EventSessionsUpdated,
			Sessions: protocol.SessionsToValues(d.store.List("")),
		})

	case protocol.CmdPRVisited:
		visitedMsg := msg.(*protocol.PRVisitedMessage)
		d.logf("Marking PR %s as visited", visitedMsg.ID)
		d.store.MarkPRVisited(visitedMsg.ID)
		// Make all PRs from the same repo HOT so user sees fresh status
		// PR ID format: "owner/repo#number" → extract repo as "owner/repo"
		if idx := strings.LastIndex(visitedMsg.ID, "#"); idx > 0 {
			repo := visitedMsg.ID[:idx]
			for _, pr := range d.store.ListPRs("") {
				if pr.Repo == repo {
					d.store.SetPRHot(pr.ID)
					go d.fetchPRDetailsImmediate(pr.ID)
				}
			}
		} else {
			d.store.SetPRHot(visitedMsg.ID)
			go d.fetchPRDetailsImmediate(visitedMsg.ID)
		}
		d.broadcastPRs()

	case protocol.CmdListWorktrees:
		listMsg := msg.(*protocol.ListWorktreesMessage)
		d.logf("Listing worktrees for %s", listMsg.MainRepo)
		d.handleListWorktreesWS(client, listMsg)

	case protocol.CmdCreateWorktree:
		createMsg := msg.(*protocol.CreateWorktreeMessage)
		d.logf("Creating worktree %s in %s", createMsg.Branch, createMsg.MainRepo)
		d.handleCreateWorktreeWS(client, createMsg)

	case protocol.CmdDeleteWorktree:
		deleteMsg := msg.(*protocol.DeleteWorktreeMessage)
		d.logf("Deleting worktree %s", deleteMsg.Path)
		d.handleDeleteWorktreeWS(client, deleteMsg)

	case protocol.CmdGetSettings:
		d.logf("Getting settings")
		settings := make(map[string]interface{})
		for k, v := range d.store.GetAllSettings() {
			settings[k] = v
		}
		d.sendToClient(client, &protocol.WebSocketEvent{
			Event:    protocol.EventSettingsUpdated,
			Settings: settings,
		})

	case protocol.CmdSetSetting:
		setMsg := msg.(*protocol.SetSettingMessage)
		d.logf("Setting %s = %s", setMsg.Key, setMsg.Value)

		// Validate setting
		if err := d.validateSetting(setMsg.Key, setMsg.Value); err != nil {
			d.logf("Setting validation failed: %v", err)
			d.sendToClient(client, &protocol.WebSocketEvent{
				Event:   protocol.EventSettingsUpdated,
				Error:   protocol.Ptr(err.Error()),
				Success: protocol.Ptr(false),
			})
			return
		}

		d.store.SetSetting(setMsg.Key, setMsg.Value)
		d.broadcastSettings()

	case protocol.CmdUnregister:
		unregMsg := msg.(*protocol.UnregisterMessage)
		d.logf("Unregistering session %s via WebSocket", unregMsg.ID)
		d.store.Remove(unregMsg.ID)
		// Broadcast updated sessions list
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:    protocol.EventSessionsUpdated,
			Sessions: protocol.SessionsToValues(d.store.List("")),
		})

	case protocol.CmdGetRecentLocations:
		locMsg := msg.(*protocol.GetRecentLocationsMessage)
		limit := 20
		if locMsg.Limit != nil {
			limit = int(*locMsg.Limit)
		}
		d.logf("Getting recent locations (limit=%d)", limit)
		locations := d.store.GetRecentLocations(limit)
		d.sendToClient(client, &protocol.WebSocketEvent{
			Event:           protocol.EventRecentLocationsResult,
			RecentLocations: protocol.RecentLocationsToValues(locations),
			Success:         protocol.Ptr(true),
		})

	case protocol.CmdListBranches:
		listMsg := msg.(*protocol.ListBranchesMessage)
		d.logf("Listing branches for %s", listMsg.MainRepo)
		d.handleListBranchesWS(client, listMsg)

	case protocol.CmdDeleteBranch:
		deleteMsg := msg.(*protocol.DeleteBranchMessage)
		d.logf("Deleting branch %s (force=%v)", deleteMsg.Branch, deleteMsg.Force)
		d.handleDeleteBranchWS(client, deleteMsg)

	case protocol.CmdSwitchBranch:
		switchMsg := msg.(*protocol.SwitchBranchMessage)
		d.logf("Switching to branch %s in %s", switchMsg.Branch, switchMsg.MainRepo)
		d.handleSwitchBranchWS(client, switchMsg)

	case protocol.CmdCreateWorktreeFromBranch:
		createMsg := msg.(*protocol.CreateWorktreeFromBranchMessage)
		d.logf("Creating worktree from branch %s in %s", createMsg.Branch, createMsg.MainRepo)
		d.handleCreateWorktreeFromBranchWS(client, createMsg)

	case protocol.CmdCreateBranch:
		createMsg := msg.(*protocol.CreateBranchMessage)
		d.logf("Creating branch %s in %s", createMsg.Branch, createMsg.MainRepo)
		d.handleCreateBranchWS(client, createMsg)

	case protocol.CmdCheckDirty:
		d.logf("Checking dirty state for %s", msg.(*protocol.CheckDirtyMessage).Repo)
		d.handleCheckDirtyWS(client, msg.(*protocol.CheckDirtyMessage))
	case protocol.CmdStash:
		d.logf("Stashing changes in %s", msg.(*protocol.StashMessage).Repo)
		d.handleStashWS(client, msg.(*protocol.StashMessage))
	case protocol.CmdStashPop:
		d.logf("Popping stash in %s", msg.(*protocol.StashPopMessage).Repo)
		d.handleStashPopWS(client, msg.(*protocol.StashPopMessage))
	case protocol.CmdCheckAttnStash:
		d.logf("Checking for attn stash in %s for branch %s", msg.(*protocol.CheckAttnStashMessage).Repo, msg.(*protocol.CheckAttnStashMessage).Branch)
		d.handleCheckAttnStashWS(client, msg.(*protocol.CheckAttnStashMessage))
	case protocol.CmdCommitWIP:
		d.logf("Committing WIP in %s", msg.(*protocol.CommitWIPMessage).Repo)
		d.handleCommitWIPWS(client, msg.(*protocol.CommitWIPMessage))
	case protocol.CmdGetDefaultBranch:
		d.logf("Getting default branch for %s", msg.(*protocol.GetDefaultBranchMessage).Repo)
		d.handleGetDefaultBranchWS(client, msg.(*protocol.GetDefaultBranchMessage))
	case protocol.CmdFetchRemotes:
		d.logf("Fetching remotes for %s", msg.(*protocol.FetchRemotesMessage).Repo)
		d.handleFetchRemotesWS(client, msg.(*protocol.FetchRemotesMessage))
	case protocol.CmdListRemoteBranches:
		d.logf("Listing remote branches for %s", msg.(*protocol.ListRemoteBranchesMessage).Repo)
		d.handleListRemoteBranchesWS(client, msg.(*protocol.ListRemoteBranchesMessage))

	case protocol.CmdSubscribeGitStatus:
		subMsg := msg.(*protocol.SubscribeGitStatusMessage)
		d.logf("Subscribing to git status for %s", subMsg.Directory)
		d.handleSubscribeGitStatus(client, subMsg)

	case protocol.CmdUnsubscribeGitStatus:
		d.logf("Unsubscribing from git status")
		client.stopGitStatusPoll()

	case protocol.CmdGetFileDiff:
		diffMsg := msg.(*protocol.GetFileDiffMessage)
		d.logf("Getting file diff for %s in %s", diffMsg.Path, diffMsg.Directory)
		go d.handleGetFileDiff(client, diffMsg)
	}
}

// broadcastPRs sends updated PR list to all WebSocket clients
func (d *Daemon) broadcastPRs() {
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventPRsUpdated,
		Prs:   protocol.PRsToValues(d.store.ListPRs("")),
	})
}

// broadcastRepoStates sends updated repo states to all WebSocket clients
func (d *Daemon) broadcastRepoStates() {
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventReposUpdated,
		Repos: protocol.RepoStatesToValues(d.store.ListRepoStates()),
	})
}

// broadcastSettings sends updated settings to all WebSocket clients
func (d *Daemon) broadcastSettings() {
	settings := make(map[string]interface{})
	for k, v := range d.store.GetAllSettings() {
		settings[k] = v
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:    protocol.EventSettingsUpdated,
		Settings: settings,
	})
}

func (d *Daemon) sendToClient(client *wsClient, message interface{}) {
	data, err := json.Marshal(message)
	if err != nil {
		return
	}
	select {
	case client.send <- data:
	default:
		// Client buffer full, skip
	}
}

// validateSetting validates a setting key and value before storing
func (d *Daemon) validateSetting(key, value string) error {
	switch key {
	case SettingProjectsDirectory:
		return validateProjectsDirectory(value)
	default:
		return fmt.Errorf("unknown setting: %s", key)
	}
}

// validateProjectsDirectory ensures the path is valid and usable
func validateProjectsDirectory(path string) error {
	if path == "" {
		return fmt.Errorf("projects directory cannot be empty")
	}

	// Expand ~ to home directory
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot determine home directory: %w", err)
		}
		path = filepath.Join(home, path[2:])
	}

	// Must be absolute
	if !filepath.IsAbs(path) {
		return fmt.Errorf("projects directory must be an absolute path")
	}

	// Check if directory exists or can be created
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		// Try to create the directory
		if err := os.MkdirAll(path, 0755); err != nil {
			return fmt.Errorf("cannot create directory: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot access directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path exists but is not a directory")
	}

	return nil
}

func (d *Daemon) handleSubscribeGitStatus(client *wsClient, msg *protocol.SubscribeGitStatusMessage) {
	// Stop any existing subscription
	client.stopGitStatusPoll()

	client.gitStatusMu.Lock()
	client.gitStatusDir = msg.Directory
	client.gitStatusStop = make(chan struct{})
	client.gitStatusTicker = time.NewTicker(500 * time.Millisecond)
	stopChan := client.gitStatusStop
	ticker := client.gitStatusTicker
	client.gitStatusMu.Unlock()

	// Send immediate first update
	d.sendGitStatusUpdate(client)

	// Start polling goroutine
	go func() {
		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				d.sendGitStatusUpdate(client)
			}
		}
	}()
}

func (d *Daemon) sendGitStatusUpdate(client *wsClient) {
	client.gitStatusMu.Lock()
	dir := client.gitStatusDir
	lastHash := client.gitStatusHash
	client.gitStatusMu.Unlock()

	if dir == "" {
		return
	}

	status, err := getGitStatus(dir)
	if err != nil {
		d.logf("Git status error for %s: %v", dir, err)
		return
	}

	// Skip if unchanged
	newHash := hashGitStatus(status)
	if newHash == lastHash {
		return
	}

	client.gitStatusMu.Lock()
	client.gitStatusHash = newHash
	client.gitStatusMu.Unlock()

	d.sendToClient(client, status)
}

func (d *Daemon) handleGetFileDiff(client *wsClient, msg *protocol.GetFileDiffMessage) {
	result := protocol.FileDiffResultMessage{
		Event:     protocol.EventFileDiffResult,
		Directory: msg.Directory,
		Path:      msg.Path,
		Success:   false,
	}

	// Get original content from HEAD
	origCmd := exec.Command("git", "show", "HEAD:"+msg.Path)
	origCmd.Dir = msg.Directory
	origOutput, origErr := origCmd.Output()

	var original string
	if origErr == nil {
		original = string(origOutput)
	}
	// If error, file might be new - original is empty

	var modified string
	if msg.Staged != nil && *msg.Staged {
		// Get staged version
		stagedCmd := exec.Command("git", "show", ":"+msg.Path)
		stagedCmd.Dir = msg.Directory
		stagedOutput, err := stagedCmd.Output()
		if err != nil {
			result.Error = protocol.Ptr("Failed to read staged file: " + err.Error())
			d.sendToClient(client, result)
			return
		}
		modified = string(stagedOutput)
	} else {
		// Read current file from disk
		filePath := filepath.Join(msg.Directory, msg.Path)
		content, err := os.ReadFile(filePath)
		if err != nil {
			// File might be deleted
			if os.IsNotExist(err) {
				modified = ""
			} else {
				result.Error = protocol.Ptr("Failed to read file: " + err.Error())
				d.sendToClient(client, result)
				return
			}
		} else {
			modified = string(content)
		}
	}

	result.Original = original
	result.Modified = modified
	result.Success = true

	d.sendToClient(client, result)
}
