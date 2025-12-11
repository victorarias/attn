package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/claude-manager/internal/protocol"
)

// wsClient represents a connected WebSocket client
type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

// wsHub manages all WebSocket connections
type wsHub struct {
	clients    map[*wsClient]bool
	broadcast  chan []byte
	register   chan *wsClient
	unregister chan *wsClient
	mu         sync.RWMutex
}

func newWSHub() *wsHub {
	return &wsHub{
		clients:    make(map[*wsClient]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *wsClient),
		unregister: make(chan *wsClient),
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
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					// Client buffer full, skip
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Broadcast sends an event to all connected clients
func (h *wsHub) Broadcast(event *protocol.WebSocketEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	select {
	case h.broadcast <- data:
	default:
		// Broadcast channel full, drop message
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

	// Handle client lifecycle
	go d.wsWritePump(client)
	d.wsReadPump(client)
}

func (d *Daemon) sendInitialState(client *wsClient) {
	event := &protocol.WebSocketEvent{
		Event:           protocol.EventInitialState,
		ProtocolVersion: protocol.ProtocolVersion,
		Sessions:        d.store.List(""),
		PRs:             d.store.ListPRs(""),
		Repos:           d.store.ListRepoStates(),
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	select {
	case client.send <- data:
	default:
	}
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

func (d *Daemon) wsReadPump(client *wsClient) {
	defer func() {
		d.wsHub.unregister <- client
		client.conn.Close(websocket.StatusNormalClosure, "")
		d.logf("WebSocket client disconnected (%d remaining)", d.wsHub.ClientCount())
	}()

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		_, data, err := client.conn.Read(ctx)
		cancel()
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
	case protocol.MsgApprovePR:
		appMsg := msg.(*protocol.ApprovePRMessage)
		d.logf("Processing approve for %s#%d", appMsg.Repo, appMsg.Number)
		go func() {
			err := d.ghClient.ApprovePR(appMsg.Repo, appMsg.Number)
			result := protocol.PRActionResultMessage{
				Event:   protocol.MsgPRActionResult,
				Action:  "approve",
				Repo:    appMsg.Repo,
				Number:  appMsg.Number,
				Success: err == nil,
			}
			if err != nil {
				result.Error = err.Error()
				d.logf("Approve failed for %s#%d: %v", appMsg.Repo, appMsg.Number, err)
			} else {
				d.logf("Approve succeeded for %s#%d", appMsg.Repo, appMsg.Number)
			}
			d.sendToClient(client, result)
			d.logf("Sent approve result to client")
			// Trigger PR refresh after action
			d.RefreshPRs()
		}()

	case protocol.MsgMergePR:
		mergeMsg := msg.(*protocol.MergePRMessage)
		go func() {
			err := d.ghClient.MergePR(mergeMsg.Repo, mergeMsg.Number, mergeMsg.Method)
			result := protocol.PRActionResultMessage{
				Event:   protocol.MsgPRActionResult,
				Action:  "merge",
				Repo:    mergeMsg.Repo,
				Number:  mergeMsg.Number,
				Success: err == nil,
			}
			if err != nil {
				result.Error = err.Error()
			}
			d.sendToClient(client, result)
			// Trigger PR refresh after action
			d.RefreshPRs()
		}()

	case protocol.CmdMutePR:
		muteMsg := msg.(*protocol.MutePRMessage)
		d.store.ToggleMutePR(muteMsg.ID)
		d.broadcastPRs()

	case protocol.CmdMuteRepo:
		muteMsg := msg.(*protocol.MuteRepoMessage)
		d.store.ToggleMuteRepo(muteMsg.Repo)
		d.broadcastRepoStates()

	case protocol.CmdRefreshPRs:
		d.logf("Refreshing PRs on request")
		go d.RefreshPRs()
	}
}

// broadcastPRs sends updated PR list to all WebSocket clients
func (d *Daemon) broadcastPRs() {
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventPRsUpdated,
		PRs:   d.store.ListPRs(""),
	})
}

// broadcastRepoStates sends updated repo states to all WebSocket clients
func (d *Daemon) broadcastRepoStates() {
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventReposUpdated,
		Repos: d.store.ListRepoStates(),
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
