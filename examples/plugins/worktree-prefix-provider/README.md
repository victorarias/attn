# Worktree prefix provider

Example attn provider plugin built with `@attn/plugin`.

It demonstrates:

- the SDK-backed daemon handshake
- provider registration
- typed `worktree.create` and `worktree.delete` handlers
- `providerError()` for unsupported calls
- `handled()` after the provider actually creates or removes a worktree

The example keeps the behavior intentionally concrete:

- create requests must include `requested_path`
- that path must stay inside `<main_repo>/.attn-example-worktrees/`
- the plugin runs `git worktree add` there
- delete requests outside that directory are rejected
- delete requests inside it use `git worktree remove`

This example consumes `@attn/plugin` through a local `file:` dependency back
into this repository. Until the SDK has a publishable external consumption path,
treat this as a source-tree example rather than a plugin to install with
`attn plugin install --path`.
