# Stricter delegation admission and placement

## Outcome

`attn delegate` rejects unknown built-in model IDs before launch, resolves
supported aliases to canonical IDs, and shares that model authority with
`attn preflight`.

Workspace placement and repository placement are independent. `--workspace`
chooses where the pane appears. `--no-worktree` reuses the source session's
checkout unless `--cwd` explicitly chooses another checkout. Worktree creation
still infers a repository from existing workspace members when no repository
override is supplied.

## Boundaries

- Plugin model identifiers remain driver-owned and pass through unchanged.
- Existing Claude aliases remain accepted and resolve to current canonical IDs.
- Conflicting explicit Git locations fail before runtime launch and name both
  resolved repositories with correction guidance.
- There is no post-launch model handshake, dry run, or explain mode.

## Verification status

- Complete package tests pass for `cmd/attn`, `internal/client`,
  `internal/agent`, and `internal/preflight`; the focused delegation tests pass
  in `internal/daemon`.
- The short full-Go harness passes every package and daemon shard except the
  app-builder tests that download TypeScript; those fail before test behavior
  with a Spotify Artifactory `401`.
- The freshly built CLI resolves `opus` to `claude-opus-5`, rejects `opus-5`
  and `gpt-5.6` with canonical suggestions, and displays workspace and
  repository placement separately in `delegate --help`.
- An isolated packaged-app install could not complete because the same
  Artifactory authentication failure blocked frontend dependency installation,
  so integrated app/daemon verification was not available in this checkout.
