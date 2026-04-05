# Web Client: Mobile Session Control via Tailscale

Date: 2026-04-04
Status: Superseded
Owner: daemon/web

Superseded note: the implemented daemon integration uses the host machine's existing Tailscale device plus `tailscale serve` to proxy `127.0.0.1:9849`, not an embedded `tsnet` node with its own tailnet identity. This avoids creating a duplicate Tailscale device for `attn`.

## Motivation

With remote host support, attn sessions can run on multiple machines. But the only way to interact with them is through the Tauri desktop app. A lightweight web client served by the daemon would allow monitoring and controlling sessions from a phone or any browser, secured by Tailscale with zero custom auth.

## Design Constraints

- **Single HTML file** (or minimal static files), vanilla JS, no framework, no build step.
- **No code sharing** with the Tauri app — fully independent. If the web client needs more sophistication later, that's a future decision.
- **`go:embed`** into the daemon binary — the web client ships with the daemon, no separate deployment.
- **Fully self-contained** — xterm.js and addons embedded alongside HTML via `go:embed`. No CDN, no external runtime dependencies.
- **Mobile-first** — designed for phone screens, works on desktop as a bonus.
- **Read-heavy scope** — attach to existing sessions, type input, approve permissions. No diff panels, git operations, or review loops.

## Architecture

```
                                       Machine A (laptop)
Phone (Safari)                         ───────────────────────────
─────────────────                      ┌─────────────────────────┐
  Tailscale VPN ──── WSS ────────────> │ tsnet :443 (HTTPS+WSS)  │
  (https://attn-laptop.<tailnet>)      │   ├─ / → embedded HTML  │
                                       │   └─ /ws → WebSocket    │
                                       │                         │
  Tauri app ──── WS ─────────────────> │ 127.0.0.1:9849 (local)  │
                                       └─────────────────────────┘

                                       Machine B (remote server)
                                       ───────────────────────────
                                       ┌─────────────────────────┐
  Phone can also ──── WSS ───────────> │ tsnet :443 (HTTPS+WSS)  │
  (https://attn-server.<tailnet>)      │   ├─ / → embedded HTML  │
                                       │   └─ /ws → WebSocket    │
                                       └─────────────────────────┘
```

Each daemon independently joins the tailnet as its own node. The phone connects directly to whichever daemon owns the sessions it wants to interact with. No single aggregation point — if the laptop is asleep, remote server sessions are still reachable.

Both listeners on each daemon share the same HTTP handler and WebSocket hub. The tsnet listener adds HTTPS with automatic Let's Encrypt certificates via Tailscale.

**Hostname convention:** Each daemon gets a unique tsnet hostname (e.g., `attn-laptop`, `attn-server`). Configurable via daemon settings, defaults to `attn-<system-hostname>`.

## Scope

### In scope

- Session list with state indicators (working, idle, waiting_input, pending_approval, etc.)
- Tap session → full-screen xterm.js terminal (attach to PTY stream)
- Type input into attached session
- Session state updates in real-time
- Remote sessions visible (aggregated by daemon, same as Tauri app sees)
- Endpoint labels shown per session

### Out of scope (for now)

- Spawning new sessions (use CLI or Tauri app)
- Diff/changes panels
- Git operations
- Review loops
- PR monitoring
- Settings management

Spawning is the most likely future addition. It would require a minimal location picker, which is non-trivial. Deferring it keeps this initial scope tight.

## Web Client Design

### File structure

```
web/
  index.html           # HTML + inline CSS + inline JS
  vendor/
    xterm.min.js       # xterm.js (vendored)
    xterm.min.css      # xterm.js styles
    xterm-addon-fit.min.js  # fit addon
```

All files embedded via `go:embed web/*` in the daemon package. xterm.js vendored from npm — no CDN, no external dependencies at runtime. Updating xterm.js is a manual copy of the dist files.

### Mobile UX (two views)

**Session list view** (default):
- Full-screen list of sessions
- Each row: session label, state badge (colored dot + text), endpoint name if remote
- Tap row → switch to terminal view
- Pull-to-refresh or auto-update via WebSocket events

**Terminal view**:
- Full-screen xterm.js terminal
- Top bar: session label, state badge, back button
- **Quick-action bar** above the keyboard: two rows of tappable buttons
  - **Modifier/key row** (always visible): keys that mobile keyboards lack
    - **Esc** — sends escape sequence immediately on tap
    - **Shift+Tab** — sends `\x1b[Z` immediately on tap
    - **Ctrl** — sticky modifier: highlights on tap, stays "pressed" until the next character is typed, then sends ctrl+char (e.g., tap Ctrl → type "c" → sends `\x03`). Tapping again deactivates.
    - **Alt** — sticky modifier: same behavior as Ctrl. Tap Alt → type "d" → sends `\x1bd`. Tapping again deactivates.
  - **Shortcut row** (always visible): common inputs sent immediately on tap (text + newline)
    - **yes** / **no** / **/compact** / **/new**
    - **1** / **2** / **3** / **4** / **5** / **6** (for numbered menu selections)
- xterm-addon-fit auto-sizes to viewport
- Back button (or swipe) → return to session list

No split views — phone screens are too small. One view at a time.

### PTY replay on attach

When the web client sends `attach_session`, the daemon replays buffered recent PTY output so the user can see what happened before they connected. This uses the same replay mechanism the Tauri app uses — no new daemon work needed.

### WebSocket protocol usage

The web client speaks the same protocol as the Tauri app. Commands used:

| Command | Purpose |
|---------|---------|
| `query` | Get initial session list |
| `attach_session` | Subscribe to PTY output for a session |
| `detach_session` | Unsubscribe from PTY output |
| `pty_input` | Send keyboard input to session |
| `pty_resize` | Update terminal dimensions |

| Event | Purpose |
|-------|---------|
| `session_registered` | New session appeared |
| `session_unregistered` | Session removed |
| `session_state_changed` | State update (working → idle, etc.) |
| `pty_output` | Terminal output data (base64) |

Protocol version is checked on connect. If mismatched, show an error asking the user to update the daemon.

### State indicator mapping

| State | Color | Mobile label |
|-------|-------|-------------|
| `launching` | blue | Launching |
| `working` | green | Working |
| `pending_approval` | yellow flash | Needs Approval |
| `waiting_input` | yellow | Waiting |
| `idle` | gray | Idle |
| `unknown` | purple | Unknown |

### Offline / disconnect handling

- Show a banner when WebSocket disconnects
- Auto-reconnect with exponential backoff (1s, 2s, 4s, max 30s)
- On reconnect, re-query sessions and re-attach if a session was selected

## Daemon Changes

### Phase 1: tsnet integration

Add `tailscale.com/tsnet` dependency. New daemon behavior:

```go
// Dual listener setup (simplified)
// 1. Existing localhost listener (unchanged)
localLn, _ := net.Listen("tcp", "127.0.0.1:9849")

// 2. Tailnet listener (new, opt-in)
tsSrv := &tsnet.Server{
    Hostname: "attn-" + systemHostname, // unique per machine
    Dir:      filepath.Join(attnDir, "tsnet"),
}
tsLn, _ := tsSrv.ListenTLS("tcp", ":443")
```

**Configuration:** The tsnet listener is opt-in via daemon settings (stored in the existing settings system). Settings:
- `tailscale_enabled` (bool) — enable/disable tsnet listener. Default: false.
- `tailscale_hostname` (string) — override the tsnet node name. Default: `attn-<system-hostname>`.

When disabled, the daemon behaves exactly as today.

**First-run auth:** tsnet prints a URL to stdout on first launch. The user visits it to authorize the node in their Tailscale admin console. This is a one-time operation; state is persisted in `~/.attn/tsnet/`.

**WhoIs:** On WebSocket connect, log the connecting device/user via `tsnet.LocalClient().WhoIs()`. This is informational only — all tailnet connections are trusted.

### Phase 2: HTTP + WebSocket serving

Add an HTTP handler that:
1. Serves embedded static files at `/`
2. Upgrades to WebSocket at `/ws`
3. Shares the existing WebSocket hub (same session state, same broadcast)

```go
//go:embed web/*
var webFS embed.FS

mux := http.NewServeMux()
mux.Handle("/", http.FileServer(http.FS(webFS)))
mux.HandleFunc("/ws", wsUpgradeHandler) // reuses existing hub

go http.Serve(localLn, mux)   // localhost for Tauri
http.Serve(tsLn, mux)          // tailnet for web client
```

The existing localhost listener also gets the HTTP handler — this is harmless (Tauri connects directly to `/ws`, the static files are just unused on localhost).

### Phase 3: web client HTML

Build the `web/index.html` file:
- Inline CSS (mobile-first responsive)
- Inline JS (WebSocket client, xterm.js integration, view switching, quick-action bar)
- References vendored xterm.js and xterm-addon-fit from `vendor/`

## Implementation Order

1. **web/index.html** — build the web client against the existing localhost:9849 WebSocket (for testing without tsnet)
2. **Daemon HTTP serving** — add `go:embed` and HTTP handler, serve on localhost alongside WebSocket
3. **tsnet integration** — add tailnet listener with HTTPS, opt-in config
4. **Polish** — mobile viewport tuning, reconnection, state badges

Steps 1-2 are independently useful (web client works on localhost). Step 3 adds remote access.

## Trade-offs

**Binary size:** tsnet adds ~15-25 MB to the daemon binary. Vendored xterm.js adds ~1 MB. Acceptable for a personal tool.

**No auth:** Security relies entirely on Tailscale. If someone is on your tailnet, they have full access to all sessions. This is fine for a personal tailnet. If sharing the tailnet with others becomes relevant, Tailscale ACLs can restrict access to specific nodes.

**No spawn:** Users must start sessions from CLI or Tauri app. This is intentional — location picking on mobile is complex and the primary use case is monitoring + light interaction with running sessions.

**No session aggregation on mobile:** Unlike the Tauri app (which aggregates remote sessions via the hub), the web client connects to one daemon at a time. To see sessions on a different machine, you navigate to that machine's tsnet URL. This is simpler and avoids the laptop-must-be-awake problem. If cross-machine aggregation on mobile becomes important, a future "dashboard" page could link to all known daemons.
