# Worktree dependency bootstrap hook

Example attn plugin built with `@attn/plugin`.

It demonstrates:

- the SDK-backed daemon handshake
- typed surface registration through `client.handle(...)`
- a `worktree.after_create` lifecycle hook

The example keeps the behavior genuinely useful:

- let attn create the requested worktree normally
- inspect the created worktree for dependency lockfiles
- run `pnpm install` when `pnpm-lock.yaml` is present
- otherwise run `yarn install` when `yarn.lock` is present
- otherwise run `npm install` when `package-lock.json` is present
- surface package-manager failures back to attn through the hook RPC call

This example consumes `@attn/plugin` through a local `file:` dependency back
into this repository. Until the SDK has a publishable external consumption path,
treat this as a source-tree example rather than a plugin to install with
`attn plugin install --path`.
