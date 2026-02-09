# attn - Claude Session Orchestration

## Overview

`attn` is a Go CLI/daemon for managing multiple Claude Code sessions. It wraps `claude` to track session state, provides a daemon that aggregates status across sessions, and enables **inter-session communication** via Slack thread subscriptions.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        attn daemon                           │
│  - Unix socket server (~/.attn/attn.sock)                   │
│  - WebSocket server (ws://127.0.0.1:9849/ws)               │
│  - SQLite persistence (~/.attn/attn.db)                     │
│  - Slack Socket Mode (thread subscriptions)                 │
│  - PR polling (GitHub API)                                   │
│  - Session state tracking                                    │
└─────────────────────────────────────────────────────────────┘
      ▲              ▲                    ▲
      │ Unix socket  │ WebSocket          │ Slack Socket Mode
      │              │                    │
┌─────┴─────┐  ┌────┴────────┐    ┌──────┴──────┐
│ attn CLI  │  │ AttnStatus  │    │ Slack API   │
│ (wrapper) │  │ (Swift UI)  │    │ (threads)   │
└───────────┘  └─────────────┘    └─────────────┘
```

## Inter-Session Communication

Sessions can communicate with each other through Slack threads. This is the primary mechanism for multi-CC orchestration.

### How It Works

```
Session A                    Slack                       Daemon                    Session B
    │                          │                           │                          │
    │ slack-post --thread TS   │                           │                          │
    │────────────────────────▶ │                           │                          │
    │                          │  Socket Mode event        │                          │
    │                          │─────────────────────────▶ │                          │
    │                          │                           │  cc-send <session_id>    │
    │                          │                           │────────────────────────▶ │
    │                          │                           │                          │
```

1. Session A posts to a Slack thread via `slack-post`
2. A human (or different bot identity) replies to the thread
3. Daemon receives the reply via Slack Socket Mode
4. Daemon looks up subscriptions for that thread
5. Daemon runs `cc-send <session_id> <message>` to deliver to subscribed sessions
6. The subscribed session receives the message as user input

**Important**: Bot messages (from the same bot token) are filtered to prevent infinite loops. Only human messages are delivered.

### CLI Commands

```bash
# Subscribe current session to a Slack thread
# Requires ATTN_SESSION_ID env var (set automatically by attn wrapper)
attn subscribe <channel_id> <thread_ts>
attn subscribe -name "channel-name" C0ACFJ7PN2Z 1770673905.431819

# Unsubscribe
attn unsubscribe <channel_id> <thread_ts>

# List all active subscriptions (JSON output)
attn subscriptions
attn subscriptions -session <session_id>
```

### Quick Start: Two Sessions Talking

```bash
# Terminal 1: Start session A
attn-claude  # or just `c`

# In session A, post a thread:
#   slack-post C0ACFJ7PN2Z "Coordination thread for task X"
#   (note the thread_ts from slack-ro history)

# Terminal 2: Start session B
attn-claude

# In session B, subscribe to the thread:
#   attn subscribe -name "task-x" C0ACFJ7PN2Z <thread_ts>

# Now: human replies in that Slack thread → delivered to session B
# Session A can post updates → human (Trevor) relays or reacts → session B notified
```

### Auth Setup

Slack credentials at `~/.secrets/slack/auth.json`:
```json
{
  "bots": {
    "trevor-bot": {
      "bot_token": "xoxb-...",
      "app_token": "xapp-..."
    }
  }
}
```

The `app_token` (xapp-) enables Socket Mode. The `bot_token` (xoxb-) is used for username resolution.

## Key Files

### cmd/attn/main.go
- Entry point - dispatches to daemon, wrapper, or hook handlers
- `runWrapper()` - launches claude with hooks config
- `runSubscribe()` / `runUnsubscribe()` / `runSubscriptions()` - thread subscription CLI
- `runClaudeDirectly()` / `runCodexDirectly()` - actual execution paths

### internal/daemon/daemon.go
- Main daemon loop
- Handles registration, state updates, queries
- Thread subscription handlers (subscribe, unsubscribe, list)

### internal/daemon/slack.go
- `slackMonitor` - manages Slack Socket Mode connection lifecycle
- `ensureSlackMonitor()` - lazy connection (starts on first subscription)
- `handleSlackMessage()` - matches thread replies to subscriptions, delivers via cc-send
- `stopSlackMonitor()` - disconnects when no subscriptions remain

### internal/daemon/websocket.go
- WebSocket hub for UI clients (AttnStatus)
- `broadcastSubscriptions()` - pushes subscription state to all WS clients
- `sendInitialState()` - includes subscriptions in initial state payload
- WS command handlers for subscribe/unsubscribe/list

### internal/slack/client.go
- Minimal Slack Socket Mode client (~320 LOC, no external deps beyond nhooyr.io/websocket)
- `Listen()` - connects to Socket Mode, delivers thread reply events
- Filters: skips bot messages, edits, deletes, non-thread messages
- `resolveUsername()` - user ID to display name with caching

### internal/client/client.go
- Go client for daemon communication (Unix socket)
- `Register(id, label, dir, windowID, cgWindowID)` - register new session
- `SubscribeThread()` / `UnsubscribeThread()` / `ListSubscriptions()` - thread subscription client

### internal/store/store.go + sqlite.go
- SQLite persistence layer
- `thread_subscriptions` table (migration 20)
- CRUD: AddThreadSubscription, RemoveThreadSubscription, GetThreadSubscriptions, GetThreadSubscriptionsBySession
- `GetSubscribedThreadKeys()` - returns map of "platform:channel:thread" → []sessionID for fast lookup

### internal/protocol/schema/main.tsp
- TypeSpec schema defining all messages
- `ThreadSubscription` model - id, platform, channel_id, thread_ts, session_id, channel_name
- `SubscribeThreadMessage` / `UnsubscribeThreadMessage` / `ListSubscriptionsMessage`
- Run `make generate-types` after schema changes

### internal/protocol/generated.go + constants.go
- Generated Go types from TypeSpec schema
- Event constants: `EventSubscriptionsUpdated = "subscriptions_updated"`
- Command constants: `CmdSubscribeThread`, `CmdUnsubscribeThread`, `CmdListSubscriptions`

## Protocol

Sessions have states: `idle`, `working`, `waiting_input`, `pending_approval`, `wrapped`

Registration flow:
1. Wrapper calls `client.Register(sessionID, label, cwd, windowID, cgWindowID)`
2. Daemon stores in SQLite, broadcasts via WebSocket
3. UI receives update, displays session

Thread subscription flow:
1. CLI calls `client.SubscribeThread(platform, channelID, threadTS, sessionID, channelName)`
2. Daemon stores in SQLite, starts Slack monitor if needed
3. Daemon broadcasts `subscriptions_updated` to WebSocket clients
4. On thread reply: daemon matches subscription, runs `cc-send` to deliver

## Build

```bash
cd ~/third/attn
go build -o attn ./cmd/attn
make install        # builds, installs to ~/.local/bin, restarts daemon
make generate-types # regenerate Go types from TypeSpec schema
```

Or use the combined script (builds Go + Swift, deploys both):
```bash
~/third/attn-status/build-and-deploy.sh
~/third/attn-status/build-and-deploy.sh --swift-only  # skip Go rebuild
```

## Testing

```bash
cd ~/third/attn
go test ./...
```

Note: Tests use `GIT_TEMPLATE_DIR=` to isolate from user's global git hooks template.

## Data

- **Database**: `~/.attn/attn.db` (SQLite)
- **Socket**: `~/.attn/attn.sock` (Unix domain socket)
- **PID file**: `~/.attn/attn.pid`
- **Daemon log**: `~/.attn/daemon.log`
- **Slack auth**: `~/.secrets/slack/auth.json`
