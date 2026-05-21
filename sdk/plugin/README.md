# `@attn/plugin`

Small TypeScript SDK for attn plugins.

This first cut focuses on the worktree extension path already exercised by attn:

- connect to the attn daemon over its plugin socket
- send the `hello` handshake
- declare concrete handled surfaces during the connection handshake
- handle daemon-initiated JSON-RPC requests
- return structured `handled`, `decline`, or `error` results

```ts
import {
  AttnPluginClient,
  decline,
  handled,
} from "@attn/plugin";

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
`ATTN_PLUGIN_NAME` from attn; manually-launched plugins can still pass
`socketPath` or `name` explicitly.

Create lifecycle hooks use the same registration path:

```ts
import {
  type WorktreeAfterCreateParams,
} from "@attn/plugin";

client.handle<"worktree.after_create">(
  "worktree.after_create",
  async (params: WorktreeAfterCreateParams) => {
    await bootstrapRepo(params.path);
  },
);
```

For a full plugin directory built on this SDK, see
`examples/plugins/worktree-deps-hook`.
