# `@attn/plugin`

Small TypeScript SDK for attn plugins.

This first cut focuses on the provider path already exercised by attn's
worktree extension points:

- connect to the attn daemon over its plugin socket
- send the `hello` handshake
- declare provider surfaces during the connection handshake
- handle daemon-initiated JSON-RPC requests
- return structured `handled`, `decline`, or `error` results

```ts
import {
  AttnPluginClient,
  decline,
  handled,
  type WorktreeCreateParams,
  type WorktreeCreateResult,
} from "@attn/plugin";

const client = new AttnPluginClient({
  socketPath: process.env.ATTN_SOCKET_PATH ?? "",
  name: "example-worktree-provider",
  version: "0.1.0",
});

client.on<WorktreeCreateParams, WorktreeCreateResult>(
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

await client.connect({
  providerSurfaces: ["worktree.create"],
});
```

The runtime remains intentionally small. Driver, observer, and actor helpers
should be extracted once those flows have concrete plugin implementations.

For a full plugin directory built on this SDK, see
`examples/plugins/worktree-deps-provider`.
