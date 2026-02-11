package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/store"
)

// Valid setting keys
const (
	SettingProjectsDirectory = "projects_directory"
	SettingUIScale           = "uiScale"
	SettingClaudeExecutable  = "claude_executable"
	SettingCodexExecutable   = "codex_executable"
	SettingCopilotExecutable = "copilot_executable"
	SettingEditorExecutable  = "editor_executable"
	SettingNewSessionAgent   = "new_session_agent"
	SettingClaudeAvailable   = "claude_available"
	SettingCodexAvailable    = "codex_available"
	SettingCopilotAvailable  = "copilot_available"
)

// wsClient represents a connected WebSocket client
type wsClient struct {
	conn      *websocket.Conn
	send      chan outboundMessage
	recv      chan []byte // incoming messages for ordered processing
	slowCount int         // tracks consecutive failed sends

	// PTY subscriptions keyed by session ID
	attachedSessions map[string]string // session -> subscriber id
	attachMu         sync.Mutex

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

type messageKind int

const (
	messageKindText messageKind = iota
)

type outboundMessage struct {
	kind    messageKind
	payload []byte
}

// wsHub manages all WebSocket connections
type wsHub struct {
	clients           map[*wsClient]bool
	broadcast         chan outboundMessage
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
		broadcast:  make(chan outboundMessage, 256),
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
	message := outboundMessage{kind: messageKindText, payload: data}
	select {
	case h.broadcast <- message:
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

func isAllowedWSOrigin(origin string) bool {
	if origin == "" {
		// Non-browser clients/tests may omit Origin.
		return true
	}
	allowedPrefixes := []string{
		"tauri://localhost",
		"http://tauri.localhost",
		"http://localhost",
		"http://127.0.0.1",
		"https://localhost",
		"https://127.0.0.1",
		"localhost:",
		"127.0.0.1:",
		"tauri.localhost",
	}
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(origin, prefix) {
			return true
		}
	}
	return false
}

// handleWS handles WebSocket connections
func (d *Daemon) handleWS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if !isAllowedWSOrigin(origin) {
		d.logf("WebSocket rejected origin: %s", origin)
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{
			"localhost",
			"localhost:*",
			"127.0.0.1",
			"127.0.0.1:*",
			"tauri.localhost",
			"tauri.localhost:*",
		},
	})
	if err != nil {
		d.logf("WebSocket accept error: %v", err)
		return
	}

	client := &wsClient{
		conn:             conn,
		send:             make(chan outboundMessage, 256),
		recv:             make(chan []byte, 256), // buffer for incoming messages
		attachedSessions: make(map[string]string),
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
	go d.wsMsgPump(client) // NEW: message processing goroutine
	d.wsReadPump(client)

	// Signal ping loop to stop when read pump exits
	close(done)
}

func (d *Daemon) sendInitialState(client *wsClient) {
	event := &protocol.WebSocketEvent{
		Event:           protocol.EventInitialState,
		ProtocolVersion: protocol.Ptr(protocol.ProtocolVersion),
		Sessions:        protocol.SessionsToValues(d.store.List("")),
		Prs:             protocol.PRsToValues(d.store.ListPRs("")),
		Repos:           protocol.RepoStatesToValues(d.store.ListRepoStates()),
		Authors:         protocol.AuthorStatesToValues(d.store.ListAuthorStates()),
		Settings:        d.settingsWithAgentAvailability(),
		Warnings:        d.getWarnings(),
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	_ = d.sendOutbound(client, outboundMessage{kind: messageKindText, payload: data})

	// Fetch details for all PRs in background (app launch)
	go d.fetchAllPRDetails()
}

func (d *Daemon) wsWritePump(client *wsClient) {
	defer func() {
		client.conn.Close(websocket.StatusNormalClosure, "")
	}()

	for message := range client.send {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		wsType := websocket.MessageText
		if message.kind != messageKindText {
			wsType = websocket.MessageBinary
		}
		err := client.conn.Write(ctx, wsType, message.payload)
		cancel()
		if err != nil {
			return
		}
	}
}

func (d *Daemon) sendOutbound(client *wsClient, message outboundMessage) bool {
	select {
	case client.send <- message:
		return true
	default:
		return false
	}
}

// wsMsgPump processes incoming messages in FIFO order
// This runs in a dedicated goroutine to avoid blocking the read loop
func (d *Daemon) wsMsgPump(client *wsClient) {
	for data := range client.recv {
		d.handleClientMessage(client, data)
	}
	d.logf("WebSocket message pump exited")
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
		d.detachAllSessions(client)
		close(client.recv) // signal wsMsgPump to exit
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

		// Enqueue for ordered processing (non-blocking with buffer)
		select {
		case client.recv <- data:
		default:
			d.logf("WebSocket client recv buffer full, dropping message")
		}
	}
}

func (d *Daemon) handleClientMessage(client *wsClient, data []byte) {
	cmd, msg, err := protocol.ParseMessage(data)
	if err != nil {
		var peek struct {
			Cmd string `json:"cmd"`
		}
		_ = json.Unmarshal(data, &peek)
		d.logf("WebSocket parse error for cmd=%s: %v", peek.Cmd, err)
		d.sendCommandError(client, peek.Cmd, err.Error())
		return
	}
	if shouldLogWSCommand(cmd) {
		d.logf("WebSocket parsed cmd: %s", cmd)
	}

	switch cmd {
	case protocol.CmdApprovePR:
		appMsg := msg.(*protocol.ApprovePRMessage)
		d.logf("Processing approve for %s", appMsg.ID)
		go func() {
			ghClient, repo, number, _, err := d.clientForPRID(appMsg.ID)
			if err == nil {
				err = ghClient.ApprovePR(repo, number)
			}
			result := protocol.PRActionResultMessage{
				Event:   protocol.EventPRActionResult,
				Action:  "approve",
				ID:      appMsg.ID,
				Success: err == nil,
			}
			if err != nil {
				result.Error = protocol.Ptr(err.Error())
				d.logf("Approve failed for %s: %v", appMsg.ID, err)
			} else {
				d.logf("Approve succeeded for %s", appMsg.ID)
				// Track approval interaction
				d.store.MarkPRApproved(appMsg.ID)
				d.store.SetPRHot(appMsg.ID)
				go d.fetchPRDetailsImmediate(appMsg.ID)
			}
			d.sendToClient(client, result)
			d.logf("Sent approve result to client")
			// Trigger PR refresh after action
			d.RefreshPRs()
		}()

	case protocol.CmdMergePR:
		mergeMsg := msg.(*protocol.MergePRMessage)
		go func() {
			ghClient, repo, number, _, err := d.clientForPRID(mergeMsg.ID)
			if err == nil {
				err = ghClient.MergePR(repo, number, mergeMsg.Method)
			}
			result := protocol.PRActionResultMessage{
				Event:   protocol.EventPRActionResult,
				Action:  "merge",
				ID:      mergeMsg.ID,
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

	case protocol.CmdMuteAuthor:
		muteMsg := msg.(*protocol.MuteAuthorMessage)
		d.store.ToggleMuteAuthor(muteMsg.Author)
		d.broadcastAuthorStates()

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

	case protocol.CmdFetchPRDetails:
		d.logf("Fetching PR details")
		fetchMsg := msg.(*protocol.FetchPRDetailsMessage)
		go func() {
			updatedPRs, err := d.fetchPRDetailsForID(fetchMsg.ID)
			result := protocol.WebSocketEvent{
				Event:   protocol.EventFetchPRDetailsResult,
				Success: protocol.Ptr(err == nil),
			}
			if err != nil {
				result.Error = protocol.Ptr(err.Error())
				d.logf("Fetch PR details failed: %v", err)
			} else {
				result.Prs = protocol.PRsToValues(updatedPRs)
				d.broadcastPRs()
				d.logf("Fetch PR details succeeded")
			}
			d.sendToClient(client, result)
		}()

	case protocol.CmdClearSessions:
		d.logf("Clearing all sessions")
		d.store.ClearSessions()
		if d.ptyManager != nil {
			for _, sessionID := range d.ptyManager.SessionIDs() {
				d.terminateSession(sessionID, syscall.SIGTERM)
			}
		}
		// Broadcast empty sessions list to all clients
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:    protocol.EventSessionsUpdated,
			Sessions: protocol.SessionsToValues(d.store.List("")),
		})

	case protocol.CmdClearWarnings:
		d.logf("Clearing daemon warnings")
		d.clearWarnings()

	case protocol.CmdPRVisited:
		visitedMsg := msg.(*protocol.PRVisitedMessage)
		d.logf("Marking PR %s as visited", visitedMsg.ID)
		d.store.MarkPRVisited(visitedMsg.ID)
		// Make all PRs from the same repo HOT so user sees fresh status
		if _, repo, _, err := protocol.ParsePRID(visitedMsg.ID); err == nil {
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
		d.sendToClient(client, &protocol.WebSocketEvent{
			Event:    protocol.EventSettingsUpdated,
			Settings: d.settingsWithAgentAvailability(),
		})

	case protocol.CmdSetSetting:
		setMsg := msg.(*protocol.SetSettingMessage)
		d.logf("Setting %s = %s", setMsg.Key, setMsg.Value)

		// Validate setting
		if err := d.validateSetting(setMsg.Key, setMsg.Value); err != nil {
			d.logf("Setting validation failed: %v", err)
			d.sendToClient(client, &protocol.WebSocketEvent{
				Event:    protocol.EventSettingsUpdated,
				Settings: d.settingsWithAgentAvailability(),
				Error:    protocol.Ptr(err.Error()),
				Success:  protocol.Ptr(false),
			})
			return
		}

		d.store.SetSetting(setMsg.Key, setMsg.Value)
		d.broadcastSettings()

	case protocol.CmdUnregister:
		unregMsg := msg.(*protocol.UnregisterMessage)
		d.logf("Unregistering session %s via WebSocket", unregMsg.ID)
		d.detachSession(client, unregMsg.ID)
		session := d.store.Get(unregMsg.ID)
		d.terminateSession(unregMsg.ID, syscall.SIGTERM)
		d.store.Remove(unregMsg.ID)
		if session != nil {
			d.wsHub.Broadcast(&protocol.WebSocketEvent{
				Event:   protocol.EventSessionUnregistered,
				Session: session,
			})
		}
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

	case protocol.CmdEnsureRepo:
		ensureMsg := msg.(*protocol.EnsureRepoMessage)
		d.logf("Ensuring repo at %s from %s", ensureMsg.TargetPath, ensureMsg.CloneURL)
		d.handleEnsureRepoWS(client, ensureMsg)

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

	case protocol.CmdGetBranchDiffFiles:
		diffMsg := msg.(*protocol.GetBranchDiffFilesMessage)
		d.logf("Getting branch diff files for %s", diffMsg.Directory)
		go d.handleGetBranchDiffFiles(client, diffMsg)

	case protocol.CmdGetRepoInfo:
		repoMsg := msg.(*protocol.GetRepoInfoMessage)
		d.logf("Getting repo info for %s", repoMsg.Repo)
		d.handleGetRepoInfoWS(client, repoMsg)

	case protocol.CmdGetReviewState:
		reviewMsg := msg.(*protocol.GetReviewStateMessage)
		d.logf("Getting review state for %s branch %s", reviewMsg.RepoPath, reviewMsg.Branch)
		d.handleGetReviewState(client, reviewMsg)

	case protocol.CmdMarkFileViewed:
		viewedMsg := msg.(*protocol.MarkFileViewedMessage)
		d.logf("Marking file %s viewed=%v in review %s", viewedMsg.Filepath, viewedMsg.Viewed, viewedMsg.ReviewID)
		d.handleMarkFileViewed(client, viewedMsg)

	case protocol.CmdAddComment:
		commentMsg := msg.(*protocol.AddCommentMessage)
		d.logf("Adding comment to review %s file %s", commentMsg.ReviewID, commentMsg.Filepath)
		d.handleAddComment(client, commentMsg)

	case protocol.CmdUpdateComment:
		commentMsg := msg.(*protocol.UpdateCommentMessage)
		d.logf("Updating comment %s", commentMsg.CommentID)
		d.handleUpdateComment(client, commentMsg)

	case protocol.CmdResolveComment:
		commentMsg := msg.(*protocol.ResolveCommentMessage)
		d.logf("Resolving comment %s resolved=%v", commentMsg.CommentID, commentMsg.Resolved)
		d.handleResolveComment(client, commentMsg)

	case protocol.CmdWontFixComment:
		commentMsg := msg.(*protocol.WontFixCommentMessage)
		d.logf("Marking comment %s wont_fix=%v", commentMsg.CommentID, commentMsg.WontFix)
		d.handleWontFixComment(client, commentMsg)

	case protocol.CmdDeleteComment:
		commentMsg := msg.(*protocol.DeleteCommentMessage)
		d.logf("Deleting comment %s", commentMsg.CommentID)
		d.handleDeleteComment(client, commentMsg)

	case protocol.CmdGetComments:
		commentMsg := msg.(*protocol.GetCommentsMessage)
		d.logf("Getting comments for review %s", commentMsg.ReviewID)
		d.handleGetComments(client, commentMsg)

	case protocol.CmdStartReview:
		reviewMsg := msg.(*protocol.StartReviewMessage)
		d.logf("Starting review for %s branch %s", reviewMsg.RepoPath, reviewMsg.Branch)
		d.handleStartReview(client, reviewMsg)

	case protocol.CmdCancelReview:
		cancelMsg := msg.(*protocol.CancelReviewMessage)
		d.logf("Cancelling review %s", cancelMsg.ReviewID)
		d.handleCancelReview(client, cancelMsg)

	case protocol.CmdSpawnSession:
		spawnMsg := msg.(*protocol.SpawnSessionMessage)
		d.handleSpawnSession(client, spawnMsg)

	case protocol.CmdAttachSession:
		attachMsg := msg.(*protocol.AttachSessionMessage)
		d.handleAttachSession(client, attachMsg)

	case protocol.CmdDetachSession:
		detachMsg := msg.(*protocol.DetachSessionMessage)
		d.detachSession(client, detachMsg.ID)

	case protocol.CmdPtyInput:
		inputMsg := msg.(*protocol.PtyInputMessage)
		if err := d.ptyManager.Input(inputMsg.ID, []byte(inputMsg.Data)); err != nil {
			if shouldLogPtyCommandError(err) {
				d.logf("pty_input failed for %s: %v", inputMsg.ID, err)
			}
		}

	case protocol.CmdPtyResize:
		resizeMsg := msg.(*protocol.PtyResizeMessage)
		d.handlePtyResize(client, resizeMsg)

	case protocol.CmdKillSession:
		killMsg := msg.(*protocol.KillSessionMessage)
		d.handleKillSession(client, killMsg)

	default:
		d.sendCommandError(client, cmd, "unsupported command")
	}
}

func (d *Daemon) sendCommandError(client *wsClient, cmd, errMsg string) {
	event := &protocol.WebSocketEvent{
		Event:   protocol.EventCommandError,
		Cmd:     protocol.Ptr(cmd),
		Success: protocol.Ptr(false),
		Error:   protocol.Ptr(errMsg),
	}
	d.sendToClient(client, event)
}

func wsSubscriberID(client *wsClient, sessionID string) string {
	return fmt.Sprintf("%p:%s", client, sessionID)
}

func (d *Daemon) detachSession(client *wsClient, sessionID string) {
	client.attachMu.Lock()
	subID, ok := client.attachedSessions[sessionID]
	if ok {
		delete(client.attachedSessions, sessionID)
	}
	client.attachMu.Unlock()
	if ok {
		d.ptyManager.Detach(sessionID, subID)
	}
}

func (d *Daemon) detachAllSessions(client *wsClient) {
	client.attachMu.Lock()
	attached := make(map[string]string, len(client.attachedSessions))
	for sessionID, subID := range client.attachedSessions {
		attached[sessionID] = subID
	}
	client.attachedSessions = make(map[string]string)
	client.attachMu.Unlock()

	for sessionID, subID := range attached {
		d.ptyManager.Detach(sessionID, subID)
	}
}

func (d *Daemon) handleSpawnSession(client *wsClient, msg *protocol.SpawnSessionMessage) {
	agent := protocol.NormalizeSpawnAgent(msg.Agent, string(protocol.SessionAgentCodex))
	isShell := agent == protocol.AgentShellValue
	spawnStartedAt := time.Now()
	label := protocol.Deref(msg.Label)
	if label == "" {
		label = filepath.Base(msg.Cwd)
	}

	spawnOpts := pty.SpawnOptions{
		ID:                msg.ID,
		CWD:               msg.Cwd,
		Agent:             agent,
		Label:             label,
		Cols:              uint16(msg.Cols),
		Rows:              uint16(msg.Rows),
		ResumeSessionID:   protocol.Deref(msg.ResumeSessionID),
		ResumePicker:      protocol.Deref(msg.ResumePicker),
		ForkSession:       protocol.Deref(msg.ForkSession),
		ClaudeExecutable:  protocol.Deref(msg.ClaudeExecutable),
		CodexExecutable:   protocol.Deref(msg.CodexExecutable),
		CopilotExecutable: protocol.Deref(msg.CopilotExecutable),
	}

	if err := d.ptyManager.Spawn(spawnOpts); err != nil {
		d.sendToClient(client, protocol.SpawnResultMessage{
			Event:   protocol.EventSpawnResult,
			ID:      msg.ID,
			Success: false,
			Error:   protocol.Ptr(err.Error()),
		})
		return
	}

	if !isShell {
		existing := d.store.Get(msg.ID)
		branchInfo, _ := git.GetBranchInfo(msg.Cwd)
		nowStr := string(protocol.TimestampNow())
		session := &protocol.Session{
			ID:             msg.ID,
			Label:          label,
			Agent:          protocol.SessionAgent(agent),
			Directory:      msg.Cwd,
			State:          protocol.SessionStateLaunching,
			StateSince:     nowStr,
			StateUpdatedAt: nowStr,
			LastSeen:       nowStr,
		}
		if branchInfo != nil {
			if branchInfo.Branch != "" {
				session.Branch = protocol.Ptr(branchInfo.Branch)
			}
			if branchInfo.IsWorktree {
				session.IsWorktree = protocol.Ptr(true)
			}
			if branchInfo.MainRepo != "" {
				session.MainRepo = protocol.Ptr(branchInfo.MainRepo)
			}
		}
		d.store.Add(session)
		d.startTranscriptWatcher(session.ID, session.Agent, session.Directory, spawnStartedAt)
		d.store.UpsertRecentLocation(msg.Cwd, label)
		eventType := protocol.EventSessionRegistered
		if existing != nil {
			eventType = protocol.EventSessionStateChanged
		}
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:   eventType,
			Session: session,
		})
	}

	d.sendToClient(client, protocol.SpawnResultMessage{
		Event:   protocol.EventSpawnResult,
		ID:      msg.ID,
		Success: true,
	})
}

func (d *Daemon) handleAttachSession(client *wsClient, msg *protocol.AttachSessionMessage) {
	subID := wsSubscriberID(client, msg.ID)

	info, err := d.ptyManager.Attach(
		msg.ID,
		subID,
		func(data []byte, seq uint32) bool {
			encoded := base64.StdEncoding.EncodeToString(data)
			event := &protocol.WebSocketEvent{
				Event: protocol.EventPtyOutput,
				ID:    protocol.Ptr(msg.ID),
				Data:  protocol.Ptr(encoded),
				Seq:   protocol.Ptr(int(seq)),
			}
			payload, marshalErr := json.Marshal(event)
			if marshalErr != nil {
				return true
			}
			return d.sendOutbound(client, outboundMessage{
				kind:    messageKindText,
				payload: payload,
			})
		},
		func(reason string) {
			event := &protocol.WebSocketEvent{
				Event:  protocol.EventPtyDesync,
				ID:     protocol.Ptr(msg.ID),
				Reason: protocol.Ptr(reason),
			}
			payload, marshalErr := json.Marshal(event)
			if marshalErr != nil {
				return
			}
			_ = d.sendOutbound(client, outboundMessage{
				kind:    messageKindText,
				payload: payload,
			})
		},
	)
	if err != nil {
		d.sendToClient(client, protocol.AttachResultMessage{
			Event:   protocol.EventAttachResult,
			ID:      msg.ID,
			Success: false,
			Error:   protocol.Ptr(err.Error()),
		})
		return
	}
	d.logf(
		"PTY attach result: id=%s running=%v last_seq=%d scrollback_bytes=%d snapshot_bytes=%d snapshot_fresh=%v size=%dx%d screen=%dx%d",
		msg.ID,
		info.Running,
		info.LastSeq,
		len(info.Scrollback),
		len(info.ScreenSnapshot),
		info.ScreenSnapshotFresh,
		info.Cols,
		info.Rows,
		info.ScreenCols,
		info.ScreenRows,
	)

	client.attachMu.Lock()
	client.attachedSessions[msg.ID] = subID
	client.attachMu.Unlock()

	result := protocol.AttachResultMessage{
		Event:               protocol.EventAttachResult,
		ID:                  msg.ID,
		Success:             true,
		ScrollbackTruncated: protocol.Ptr(info.ScrollbackTruncated),
		LastSeq:             protocol.Ptr(int(info.LastSeq)),
		Cols:                protocol.Ptr(int(info.Cols)),
		Rows:                protocol.Ptr(int(info.Rows)),
		Pid:                 protocol.Ptr(info.PID),
		Running:             protocol.Ptr(info.Running),
	}
	if len(info.Scrollback) > 0 {
		encoded := base64.StdEncoding.EncodeToString(info.Scrollback)
		result.Scrollback = protocol.Ptr(encoded)
	}
	if len(info.ScreenSnapshot) > 0 {
		encoded := base64.StdEncoding.EncodeToString(info.ScreenSnapshot)
		result.ScreenSnapshot = protocol.Ptr(encoded)
		result.ScreenRows = protocol.Ptr(int(info.ScreenRows))
		result.ScreenCols = protocol.Ptr(int(info.ScreenCols))
		result.ScreenCursorX = protocol.Ptr(int(info.ScreenCursorX))
		result.ScreenCursorY = protocol.Ptr(int(info.ScreenCursorY))
		result.ScreenCursorVisible = protocol.Ptr(info.ScreenCursorVisible)
		result.ScreenSnapshotFresh = protocol.Ptr(info.ScreenSnapshotFresh)
	}
	d.sendToClient(client, result)
}

func (d *Daemon) handlePtyResize(_ *wsClient, msg *protocol.PtyResizeMessage) {
	if msg.Cols <= 0 || msg.Rows <= 0 {
		return
	}
	d.logf("pty_resize: id=%s cols=%d rows=%d", msg.ID, msg.Cols, msg.Rows)
	if err := d.ptyManager.Resize(msg.ID, uint16(msg.Cols), uint16(msg.Rows)); err != nil {
		if shouldLogPtyCommandError(err) {
			d.logf("pty_resize failed for %s: %v", msg.ID, err)
		}
	}
}

func parseSignal(name string) syscall.Signal {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "", "SIGTERM", "TERM":
		return syscall.SIGTERM
	case "SIGINT", "INT":
		return syscall.SIGINT
	case "SIGHUP", "HUP":
		return syscall.SIGHUP
	case "SIGKILL", "KILL":
		return syscall.SIGKILL
	default:
		return syscall.SIGTERM
	}
}

func (d *Daemon) handleKillSession(client *wsClient, msg *protocol.KillSessionMessage) {
	d.detachSession(client, msg.ID)
	sig := parseSignal(protocol.Deref(msg.Signal))
	if err := d.ptyManager.Kill(msg.ID, sig); err != nil {
		if shouldLogPtyCommandError(err) {
			d.logf("kill_session failed for %s: %v", msg.ID, err)
		}
	}
}

func shouldLogWSCommand(cmd string) bool {
	switch cmd {
	case protocol.CmdPtyInput:
		return false
	default:
		return true
	}
}

func shouldLogPtyCommandError(err error) bool {
	// Session-not-found can happen during normal UI race windows (resize/input before spawn/attach).
	return !errors.Is(err, pty.ErrSessionNotFound)
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

// broadcastAuthorStates sends updated author states to all WebSocket clients
func (d *Daemon) broadcastAuthorStates() {
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   protocol.EventAuthorsUpdated,
		Authors: protocol.AuthorStatesToValues(d.store.ListAuthorStates()),
	})
}

// broadcastSettings sends updated settings to all WebSocket clients
func (d *Daemon) broadcastSettings() {
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:    protocol.EventSettingsUpdated,
		Settings: d.settingsWithAgentAvailability(),
	})
}

func (d *Daemon) settingsWithAgentAvailability() map[string]interface{} {
	stored := d.store.GetAllSettings()
	settings := make(map[string]interface{}, len(stored)+3)
	for k, v := range stored {
		settings[k] = v
	}
	settings[SettingClaudeAvailable] = strconv.FormatBool(isAgentExecutableAvailable(stored[SettingClaudeExecutable], "claude"))
	settings[SettingCodexAvailable] = strconv.FormatBool(isAgentExecutableAvailable(stored[SettingCodexExecutable], "codex"))
	settings[SettingCopilotAvailable] = strconv.FormatBool(isAgentExecutableAvailable(stored[SettingCopilotExecutable], "copilot"))
	return settings
}

func isAgentExecutableAvailable(configuredExecutable, defaultExecutable string) bool {
	executable := strings.TrimSpace(configuredExecutable)
	if executable == "" {
		executable = defaultExecutable
	}
	_, err := exec.LookPath(executable)
	return err == nil
}

func (d *Daemon) sendToClient(client *wsClient, message interface{}) {
	data, err := json.Marshal(message)
	if err != nil {
		return
	}
	_ = d.sendOutbound(client, outboundMessage{
		kind:    messageKindText,
		payload: data,
	})
}

// validateSetting validates a setting key and value before storing
func (d *Daemon) validateSetting(key, value string) error {
	switch key {
	case SettingProjectsDirectory:
		return validateProjectsDirectory(value)
	case SettingUIScale:
		return validateUIScale(value)
	case SettingClaudeExecutable, SettingCodexExecutable, SettingCopilotExecutable:
		return validateExecutableSetting(value)
	case SettingEditorExecutable:
		return validateEditorSetting(value)
	case SettingNewSessionAgent:
		return validateNewSessionAgent(value)
	default:
		return fmt.Errorf("unknown setting: %s", key)
	}
}

// validateUIScale ensures the scale value is a valid float within range
func validateUIScale(value string) error {
	scale, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("invalid scale value: %s", value)
	}
	if scale < 0.5 || scale > 2.0 {
		return fmt.Errorf("scale must be between 0.5 and 2.0")
	}
	return nil
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

func validateExecutableSetting(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	path, err := exec.LookPath(value)
	if err != nil {
		return fmt.Errorf("executable not found: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access executable: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("executable path points to a directory")
	}

	return nil
}

func validateEditorSetting(value string) error {
	editor := strings.TrimSpace(value)
	if editor == "" {
		return nil
	}

	binary := extractCommandBinary(editor)
	if binary == "" {
		return fmt.Errorf("invalid editor command")
	}

	path, err := exec.LookPath(binary)
	if err != nil {
		return fmt.Errorf("executable not found: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access executable: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("executable path points to a directory")
	}

	return nil
}

func validateNewSessionAgent(value string) error {
	agent := strings.TrimSpace(value)
	if agent == "" {
		return nil
	}
	lower := strings.ToLower(agent)
	if lower != "codex" && lower != "claude" && lower != "copilot" {
		return fmt.Errorf("unknown agent: %s", value)
	}
	return nil
}

func extractCommandBinary(command string) string {
	if command == "" {
		return ""
	}
	if command[0] == '"' || command[0] == '\'' {
		quote := command[0]
		for i := 1; i < len(command); i++ {
			if command[i] == quote {
				return command[1:i]
			}
		}
		return ""
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
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

	// Determine the ref to compare against
	// If base_ref is provided, use it (for PR-like branch diffs)
	// Otherwise, use HEAD (traditional behavior)
	baseRef := "HEAD"
	if msg.BaseRef != nil && *msg.BaseRef != "" {
		baseRef = *msg.BaseRef
	}

	// Get original content from base ref
	origCmd := exec.Command("git", "show", baseRef+":"+msg.Path)
	origCmd.Dir = msg.Directory
	origOutput, origErr := origCmd.Output()

	var original string
	if origErr == nil {
		original = string(origOutput)
	}
	// If error, file might be new - original is empty

	var modified string
	if msg.Staged != nil && *msg.Staged {
		// Get staged version (deprecated, kept for backward compatibility)
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
		// Read current file from disk (includes both committed and uncommitted changes)
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

func (d *Daemon) handleGetBranchDiffFiles(client *wsClient, msg *protocol.GetBranchDiffFilesMessage) {
	result := protocol.BranchDiffFilesResultMessage{
		Event:     protocol.EventBranchDiffFilesResult,
		Directory: msg.Directory,
		Success:   false,
	}

	// Determine base ref - use provided or default to origin/<default-branch>
	baseRef := ""
	if msg.BaseRef != nil && *msg.BaseRef != "" {
		baseRef = *msg.BaseRef
	} else {
		// Get the default branch
		defaultBranch, err := git.GetDefaultBranch(msg.Directory)
		if err != nil {
			result.Error = protocol.Ptr("Failed to get default branch: " + err.Error())
			d.sendToClient(client, result)
			return
		}
		baseRef = "origin/" + defaultBranch
	}
	result.BaseRef = baseRef

	// Get the branch diff files
	files, err := git.GetBranchDiffFiles(msg.Directory, baseRef)
	if err != nil {
		result.Error = protocol.Ptr("Failed to get branch diff: " + err.Error())
		d.sendToClient(client, result)
		return
	}

	// Convert to protocol types
	protoFiles := make([]protocol.BranchDiffFile, len(files))
	for i, f := range files {
		protoFiles[i] = protocol.BranchDiffFile{
			Path:   f.Path,
			Status: f.Status,
		}
		if f.OldPath != "" {
			protoFiles[i].OldPath = &f.OldPath
		}
		if f.Additions > 0 {
			protoFiles[i].Additions = &f.Additions
		}
		if f.Deletions > 0 {
			protoFiles[i].Deletions = &f.Deletions
		}
		if f.HasUncommitted {
			protoFiles[i].HasUncommitted = &f.HasUncommitted
		}
	}

	result.Files = protoFiles
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleGetReviewState(client *wsClient, msg *protocol.GetReviewStateMessage) {
	result := protocol.GetReviewStateResultMessage{
		Event:   protocol.EventGetReviewStateResult,
		Success: false,
	}

	review, err := d.store.GetOrCreateReview(msg.RepoPath, msg.Branch)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	viewedFiles, err := d.store.GetViewedFiles(review.ID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.State = &protocol.ReviewState{
		ReviewID:    review.ID,
		RepoPath:    review.RepoPath,
		Branch:      review.Branch,
		ViewedFiles: viewedFiles,
	}
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleMarkFileViewed(client *wsClient, msg *protocol.MarkFileViewedMessage) {
	result := protocol.MarkFileViewedResultMessage{
		Event:    protocol.EventMarkFileViewedResult,
		ReviewID: msg.ReviewID,
		Filepath: msg.Filepath,
		Viewed:   msg.Viewed,
		Success:  false,
	}

	var err error
	if msg.Viewed {
		err = d.store.MarkFileViewed(msg.ReviewID, msg.Filepath)
	} else {
		err = d.store.UnmarkFileViewed(msg.ReviewID, msg.Filepath)
	}

	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleAddComment(client *wsClient, msg *protocol.AddCommentMessage) {
	result := protocol.AddCommentResultMessage{
		Event:   protocol.EventAddCommentResult,
		Success: false,
	}

	comment, err := d.store.AddComment(msg.ReviewID, msg.Filepath, int(msg.LineStart), int(msg.LineEnd), msg.Content, "user")
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	result.Comment = &protocol.ReviewComment{
		ID:        comment.ID,
		ReviewID:  comment.ReviewID,
		Filepath:  comment.Filepath,
		LineStart: int(comment.LineStart),
		LineEnd:   int(comment.LineEnd),
		Content:   comment.Content,
		Author:    comment.Author,
		Resolved:  comment.Resolved,
		CreatedAt: comment.CreatedAt.Format(time.RFC3339),
	}
	d.sendToClient(client, result)
}

func (d *Daemon) handleUpdateComment(client *wsClient, msg *protocol.UpdateCommentMessage) {
	result := protocol.UpdateCommentResultMessage{
		Event:   protocol.EventUpdateCommentResult,
		Success: false,
	}

	err := d.store.UpdateComment(msg.CommentID, msg.Content)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleResolveComment(client *wsClient, msg *protocol.ResolveCommentMessage) {
	result := protocol.ResolveCommentResultMessage{
		Event:   protocol.EventResolveCommentResult,
		Success: false,
	}

	// When resolving from the UI, the user is the resolver
	resolvedBy := ""
	if msg.Resolved {
		resolvedBy = "user"
	}
	err := d.store.ResolveComment(msg.CommentID, msg.Resolved, resolvedBy)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleWontFixComment(client *wsClient, msg *protocol.WontFixCommentMessage) {
	result := protocol.WontFixCommentResultMessage{
		Event:   protocol.EventWontFixCommentResult,
		Success: false,
	}

	// When marking as wont_fix from the UI, the user is the marker
	wontFixBy := ""
	if msg.WontFix {
		wontFixBy = "user"
	}
	err := d.store.WontFixComment(msg.CommentID, msg.WontFix, wontFixBy)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleDeleteComment(client *wsClient, msg *protocol.DeleteCommentMessage) {
	result := protocol.DeleteCommentResultMessage{
		Event:   protocol.EventDeleteCommentResult,
		Success: false,
	}

	err := d.store.DeleteComment(msg.CommentID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleGetComments(client *wsClient, msg *protocol.GetCommentsMessage) {
	result := protocol.GetCommentsResultMessage{
		Event:   protocol.EventGetCommentsResult,
		Success: false,
	}

	var comments []*store.ReviewComment
	var err error

	if msg.Filepath != nil && *msg.Filepath != "" {
		comments, err = d.store.GetCommentsForFile(msg.ReviewID, *msg.Filepath)
	} else {
		comments, err = d.store.GetComments(msg.ReviewID)
	}

	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	result.Comments = make([]protocol.ReviewComment, len(comments))
	for i, c := range comments {
		result.Comments[i] = protocol.ReviewComment{
			ID:        c.ID,
			ReviewID:  c.ReviewID,
			Filepath:  c.Filepath,
			LineStart: int(c.LineStart),
			LineEnd:   int(c.LineEnd),
			Content:   c.Content,
			Author:    c.Author,
			Resolved:  c.Resolved,
			CreatedAt: c.CreatedAt.Format(time.RFC3339),
		}
	}
	d.sendToClient(client, result)
}
