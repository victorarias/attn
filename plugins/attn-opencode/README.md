# attn OpenCode plugin

This installable plugin runs an OpenCode TUI in an attn-owned PTY while the
plugin uses OpenCode's loopback HTTP/SSE server to create and monitor the
linked native session. It requires OpenCode `1.17.16` or `1.17.18`; versions
outside that contract-tested set stay visible as an unhealthy plugin and do not
register the `opencode` agent.

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
password and staged prompt live in private files under the active profile's
attn data directory (`plugins/attn-opencode/`), are never included in metadata
or argv, and are deleted when attn reports that the PTY run has closed.

The first slice reports OpenCode `busy` and `retry` as `working`, and explicit
`idle` as `idle`. It intentionally does not infer `waiting_input` from an idle
event.
