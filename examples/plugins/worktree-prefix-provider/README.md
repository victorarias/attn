# Worktree prefix provider

Example attn provider plugin built with `@attn/plugin`.

It demonstrates:

- the SDK-backed daemon handshake
- provider registration
- typed `worktree.create` and `worktree.delete` handlers
- `decline()` for repos outside the example contract
- `handled()` after the example provider actually creates or removes a worktree

The plugin only claims repositories that contain a
`.attn-example-provider` marker file. For those repos:

- create requests are routed into
  `<main_repo>/.attn-example-worktrees/<branch>`
- delete requests under that directory use `git worktree remove`

That makes it small enough to read while still showing the actual worktree
operations a provider would perform.

This example consumes `@attn/plugin` through a local `file:` dependency back
into this repository. Until the SDK has a publishable external consumption path,
treat this as a source-tree example rather than a plugin to install with
`attn plugin install --path`.
