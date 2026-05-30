# Plan: Confirmed Worktree Delete

## Goal

Make worktree deletion reliable when the worktree has local changes, while keeping the destructive path explicit. The usual flow should stay lightweight: the user confirms a normal delete, attn tries it, and only if that fails does attn show the concrete reason and offer a force delete. If the user confirms force, attn deletes the worktree folder and local branch. Remote refs are never deleted.

## Current Understanding

- Worktree deletion is initiated from the new-session `LocationPicker` / `RepoOptions` flow and from the post-session `WorktreeCleanupPrompt`.
- `RepoOptions` currently has a compact inline `Delete <name>? (y/n)` prompt, then calls `sendDeleteWorktree(path)`.
- `WorktreeCleanupPrompt` already frames deletion as a user choice, but failed deletion only exposes the raw error and retry uses the same non-force path.
- `sendDeleteWorktree` sends `delete_worktree` over websocket and waits for `delete_worktree_result`.
- `internal/daemon.doDeleteWorktree` currently stops sessions in that directory before asking any `worktree.delete` plugin provider or calling `git.DeleteWorktree(mainRepo, path)`.
- `internal/git.DeleteWorktree` uses `git worktree remove <path>` without `--force`, so dirty worktrees fail before `finalizeDeletedWorktree` can remove the local branch.
- `finalizeDeletedWorktree` already deletes the local branch with `git branch -D`; it logs branch delete failure but does not fail the whole worktree deletion.
- Worktree provider plugins receive `main_repo`, `path`, and `branch`; they do not currently know whether the user confirmed a force delete.

## Components

- `internal/protocol/schema/main.tsp`: add protocol shape for force deletion and structured delete failure details, then regenerate Go/TS protocol files.
- `internal/git/worktree.go`: support non-force and force worktree removal.
- `internal/daemon/worktree.go`: thread force options through deletion, move provider/built-in deletion before irreversible attn session/store cleanup, and preserve store/broadcast behavior after successful deletion.
- `internal/daemon/plugin_worktree.go` and `sdk/plugin/src/index.ts`: include a `force` boolean for `worktree.delete` provider calls.
- `app/src/hooks/useDaemonSocket.ts`: expose `sendDeleteWorktree(path, endpointId?, { force })` and structured failure details from `delete_worktree_result`.
- `app/src/components/NewSessionDialog/RepoOptions.tsx`: keep the terse inline normal-delete prompt, then show a force option only after a failed normal delete explains why it failed.
- `app/src/components/WorktreeCleanupPrompt.tsx` and its handler in `App.tsx`: after normal delete failure, show the reason and let retry send `force: true`.

## Code Shape

```text
User presses delete
  -> existing inline prompt asks Delete <name>? (y/n)
  -> user confirms normal delete
    -> UI sends delete_worktree { path }
    -> daemon discovers worktree
    -> daemon dispatches worktree.delete provider with force=false
      -> if provider handles deletion, validate and finalize
      -> if provider errors, return failure; do not fall back to built-in force
    -> built-in fallback runs git worktree remove <path>
  -> if normal delete fails
    -> daemon returns delete_worktree_result with reason and forceable=true when appropriate
    -> UI keeps the row lightweight but shows the reason and offers Force delete / cancel
  -> user confirms force delete
    -> UI sends delete_worktree { path, force: true }
    -> daemon dispatches worktree.delete provider with force=true
      -> if provider handles deletion, validate and finalize
      -> if provider errors, return failure; respect the plugin
    -> built-in fallback runs git worktree remove --force <path>
  -> after successful provider or built-in deletion
    -> daemon stops/removes attn sessions in that directory
    -> daemon removes registry row and git branch -D <branch>
    -> daemon broadcasts worktree_deleted and sessions_updated
```

Suggested structured delete failure shape:

```ts
type DeleteWorktreeResultMessage = {
  event: "delete_worktree_result";
  path: string;
  endpoint_id?: string;
  success: boolean;
  error?: string;
  forceable?: boolean;
  reason_kind?: "dirty_worktree" | "provider_error" | "not_found" | "git_error";
};
```

The first user confirmation authorizes a normal delete only. The second confirmation, shown only after a failed normal delete, authorizes `force: true`.

## Approach

- Add `force?: boolean` to `DeleteWorktreeMessage`.
- Add structured fields to `DeleteWorktreeResultMessage` so the UI can distinguish forceable dirty-worktree failures from provider errors and other hard failures.
- Implement `git.DeleteWorktree(repoDir, path, force)` with `git worktree remove --force <path>` only when requested. Keep prune behavior for missing directories.
- Change `doDeleteWorktree` to accept an options struct, including `Force bool`, and pass that into provider dispatch and the built-in git delete fallback.
- Keep local branch deletion as-is: after the worktree is gone, delete the local branch with `git branch -D`; do not touch remotes.
- Have provider plugins receive `force` so custom deletion policies can honor the same user confirmation. If a plugin claims the delete and fails, attn should return that failure instead of bypassing the plugin with built-in deletion.
- Move deletion before irreversible attn cleanup:
  - Discover the worktree and branch first.
  - Dispatch the plugin provider before stopping/removing sessions.
  - If no provider handles it, run built-in git delete.
  - Only after deletion succeeds, stop/remove attn sessions, remove the store row, delete the local branch, and broadcast.
- In `RepoOptions`, preserve the current inline normal-delete prompt. On normal delete failure, replace that row with compact failure copy plus `Force delete` and cancel options.
- In `WorktreeCleanupPrompt`, keep the first delete as normal delete. On failure, show the reason and make retry an explicit force-delete action when the failure is forceable.
- Preserve the existing operation-running state and refresh of repo options after deletion.

## Progress

- [x] Identify current deletion flow and failure point.
- [x] Draft implementation plan.
- [x] Add protocol changes and regenerate generated files.
- [x] Implement git/daemon force delete and structured failure support.
- [x] Update plugin delete params and SDK types.
- [x] Update frontend confirmation UI.
- [x] Add backend and frontend tests.
- [x] Run focused Go and frontend verification.
- [x] Run real-app/manual verification.
- [x] Open PR, resolve merge conflict with `main`, pass CI, and receive required review.

## Outcome

PR: https://github.com/victorarias/attn/pull/239

Status: ready to merge. CI passed, `figgyster` approved, Codex review returned `+1`, and there are no inline review comments.

## Decisions

- Decision: Force deletion should require an explicit confirmed request from the UI.
  Reason: Dirty worktree deletion is destructive; a plain delete action should attempt normal deletion first, then let the user opt into force after seeing the failure reason.

- Decision: Keep the lightweight inline delete UX and add the force option only after normal deletion fails.
  Reason: The inline prompt is intentional and keeps the picker fast; forcing should be a recovery path, not the first confirmation path.

- Decision: Respect worktree delete providers before attn tears down local session state.
  Reason: Plugins may own custom deletion policy. If they fail, the delete fails and attn should not have already removed sessions or local tracking.

- Decision: Keep remote refs out of scope.
  Reason: Victor wants local cleanup only: folder, worktree metadata, and local branch.

## Tests

- Go:
  - `internal/git`: non-force delete fails on dirty worktree; force delete succeeds and removes worktree metadata.
  - `internal/daemon`: failed normal delete leaves sessions, store rows, and branch intact.
  - `internal/daemon`: `doDeleteWorktree(...Force: true)` removes dirty worktree, then removes attn sessions, deletes local branch, broadcasts lifecycle events, and does not call remote-delete git operations.
  - `internal/daemon`: provider delete receives `force=false` on normal delete and `force=true` on force retry.
  - `internal/daemon`: provider failure leaves sessions/state intact and does not fall back to built-in delete.
  - Protocol parse/dispatch tests for the new result fields and `force` field.
- Frontend:
  - `RepoOptions` keeps normal inline confirmation, renders failed-delete reason, supports cancel, and sends force flag only from the force action.
  - `LocationPicker` passes endpoint id and force option through.
  - `WorktreeCleanupPrompt` first attempts normal delete, then shows failed-delete reason and retries with force only after explicit action.
- Manual:
  - Use dev install, create a worktree with unstaged and untracked files, delete from the picker, confirm normal delete, see the failure reason, force delete, and verify the folder and local branch are gone.
  - Repeat from the post-session cleanup prompt.
  - Verify remote branch still exists when the local branch tracked a remote.

## Open Questions

- Should failed dirty-worktree copy include a small sample of file names, or is Git's failure reason enough? A count plus first few paths would be useful if we can get it cheaply from a one-shot status after failure.

## Follow-ups

- Consider a reusable destructive-confirmation component if more local cleanup flows need the same preview/confirm pattern later.
