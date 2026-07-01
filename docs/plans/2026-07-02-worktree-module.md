# Plan: One worktree module, hooks inside

From the 2026-07-01 architecture review. Line references are as of commit
`80c62f6b` — re-anchor by symbol name.

## Goal

Worktree lifecycle orchestration exists twice, and the copies have diverged in a
way that matters:

- **Daemon path** (`internal/daemon/worktree.go` `doCreateWorktree` ~100,
  `doDeleteWorktree` ~247): dispatches the `worktree.before_create` /
  `worktree.create` provider / `worktree.after_create` plugin surfaces
  (`plugin_worktree.go`), registers the worktree in the store
  (`registerCreatedWorktree`), and broadcasts.
- **Workflow driver path** (`internal/workflow/driveragent.go` `runIsolated`
  ~236): calls `git.GenerateWorktreePath` + `git.CreateWorktree` +
  `git.DeleteWorktree` directly. It runs in the **CLI process** (the goja engine
  lives in `cmd/attn/workflow.go`), where the daemon's plugin registry does not
  exist — so workflow-isolated worktrees **silently skip every worktree plugin
  provider** and are invisible to the daemon's worktree bookkeeping.

Meanwhile `internal/git/worktree.go` ~50-105 accumulated five shallow
`CreateWorktree*` variants (one per call-site preference) — interface as wide as
the implementation.

Deepen: one `worktree` module owning spec → hooks → create → cleanup, with the
plugin chain injected as a seam. The daemon and the delegation path become
callers of one implementation; the workflow driver stops bypassing hooks.

## Architecture Map

```text
Current:
daemon doCreateWorktree ─> before_create hooks ─> create provider | git.CreateWorktree*
                        ─> registerCreatedWorktree ─> after_create hooks ─> broadcast
delegate.go ────────────> d.doCreateWorktree (OK: gets hooks)
workflow driveragent ───> git.CreateWorktree directly        LEAK: no hooks, no bookkeeping
                          (CLI process — cannot reach the daemon's plugin registry in-process)

Target:
internal/worktree (new package)
  Create(spec Spec, hooks Hooks) (Handle, error)     // path gen + create, hooks around it
  Handle.Cleanup(force bool) error                   // delete + branch prune
daemon  ─> worktree.Create(spec, pluginHooks{d})     // adapter over plugin_worktree.go dispatch
workflow driver ─> daemon over the existing unix socket (client call) ─> same door
                   (fail-closed if the daemon is unreachable — matches today's
                    fail-closed CreateWorktree error contract in runIsolated)
```

## Data Model / Interfaces

```go
// internal/worktree
type Spec struct {
    MainRepo     string
    Branch       string
    StartingFrom string // "" => the package's default (origin/main resolution)
    RequestedPath string // "" => GenerateWorktreePath
}

type Hooks interface {           // the seam; two real adapters justify it:
    BeforeCreate(Spec) error     //   daemon: plugin surface dispatch
    Create(Spec) (path string, handled bool, err error)  // provider may take over creation
    AfterCreate(Spec, path string) error
}                                //   tests: recording fake
type NoHooks struct{}            // explicit no-op adapter for hookless contexts

type Handle struct { Repo, Path, Branch string }
func Create(spec Spec, hooks Hooks) (Handle, error)
func (h Handle) Cleanup(force bool) error   // absorbs DeleteWorktree + DeleteBranch
```

The five `git.CreateWorktree*` variants collapse into `Create` honoring
`Spec.StartingFrom`/`Spec.RequestedPath`; `internal/git` keeps only the thin
`git worktree add` execution the module calls.

## Boundaries

- `internal/worktree` owns path generation, creation, hook ordering, and cleanup.
  It must not import `internal/daemon` or know what a plugin is — it sees `Hooks`.
- The daemon owns the plugin-backed `Hooks` adapter and store
  registration/broadcast (registration stays daemon-side; it is bookkeeping, not
  creation).
- The workflow driver must not shell out to git for worktrees once stage 2 lands;
  it asks the daemon. Its cleanliness-based keep/delete policy
  (`runIsolated`: keep dirty worktrees, delete clean ones) stays in the driver —
  that is workflow policy, not worktree mechanics.

## Implementation Steps

- [ ] Stage 1 (pure refactor, no behavior change): create `internal/worktree`,
      move orchestration out of `doCreateWorktree`/`doDeleteWorktree` into it,
      implement the daemon's plugin `Hooks` adapter, collapse the
      `git.CreateWorktree*` variants (update `delegate.go` and remaining callers).
      The workflow driver keeps calling git directly in this stage (now via
      `worktree.Create(spec, worktree.NoHooks{})` so the divergence is at least
      explicit and greppable).
- [ ] Stage 2 (closes the leak — **gated on the open question below**): add a
      socket command (or extend `CmdCreateWorktree` with an `ephemeral` flag —
      protocol change: follow AGENTS.md Critical Pattern #1, bump
      `ProtocolVersion`), add the matching `internal/client` method, and switch
      `driverAgent.runIsolated` to create/cleanup through the daemon.
      Fail-closed when the daemon is unreachable.
- [ ] Update the review-era AGENTS.md notes if any mention the bypass.

## Decisions

- Hooks as an injected interface rather than moving plugin dispatch into the
  module: the plugin registry is daemon-process state; the module must stay
  usable from tests and (stage 1) the CLI process. Two adapters (plugin-backed,
  recording fake) make the seam real.
- Worktree store registration stays out of the module — it is daemon bookkeeping
  with its own broadcast lifecycle.

## Open Questions

- Stage 2: should workflow-isolated worktrees be *registered* daemon-side (they
  become visible in the worktrees UI, possibly noisy for N parallel agents) or
  created through an `ephemeral` mode that runs hooks but skips
  registration/broadcast? Decide with Victor before implementing stage 2.

## Verification

```bash
go build ./...
go test ./internal/worktree ./internal/git ./internal/workflow -count=1
go test ./internal/daemon -run 'Worktree|Delegate|Plugin' -count=1
```

Structural asserts after stage 1:

```bash
grep -rn 'git\.CreateWorktree' internal/ --include='*.go' | grep -v _test | grep -v 'internal/worktree/'
# -> no output (all creation goes through the module)
```

Behavior checks:

1. Existing daemon worktree tests (`plugin_worktree_test.go`,
   `worktree`-related cases in `daemon_test.go`) stay green **unmodified** in
   stage 1 — they are the spec for hook ordering and provider-takeover behavior.
2. Delegation still creates worktrees with hooks (existing `delegate_test.go`).
3. Workflow isolation tests (`internal/workflow`) stay green in stage 1; stage 2
   adds a test asserting the driver's create path dispatches hooks (fake daemon
   or recording Hooks).
4. Manual (stage 2): `make dev`, run a workflow with `isolation: 'worktree'`,
   confirm worktree creation/cleanup works and hook logs appear in
   `~/.attn-dev/daemon.log` (`worktree hook plugin=… status=completed`).

## Follow-ups

- `doDeleteWorktree`'s provider-error classification could move into the module
  once stage 1 proves the shape.
