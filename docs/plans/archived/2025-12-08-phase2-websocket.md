# Phase 2: Go Daemon WebSocket Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add WebSocket endpoint to Go daemon so Tauri app can receive real-time session and PR updates.

**Architecture:** Go daemon adds HTTP server on port 9849 with `/ws` WebSocket endpoint alongside existing Unix socket. Daemon broadcasts events when state changes. React frontend connects via WebSocket and updates sidebar with real data.

**Tech Stack:** Go (nhooyr.io/websocket), React (native WebSocket API), Zustand (state management)

**References:**
- [nhooyr.io/websocket docs](https://pkg.go.dev/nhooyr.io/websocket)
- Current daemon: `internal/daemon/daemon.go`
- Current protocol: `internal/protocol/types.go`

---

## Task 1: Add WebSocket Event Types to Protocol

**Files:**
- Modify: `internal/protocol/types.go`

**Step 1: Add event type constants**

Add after line 23 (after `CmdFetchPRDetails`):

```go
// WebSocket Events (daemon -> client)
const (
	EventSessionRegistered   = "session_registered"
	EventSessionUnregistered = "session_unregistered"
	EventSessionStateChanged = "session_state_changed"
	EventSessionTodosUpdated = "session_todos_updated"
	EventPRsUpdated          = "prs_updated"
	EventInitialState        = "initial_state"
)
```

**Step 2: Add WebSocket event message type**

Add after `Response` struct (around line 192):

```go
// WebSocketEvent is sent from daemon to connected WebSocket clients
type WebSocketEvent struct {
	Event    string     `json:"event"`
	Session  *Session   `json:"session,omitempty"`
	Sessions []*Session `json:"sessions,omitempty"`
	PRs      []*PR      `json:"prs,omitempty"`
}
```

**Step 3: Verify it compiles**

```bash
go build ./...
```

Expected: Build succeeds.

**Step 4: Commit**

```bash
git add internal/protocol/types.go
git commit -m "feat(protocol): add WebSocket event types"
```

---

## Task 2: Create WebSocket Server

**Files:**
- Create: `internal/daemon/websocket.go`

**Step 1: Install WebSocket dependency**

```bash
go get nhooyr.io/websocket
```

**Step 2: Create websocket.go**

```go
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
		Event:    protocol.EventInitialState,
		Sessions: d.store.List(""),
		PRs:      d.store.ListPRs(""),
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
		_, _, err := client.conn.Read(ctx)
		cancel()
		if err != nil {
			return
		}
		// We don't expect messages from client, just keep connection alive
	}
}
```

**Step 3: Verify it compiles**

```bash
go build ./...
```

Expected: Build succeeds.

**Step 4: Commit**

```bash
git add internal/daemon/websocket.go go.mod go.sum
git commit -m "feat(daemon): add WebSocket hub and client handling"
```

---

## Task 3: Integrate WebSocket into Daemon

**Files:**
- Modify: `internal/daemon/daemon.go`

**Step 1: Add wsHub field and HTTP server to Daemon struct**

Update struct (around line 17):

```go
// Daemon manages Claude sessions
type Daemon struct {
	socketPath string
	store      *store.Store
	listener   net.Listener
	httpServer *http.Server
	wsHub      *wsHub
	done       chan struct{}
	logger     *logging.Logger
	ghFetcher  *github.Fetcher
}
```

**Step 2: Update New() to create wsHub**

Update New function (around line 27):

```go
// New creates a new daemon
func New(socketPath string) *Daemon {
	logger, _ := logging.New(logging.DefaultLogPath())
	return &Daemon{
		socketPath: socketPath,
		store:      store.NewWithPersistence(store.DefaultStatePath()),
		wsHub:      newWSHub(),
		done:       make(chan struct{}),
		logger:     logger,
		ghFetcher:  github.NewFetcher(),
	}
}
```

**Step 3: Update NewForTesting to create wsHub**

Update NewForTesting (around line 39):

```go
// NewForTesting creates a daemon with a non-persistent store for tests
func NewForTesting(socketPath string) *Daemon {
	return &Daemon{
		socketPath: socketPath,
		store:      store.New(),
		wsHub:      newWSHub(),
		done:       make(chan struct{}),
		logger:     nil, // No logging in tests
	}
}
```

**Step 4: Add HTTP server start to Start()**

Add after `d.log("daemon started")` (around line 58), before the persistence goroutine:

```go
	// Start WebSocket hub
	go d.wsHub.run()

	// Start HTTP server for WebSocket
	go d.startHTTPServer()
```

**Step 5: Add startHTTPServer method**

Add after the Stop() method (around line 99):

```go
func (d *Daemon) startHTTPServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", d.handleWS)

	port := os.Getenv("CM_WS_PORT")
	if port == "" {
		port = "9849"
	}

	d.httpServer = &http.Server{
		Addr:    "127.0.0.1:" + port,
		Handler: mux,
	}

	d.logf("WebSocket server starting on ws://127.0.0.1:%s/ws", port)
	if err := d.httpServer.ListenAndServe(); err != http.ErrServerClosed {
		d.logf("HTTP server error: %v", err)
	}
}
```

**Step 6: Update Stop() to shutdown HTTP server**

Update Stop method to include HTTP server shutdown:

```go
// Stop stops the daemon
func (d *Daemon) Stop() {
	d.log("daemon stopping")
	close(d.done)
	if d.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.httpServer.Shutdown(ctx)
	}
	if d.listener != nil {
		d.listener.Close()
	}
	os.Remove(d.socketPath)
	if d.logger != nil {
		d.logger.Close()
	}
}
```

**Step 7: Add import for "context"**

Add `"context"` to the import block at the top.

**Step 8: Verify it compiles**

```bash
go build ./...
```

Expected: Build succeeds.

**Step 9: Commit**

```bash
git add internal/daemon/daemon.go
git commit -m "feat(daemon): integrate WebSocket server into daemon lifecycle"
```

---

## Task 4: Broadcast Events on State Changes

**Files:**
- Modify: `internal/daemon/daemon.go`

**Step 1: Update handleRegister to broadcast**

Update handleRegister (around line 161):

```go
func (d *Daemon) handleRegister(conn net.Conn, msg *protocol.RegisterMessage) {
	session := &protocol.Session{
		ID:         msg.ID,
		Label:      msg.Label,
		Directory:  msg.Dir,
		TmuxTarget: msg.Tmux,
		State:      protocol.StateWaiting,
		StateSince: time.Now(),
		LastSeen:   time.Now(),
	}
	d.store.Add(session)
	d.sendOK(conn)

	// Broadcast to WebSocket clients
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   protocol.EventSessionRegistered,
		Session: session,
	})
}
```

**Step 2: Update handleUnregister to broadcast**

Update handleUnregister (around line 175):

```go
func (d *Daemon) handleUnregister(conn net.Conn, msg *protocol.UnregisterMessage) {
	// Get session before removing for broadcast
	sessions := d.store.List("")
	var session *protocol.Session
	for _, s := range sessions {
		if s.ID == msg.ID {
			session = s
			break
		}
	}

	d.store.Remove(msg.ID)
	d.sendOK(conn)

	// Broadcast to WebSocket clients
	if session != nil {
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:   protocol.EventSessionUnregistered,
			Session: session,
		})
	}
}
```

**Step 3: Update handleState to broadcast**

Update handleState (around line 180):

```go
func (d *Daemon) handleState(conn net.Conn, msg *protocol.StateMessage) {
	d.store.UpdateState(msg.ID, msg.State)
	d.store.Touch(msg.ID)
	d.sendOK(conn)

	// Broadcast to WebSocket clients
	sessions := d.store.List("")
	for _, s := range sessions {
		if s.ID == msg.ID {
			d.wsHub.Broadcast(&protocol.WebSocketEvent{
				Event:   protocol.EventSessionStateChanged,
				Session: s,
			})
			break
		}
	}
}
```

**Step 4: Update handleTodos to broadcast**

Update handleTodos (around line 186):

```go
func (d *Daemon) handleTodos(conn net.Conn, msg *protocol.TodosMessage) {
	d.store.UpdateTodos(msg.ID, msg.Todos)
	d.store.Touch(msg.ID)
	d.sendOK(conn)

	// Broadcast to WebSocket clients
	sessions := d.store.List("")
	for _, s := range sessions {
		if s.ID == msg.ID {
			d.wsHub.Broadcast(&protocol.WebSocketEvent{
				Event:   protocol.EventSessionTodosUpdated,
				Session: s,
			})
			break
		}
	}
}
```

**Step 5: Update doPRPoll to broadcast**

Update doPRPoll (around line 308):

```go
func (d *Daemon) doPRPoll() {
	prs, err := d.ghFetcher.FetchAll()
	if err != nil {
		d.logf("PR poll error: %v", err)
		return
	}

	d.store.SetPRs(prs)

	// Broadcast to WebSocket clients
	allPRs := d.store.ListPRs("")
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventPRsUpdated,
		PRs:   allPRs,
	})

	// Count waiting (non-muted) PRs for logging
	waiting := 0
	for _, pr := range allPRs {
		if pr.State == protocol.StateWaiting && !pr.Muted {
			waiting++
		}
	}
	d.logf("PR poll: %d PRs (%d waiting)", len(prs), waiting)
}
```

**Step 6: Verify it compiles**

```bash
go build ./...
```

Expected: Build succeeds.

**Step 7: Commit**

```bash
git add internal/daemon/daemon.go
git commit -m "feat(daemon): broadcast WebSocket events on state changes"
```

---

## Task 5: Test WebSocket Manually

**Step 1: Build and install cm**

```bash
make install
```

**Step 2: Stop any running daemon**

```bash
pkill -f "cm.*daemon" || true
```

**Step 3: Start daemon in foreground with debug logging**

```bash
DEBUG=debug cm -d
```

Expected: See "WebSocket server starting on ws://127.0.0.1:9849/ws"

**Step 4: Test WebSocket connection with websocat (if available) or browser console**

In browser console:
```javascript
ws = new WebSocket('ws://127.0.0.1:9849/ws');
ws.onmessage = (e) => console.log(JSON.parse(e.data));
```

Expected: Receive `initial_state` event with sessions and PRs arrays.

**Step 5: Start a cm session in another terminal**

```bash
cm -s test
```

Expected: WebSocket receives `session_registered` event.

**Step 6: Commit test confirmation**

No code changes, just verify it works.

---

## Task 6: Create Frontend WebSocket Hook

**Files:**
- Create: `app/src/hooks/useDaemonSocket.ts`

**Step 1: Create the hook**

```typescript
import { useEffect, useRef, useCallback } from 'react';

export interface DaemonSession {
  id: string;
  label: string;
  directory: string;
  tmux_target: string;
  state: 'working' | 'waiting';
  state_since: string;
  todos: string[] | null;
  last_seen: string;
  muted: boolean;
}

export interface DaemonPR {
  id: string;
  repo: string;
  number: number;
  title: string;
  url: string;
  role: 'author' | 'reviewer';
  state: 'working' | 'waiting';
  reason: string;
  last_updated: string;
  muted: boolean;
}

interface WebSocketEvent {
  event: string;
  session?: DaemonSession;
  sessions?: DaemonSession[];
  prs?: DaemonPR[];
}

interface UseDaemonSocketOptions {
  onSessionsUpdate: (sessions: DaemonSession[]) => void;
  onPRsUpdate: (prs: DaemonPR[]) => void;
  wsUrl?: string;
}

export function useDaemonSocket({
  onSessionsUpdate,
  onPRsUpdate,
  wsUrl = 'ws://127.0.0.1:9849/ws',
}: UseDaemonSocketOptions) {
  const wsRef = useRef<WebSocket | null>(null);
  const sessionsRef = useRef<DaemonSession[]>([]);
  const reconnectTimeoutRef = useRef<number | null>(null);

  const connect = useCallback(() => {
    if (wsRef.current?.readyState === WebSocket.OPEN) return;

    const ws = new WebSocket(wsUrl);

    ws.onopen = () => {
      console.log('[Daemon] WebSocket connected');
    };

    ws.onmessage = (event) => {
      try {
        const data: WebSocketEvent = JSON.parse(event.data);
        console.log('[Daemon] Event:', data.event);

        switch (data.event) {
          case 'initial_state':
            if (data.sessions) {
              sessionsRef.current = data.sessions;
              onSessionsUpdate(data.sessions);
            }
            if (data.prs) {
              onPRsUpdate(data.prs);
            }
            break;

          case 'session_registered':
            if (data.session) {
              sessionsRef.current = [...sessionsRef.current, data.session];
              onSessionsUpdate(sessionsRef.current);
            }
            break;

          case 'session_unregistered':
            if (data.session) {
              sessionsRef.current = sessionsRef.current.filter(
                (s) => s.id !== data.session!.id
              );
              onSessionsUpdate(sessionsRef.current);
            }
            break;

          case 'session_state_changed':
          case 'session_todos_updated':
            if (data.session) {
              sessionsRef.current = sessionsRef.current.map((s) =>
                s.id === data.session!.id ? data.session! : s
              );
              onSessionsUpdate(sessionsRef.current);
            }
            break;

          case 'prs_updated':
            if (data.prs) {
              onPRsUpdate(data.prs);
            }
            break;
        }
      } catch (err) {
        console.error('[Daemon] Parse error:', err);
      }
    };

    ws.onclose = () => {
      console.log('[Daemon] WebSocket disconnected, reconnecting in 3s...');
      wsRef.current = null;
      reconnectTimeoutRef.current = window.setTimeout(connect, 3000);
    };

    ws.onerror = (err) => {
      console.error('[Daemon] WebSocket error:', err);
      ws.close();
    };

    wsRef.current = ws;
  }, [wsUrl, onSessionsUpdate, onPRsUpdate]);

  useEffect(() => {
    connect();

    return () => {
      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current);
      }
      wsRef.current?.close();
    };
  }, [connect]);

  return {
    isConnected: wsRef.current?.readyState === WebSocket.OPEN,
  };
}
```

**Step 2: Verify TypeScript compiles**

```bash
cd app && pnpm build
```

Expected: Build succeeds.

**Step 3: Commit**

```bash
git add app/src/hooks/useDaemonSocket.ts
git commit -m "feat(app): add useDaemonSocket hook for daemon WebSocket connection"
```

---

## Task 7: Create Daemon Sessions Store

**Files:**
- Create: `app/src/store/daemonSessions.ts`

**Step 1: Create the store**

```typescript
import { create } from 'zustand';
import { DaemonSession, DaemonPR } from '../hooks/useDaemonSocket';

interface DaemonStore {
  // Sessions from daemon (cm-tracked sessions)
  daemonSessions: DaemonSession[];
  setDaemonSessions: (sessions: DaemonSession[]) => void;

  // PRs from daemon
  prs: DaemonPR[];
  setPRs: (prs: DaemonPR[]) => void;

  // Connection status
  isConnected: boolean;
  setConnected: (connected: boolean) => void;
}

export const useDaemonStore = create<DaemonStore>((set) => ({
  daemonSessions: [],
  setDaemonSessions: (sessions) => set({ daemonSessions: sessions }),

  prs: [],
  setPRs: (prs) => set({ prs }),

  isConnected: false,
  setConnected: (connected) => set({ isConnected: connected }),
}));
```

**Step 2: Verify TypeScript compiles**

```bash
cd app && pnpm build
```

Expected: Build succeeds.

**Step 3: Commit**

```bash
git add app/src/store/daemonSessions.ts
git commit -m "feat(app): add daemon sessions store for WebSocket data"
```

---

## Task 8: Update Sidebar to Show Daemon Sessions

**Files:**
- Modify: `app/src/components/Sidebar.tsx`
- Modify: `app/src/components/Sidebar.css`

**Step 1: Update Sidebar component**

Replace entire file:

```tsx
import { DaemonSession, DaemonPR } from '../hooks/useDaemonSocket';
import './Sidebar.css';

interface LocalSession {
  id: string;
  label: string;
  state: 'working' | 'waiting';
}

interface SidebarProps {
  // Local sessions (PTY sessions in this app)
  localSessions: LocalSession[];
  selectedId: string | null;
  onSelectSession: (id: string) => void;
  onNewSession: () => void;
  onCloseSession: (id: string) => void;
  // Daemon sessions (from cm daemon via WebSocket)
  daemonSessions: DaemonSession[];
  // PRs
  prs: DaemonPR[];
  isConnected: boolean;
}

export function Sidebar({
  localSessions,
  selectedId,
  onSelectSession,
  onNewSession,
  onCloseSession,
  daemonSessions,
  prs,
  isConnected,
}: SidebarProps) {
  // Filter PRs that need attention (waiting and not muted)
  const waitingPRs = prs.filter((pr) => pr.state === 'waiting' && !pr.muted);

  return (
    <div className="sidebar">
      {/* Local Sessions */}
      <div className="sidebar-section">
        <div className="sidebar-header">
          <h2>Sessions</h2>
          <button className="new-session-btn" onClick={onNewSession} title="New Session">
            +
          </button>
        </div>
        <div className="session-list">
          {localSessions.length === 0 ? (
            <div className="empty-state">
              No sessions
              <button className="start-session-btn" onClick={onNewSession}>
                Start a session
              </button>
            </div>
          ) : (
            localSessions.map((session) => (
              <div
                key={session.id}
                className={`session-item ${selectedId === session.id ? 'selected' : ''}`}
                onClick={() => onSelectSession(session.id)}
              >
                <span className={`state-indicator ${session.state}`} />
                <span className="session-label">{session.label}</span>
                <button
                  className="close-session-btn"
                  onClick={(e) => {
                    e.stopPropagation();
                    onCloseSession(session.id);
                  }}
                  title="Close session"
                >
                  ×
                </button>
              </div>
            ))
          )}
        </div>
      </div>

      {/* Daemon Sessions (other cm sessions) */}
      {daemonSessions.length > 0 && (
        <div className="sidebar-section">
          <div className="sidebar-header">
            <h2>Other Sessions</h2>
            <span className={`connection-indicator ${isConnected ? 'connected' : ''}`} />
          </div>
          <div className="session-list">
            {daemonSessions.map((session) => (
              <div key={session.id} className="session-item daemon-session">
                <span className={`state-indicator ${session.state}`} />
                <span className="session-label">{session.label}</span>
                {session.todos && session.todos.length > 0 && (
                  <span className="todo-count">{session.todos.length}</span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* PRs needing attention */}
      {waitingPRs.length > 0 && (
        <div className="sidebar-section">
          <div className="sidebar-header">
            <h2>PRs</h2>
            <span className="pr-count">{waitingPRs.length}</span>
          </div>
          <div className="pr-list">
            {waitingPRs.map((pr) => (
              <a
                key={pr.id}
                href={pr.url}
                target="_blank"
                rel="noopener noreferrer"
                className={`pr-item ${pr.role}`}
              >
                <span className="pr-repo">{pr.repo.split('/')[1]}</span>
                <span className="pr-number">#{pr.number}</span>
                <span className="pr-reason">{pr.reason.replace(/_/g, ' ')}</span>
              </a>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
```

**Step 2: Update Sidebar CSS**

Add to end of `app/src/components/Sidebar.css`:

```css
/* Sections */
.sidebar-section {
  border-bottom: 1px solid #3c3c3c;
}

.sidebar-section:last-child {
  border-bottom: none;
}

/* Connection indicator */
.connection-indicator {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #f44747;
}

.connection-indicator.connected {
  background: #4ec9b0;
}

/* Todo count */
.todo-count {
  background: #3c3c3c;
  color: #cccccc;
  font-size: 11px;
  padding: 2px 6px;
  border-radius: 10px;
  margin-left: auto;
}

/* Daemon sessions (not interactive) */
.session-item.daemon-session {
  opacity: 0.7;
  cursor: default;
}

.session-item.daemon-session:hover {
  background: transparent;
}

/* PR list */
.pr-list {
  padding: 8px;
}

.pr-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  border-radius: 4px;
  color: #cccccc;
  text-decoration: none;
  font-size: 12px;
}

.pr-item:hover {
  background: #2a2d2e;
}

.pr-item.author {
  border-left: 2px solid #4ec9b0;
}

.pr-item.reviewer {
  border-left: 2px solid #dcdcaa;
}

.pr-repo {
  color: #808080;
}

.pr-number {
  color: #569cd6;
}

.pr-reason {
  color: #808080;
  font-size: 11px;
  margin-left: auto;
}

.pr-count {
  background: #dcdcaa;
  color: #1e1e1e;
  font-size: 11px;
  padding: 2px 6px;
  border-radius: 10px;
  font-weight: 600;
}
```

**Step 3: Verify TypeScript compiles**

```bash
cd app && pnpm build
```

Expected: Build succeeds (may have type errors in App.tsx, fixed in next task).

**Step 4: Commit**

```bash
git add app/src/components/Sidebar.tsx app/src/components/Sidebar.css
git commit -m "feat(app): update Sidebar to show daemon sessions and PRs"
```

---

## Task 9: Wire Up WebSocket in App.tsx

**Files:**
- Modify: `app/src/App.tsx`

**Step 1: Update imports**

Add at top:

```tsx
import { useDaemonSocket } from './hooks/useDaemonSocket';
import { useDaemonStore } from './store/daemonSessions';
```

**Step 2: Add daemon state and WebSocket connection**

Add inside App function, after the `useSessionStore` destructuring:

```tsx
  const {
    daemonSessions,
    setDaemonSessions,
    prs,
    setPRs,
    isConnected,
    setConnected,
  } = useDaemonStore();

  // Connect to daemon WebSocket
  useDaemonSocket({
    onSessionsUpdate: setDaemonSessions,
    onPRsUpdate: setPRs,
  });
```

**Step 3: Update Sidebar props**

Update the Sidebar component usage to pass the new props:

```tsx
      <Sidebar
        localSessions={sessions}
        selectedId={activeSessionId}
        onSelectSession={handleSelectSession}
        onNewSession={handleNewSession}
        onCloseSession={handleCloseSession}
        daemonSessions={daemonSessions}
        prs={prs}
        isConnected={isConnected}
      />
```

**Step 4: Verify TypeScript compiles**

```bash
cd app && pnpm build
```

Expected: Build succeeds.

**Step 5: Commit**

```bash
git add app/src/App.tsx
git commit -m "feat(app): wire up daemon WebSocket in App component"
```

---

## Task 10: End-to-End Test

**Step 1: Build and install cm**

```bash
make install
```

**Step 2: Stop existing daemon**

```bash
pkill -f "cm.*daemon" || true
```

**Step 3: Start daemon**

```bash
DEBUG=debug cm -d &
```

**Step 4: Start the Tauri app**

```bash
cd app && pnpm tauri dev
```

**Step 5: Verify connection**

Expected: App starts, sidebar shows "Other Sessions" section if any cm sessions exist.

**Step 6: Start a cm session in terminal**

```bash
cm -s test-session -y
```

Expected: Sidebar updates to show the new session under "Other Sessions".

**Step 7: Create a session in the app**

Click "+" and select a folder.

Expected: Session appears in "Sessions" section, and also appears in "Other Sessions" (since cm registers it with daemon).

**Step 8: Final commit**

```bash
git add -A
git commit -m "feat(app): Phase 2 complete - WebSocket connection to daemon"
```

---

## Verification Checklist

- [ ] Go daemon starts WebSocket server on port 9849
- [ ] WebSocket sends initial_state on connect
- [ ] Session register/unregister events broadcast
- [ ] Session state changes broadcast
- [ ] PR updates broadcast
- [ ] Frontend connects and receives events
- [ ] Sidebar shows daemon sessions
- [ ] Sidebar shows PRs needing attention
- [ ] Reconnection works when daemon restarts

---

## Next Phase Preview

Phase 3 will add:
- Hooks config generation in Tauri/Rust
- Session state/todos reflected from hooks
- Full Claude integration
