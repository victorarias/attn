# `@victorarias/attn-plugin`

Small TypeScript SDK for attn plugins.

This first cut focuses on the worktree extension path already exercised by attn:

- connect to the attn daemon over its plugin socket
- send the `hello` handshake
- declare concrete handled surfaces during the connection handshake
- respond to daemon healthchecks automatically
- handle daemon-initiated JSON-RPC requests
- return structured `handled`, `decline`, or `error` results

```ts
import {
  AttnPluginClient,
  decline,
  handled,
} from "@victorarias/attn-plugin";

const client = new AttnPluginClient({
  version: "0.1.0",
});

client.handle<"worktree.create">(
  "worktree.create",
  async (params) => {
    if (!params.main_repo.includes("example")) {
      return decline();
    }

    return handled({
      path: "/tmp/example-worktree",
      branch: params.branch,
    });
  },
);

await client.connect();
```

`client.handle(...)` is the declaration point. `connect()` includes every
registered surface in the daemon handshake, so plugin code does not maintain a
second registration list. Managed plugins get `ATTN_SOCKET_PATH` and
`ATTN_PLUGIN_NAME` from attn, plus an `ATTN_PLUGIN_GENERATION` token that the SDK
returns in the handshake. Manually launched plugins can still pass `socketPath`
or `name` explicitly and use generation 1.

Attn supervises installed plugin processes. A clean exit, crash, or connection
loss longer than five seconds triggers a restart with bounded backoff. Plugin
code should therefore rebuild registrations and in-memory state from durable
sources each time `connect()` succeeds.

The SDK handles `attn.health` internally and returns `{ ok: true }`. Plugin
authors do not need to register a health handler for the daemon to distinguish a
live plugin from a process that merely started.

The connection is full duplex, and attn does not wait its turn. A request from
attn — a healthcheck, a `driver.session_closed` for a session whose process just
exited — can arrive while one of the plugin's own requests is still waiting for
its response, so the next message on the socket is not necessarily the one the
plugin asked for. The SDK already routes by shape: a message carrying a method
goes to its handler, a message carrying only an id resolves the pending request.
Plugin code that talks to the socket directly must do the same; treating an
unexpected request as an error loses whichever message attn happened to send
first, and since the ordering is a scheduling race it loses it rarely.

Create lifecycle hooks use the same registration path:

```ts
import {
  type WorktreeAfterCreateParams,
} from "@victorarias/attn-plugin";

client.handle<"worktree.after_create">(
  "worktree.after_create",
  async (params: WorktreeAfterCreateParams) => {
    await bootstrapRepo(params.path);
  },
);
```

For a full plugin directory built on this SDK, see
`examples/plugins/worktree-deps-hook`.
