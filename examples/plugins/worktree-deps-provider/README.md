# Worktree dependency bootstrap provider

Example attn provider plugin built with `@attn/plugin`.

It demonstrates:

- the SDK-backed daemon handshake
- provider registration
- typed `worktree.create` and `worktree.delete` handlers
- `providerError()` when Git or package-manager setup fails
- `handled()` after the provider actually creates or removes a worktree

The example keeps the behavior genuinely useful:

- create the requested worktree with normal Git
- inspect the new worktree for dependency lockfiles
- run `pnpm install` when `pnpm-lock.yaml` is present
- otherwise run `yarn install` when `yarn.lock` is present
- otherwise run `npm install` when `package-lock.json` is present
- if dependency setup fails, remove the just-created worktree before returning an error
- delete worktrees with normal `git worktree remove`

This example consumes `@attn/plugin` through a local `file:` dependency back
into this repository. Until the SDK has a publishable external consumption path,
treat this as a source-tree example rather than a plugin to install with
`attn plugin install --path`.
