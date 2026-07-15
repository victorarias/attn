# attn OpenCode plugin

This installable plugin runs an OpenCode TUI in an attn-owned PTY while the
plugin uses OpenCode's loopback HTTP/SSE server to create and monitor the
linked native session. It requires a stable OpenCode release at or above
`1.17.16`; older, malformed, and prerelease versions stay visible as an
unhealthy plugin and do not register the `opencode` agent. Newer stable releases
are attempted against the server contract and surface the specific failing API
through degraded health if that contract has changed.

Install it into a non-production attn profile while developing:

```sh
attn plugin install --path /path/to/attn/plugins/attn-opencode
```

Restart that profile's daemon after installation. The plugin resolves
`ATTN_OPENCODE_EXECUTABLE` first, then `opencode` from the daemon's login-shell
`PATH`.

An ordinary promptless OpenCode session launches the TUI with OpenCode's own
model and variant defaults. The plugin observes the native session OpenCode
creates and persists its identity for resume. Delegated OpenCode sessions
require an explicit `--model provider/model` and one of the contract-tested
effort pins: `--effort low` or `--effort max`. The plugin sends those pins to
OpenCode's server as `{ providerID, id, variant }`, so its staged prompt is
bound to the selected native session. Other effort labels remain intentionally
unsupported until they have the same adapter and live-app evidence.

Each run gets a separate loopback port and random Basic-auth password. The
password, staged prompt, and attn-composed launch instructions live in private
files under the active profile's attn data directory
(`plugins/attn-opencode/`), are never included in metadata or argv, and are
deleted when attn reports that the PTY run has closed. A private per-process
OpenCode system hook rereads that file for every prompt, so resumed sessions
receive role and context changes without modifying repository or global
OpenCode configuration.

If the plugin process exits or loses its daemon connection, attn restarts it
with bounded backoff. On reconnect, the plugin intersects its private run
records with the daemon's active-run ownership and rebuilds monitoring only for
surviving runs. It reconnects to the same authenticated OpenCode server and
native session; it does not launch another TUI. Orphaned or malformed private
records are removed.

Ordinary sessions receive the same workspace-context, workflow, and ticket
guidance as built-in attn agents. Chiefs receive Notebook guidance instead.
Resuming a session, including a chief promotion or demotion, recomposes the
current guidance while retaining the same native OpenCode session.

The plugin reports OpenCode `busy` and `retry` as `working`. Native question
requests become `waiting_input`, and native permission requests become
`pending_approval`; answering either returns the session to `working`. On
startup, reconnect, and explicit idle events, the plugin checks OpenCode's
pending request lists before reporting `idle`, so a missed SSE event cannot hide
a session that needs attention. When an explicit idle turn has no native
attention request, the plugin classifies the newest completed assistant prose
using the same OpenCode model in a temporary, tool-disabled session. Ordinary
prose questions then become `waiting_input`, completed answers become `idle`,
and uncertain extraction or classification becomes `unknown`. The temporary
session is deleted without selecting it in the TUI, and duplicate idle events
reuse a verdict cached against the exact native message text.
