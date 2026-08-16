# Local WS client token

The daemon listens on two things: a unix socket, gated by file permissions, and
a loopback WebSocket port, gated by nothing. Any local process that knows the
port speaks the full protocol today — read every session, spawn agents, write
files through `fs_write`. PR #922 fenced profiles at the filesystem and spawn
level, but the wire carries no credential, so one profile's client can drive
another profile's daemon.

This closes it with a per-profile runtime token the daemon mints and every
local client presents in `client_hello`.

## The credential

`<data-dir>/client-token` — 32 bytes from `crypto/rand`, hex, written 0600.
Minted by the daemon at `Start()` and reused on later startups so a restart
does not strand a client. `ATTN_CLIENT_TOKEN` overrides the file for harnesses
that spawn a daemon and a client together and want to hand both the same value.

Per-profile is the point: the same binary serves every profile, so the secret
has to live in profile data. Nothing is baked at build time — a released binary
would ship a public secret.

Three token-shaped things now exist; they do different jobs:

| | what it proves | where |
| --- | --- | --- |
| `client-token` | this process may speak the protocol at all | hello, every local client |
| `browser-host-token` | this client is the trusted Tauri main webview | hello, app only |
| `ATTN_WS_AUTH_TOKEN` | operator bearer for an exposed WS port | HTTP header, opt-in |

## The check

`handleClientHello` compares in constant time before it records identity or
capabilities. A mismatch sends `command_error` with code
`unauthorized_client`, naming the file the daemon read, then closes with a
policy violation — never a silent hangup. Because identity is recorded only
after the token passes, a refused client also fails the existing
`workspace_sessions` gate on every command that follows.

A daemon holding no token authorizes nobody, rather than matching the client
that also sent nothing. `Start()` mints before the port listens, so this only
names a daemon someone constructed without it.

Whatever the client pipelined behind its hello is already in the read buffer
when the refusal is queued, and the message pump drops it: the
`workspace_sessions` gate hangs up on the connection itself, which would close
the socket before the write pump flushed the refusal — a silent close, arriving
as `events: []` on the wire. So the pump stops dispatching once the send channel
is closed, and the refusal is the last thing that connection hears.

One exemption: a connection that cleared `ATTN_WS_AUTH_TOKEN` at the HTTP layer
skips the client-token check. That bearer is only set when the port was
deliberately exposed beyond loopback (`tailscale serve`, the remote-web
setting), and the browser it serves cannot read a file on the daemon's disk.
The two gates answer the same question — may this process speak here? — and the
exposed-port one is the deliberate, stronger answer. Without the exemption,
enabling remote web would close it. Loopback is unaffected: with no bearer set,
the HTTP gate is skipped entirely and the client token is the only way in.

Unix-socket commands never reach this handler — `daemon.go`'s socket dispatch
is a separate switch, and file permissions already gate that path. The WS check
is not weakened for it.

## Admission

Refusing commands is only half of it. The daemon used to push `initial_state`
— every session, PR, workspace, ticket and setting — on connect, and register
the connection with the hub, both before a single byte arrived from the client.
A credential that gates writes while the whole read side streams to anyone who
opens a socket would be theatre.

So hub registration and the `initial_state` push moved out of the accept path
into `admitClient`, called only once the hello passes. The hub is the sole
fan-out, so a connection that has not presented the token is not merely refused
its commands — it is invisible to every broadcast. The app already sent hello
first thing on open, so nothing waits longer than it did.

Registration became a direct, locked insert (`wsHub.add`) rather than a
handoff on the `register` channel, which had no other user: admission is
synchronous now, so there is no window in which a client is admitted but not
yet receiving. The unregister side stays a channel — a disconnect is
discovered on the read pump, not asked for — and now closes the send channel
even for a connection that was never admitted, so its write pump still exits.

## Clients

One shared read per language, not N copies:

- Go (`cmd/attn` plugin requests, `scripts/wsctl`) — `config.ClientToken()`.
- Rust/Tauri — `profile::read_client_token()` behind the `get_client_token`
  command, mirroring `get_browser_host_token`.
- Frontend — Tauri `invoke` when in the app, `import.meta.env.VITE_CLIENT_TOKEN`
  when running in a plain browser (`dev:vite`, Playwright). `vite.config.ts`
  fills that in from the active profile's token file when it is not already set.
- Harness (`app/scripts/real-app-harness`) — `clientTokenForProfile()` beside
  the existing `dataDirForProfile()`.
- Playwright e2e — a fixed token handed to both the throwaway daemon
  (`ATTN_CLIENT_TOKEN`) and Vite (`VITE_CLIENT_TOKEN`).

`plugins/attn-pi` does not dial the daemon's WebSocket; its host talks over the
plugin runtime's stdio envelope, so there is nothing to change there.

## Remote endpoints

The brief scoped remote daemons out, but enforcing the token would have broken
them outright: the hub relays through `attn ws-relay`, a raw TCP pipe, so the
hub's own `client_hello` reaches the remote daemon end to end and would be
refused.

The hub reads the remote's token the way it already reads everything else on
that host — over SSH, with `runSSHExit` running `attn client-token` before the
dial. SSH access to the box is already full authority over it, so this grants
nothing new. It is not a shared filesystem: no path is assumed beyond the
remote binary the hub already invokes. When enrollment carries credentials, this
call is the thing it replaces.

The read happens after the connect succeeds — a host whose daemon has never run
has nothing to read — and costs one more short SSH exec per connection attempt,
on a path that already runs several. Not cached: the token is stable across
restarts, so a cache would buy one exec per reconnect at the price of knowing
when to invalidate it.

A remote running an older binary cannot happen: the hub already parks any
endpoint whose `SourceFingerprint` differs.

## Protocol

`client_token?: string` on `ClientHelloMessage`, `ProtocolVersion` 256 → 257,
`PROTOCOL_VERSION` in `useDaemonSocket.ts` to match.
