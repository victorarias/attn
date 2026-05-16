# Long Git Operation Surface Map

Date: 2026-05-15

Purpose: map every product surface that waits on git today before choosing UI patterns. The problem is not one spinner. Different surfaces need different treatment depending on whether the user is blocked, whether existing data can stay visible, and whether the surface is even mounted.

## Operating Principles

- Do not show a loud progress surface for every slow git command. In large repos, "slow" may be normal.
- Prefer stale-but-usable UI for background refreshes. Mark data as refreshing only when the user needs to know.
- Escalate only when the user is blocked from completing the action they initiated.
- Do not anchor git operation UX only in the right dock. The right dock may be closed or irrelevant.
- Keep terminal space for agent/user interaction. Avoid terminal overlays unless the terminal itself initiated the operation.
- Operation state should come from daemon protocol, not inferred only from frontend promise timers.

## Surface Inventory

| Surface | Trigger | Daemon/git path | Current UI pattern | Blocking? | Problem in large repos | Recommended policy |
| --- | --- | --- | --- | --- | --- | --- |
| Active session branch metadata | Session register; branch monitor every 15s | `git.GetBranchInfo`: `rev-parse`, `symbolic-ref`, worktree detection | No explicit UI; branch/worktree badge updates when session data changes | No | Slow branch reads can lag session metadata but should not distract | Silent background. Log slow ops only. Keep previous branch/worktree metadata until fresh data arrives. |
| Active session git status subscription | Selecting a session | `getGitStatus`: `git status --porcelain -z --untracked-files=all`, `diff --numstat`, `check-ignore` | No direct indicator; drives downstream changes state | No | Polling every 2s can be expensive; errors are logged but invisible | Background with adaptive cadence. Do not show every poll. Expose "status stale" only after repeated failures or when user opens changes. |
| Changes panel branch diff summary | Session view open; every 30s; every git status update | `get_branch_diff_files`: default branch lookup, `git diff --name-status`, `--numstat`, status scan | Skeleton when no files; panel error on failure | Usually no | Repeated slow diffs will make the panel feel permanently busy; dock may be closed | Preserve last result. Show compact "refreshing" in panel header only while panel is visible. If hidden, do not surface unless failure affects a later user action. Consider slowing/reducing refresh after slow results. |
| Diff detail panel initial file list | Opening diff detail panel | `sendFetchRemotes`, then `get_branch_diff_files` | Panel-level loading/empty/error behavior | Yes, for reviewing | User intentionally opened review; they need to know why files are unavailable | Local panel progress. Show last cached list if available with "updating". Escalate timeout/error in panel, not globally. |
| Diff detail selected file | Selecting/navigating a file | `get_file_diff`: `git show base:path`, file read, optional staged `git show :path` | Diff area loads/fails per file | Yes, for selected file | Slow per-file diff can interrupt review repeatedly | Per-file inline loading in diff area. Cache last file diff. Do not block file list navigation. |
| Diff detail viewed-file change detection | Git status update for viewed files | Background `fetchDiff` for viewed files | No direct UI; marks changed badge | No | Can issue hidden diff reads for already-viewed files | Silent background. Cap concurrency and skip if a previous check is in flight. Surface only as a changed badge when complete. |
| Location picker path inspection | User selects/types a path | `inspect_path`: path stat, repo root detection via git | Picker waits; errors via toast | Yes, for opening a location | Selecting repo root can feel frozen before repo options appear | Inline picker progress after short delay, tied to selected path. Keep keyboard focus and allow cancel/back. |
| Location picker repo options load | Path inspection finds repo root | `get_repo_info`: current branch, head commit, default branch, worktree list | Picker switches to repo options after result; no intermediate repo-specific state | Yes | Large worktree lists/default branch lookup can delay opening | Inline "loading repo options" inside picker. If slow, show repo path and allow "open main repo now" if enough info exists. |
| Repo options refresh | User refreshes repo options | `get_repo_info` | Existing options remain; refresh flag passed to `RepoOptions` | No | This is already close to correct; only needs clearer stale/refresh state | Keep stale options visible. Small header spinner/text only. |
| Repo options create worktree | User submits create-worktree form | `create_worktree`: optional fetch remote branch, `git worktree add` | Form remains; errors toast/dialog callback; no explicit progress in form | Yes | Worktree add/fetch may take minutes; user needs cancellable progress | Inline form operation state. Disable conflicting create/delete/select actions, not whole picker. On success launch session. |
| Repo options delete worktree | User deletes from picker | `delete_worktree`: terminate sessions, `git worktree remove`, delete branch | Await then refresh repo info; limited row state | Yes for selected row, not whole picker | Slow delete can make picker ambiguous | Row-level pending state with optimistic "deleting..." and undo only if feasible. Keep other rows usable. |
| Close worktree cleanup delete | User closes session and chooses delete | `delete_worktree` | Prompt closes after await; failure only console logged | Yes, but narrow | User can lose feedback if delete hangs/fails | Keep prompt or toast-style progress until result. Show failure with retry/keep. |
| PR dashboard refresh | User refreshes PRs | GitHub, not git | Refresh button spinner/error icon | No | Not part of git slowness, but same async vocabulary | Leave mostly as-is. Do not mix with git operation UX. |
| Open PR into worktree | User clicks PR "New" | Fetch PR details, `ensure_repo` clone/fetch, `create_worktree_from_branch`, spawn session | No local progress; errors eventually alert/toast | Yes | This is the clearest "no right dock exists yet" case. Clone/fetch/worktree can be very long before any session appears | Global app-level job toast or launcher modal, not terminal/right dock. Show steps: fetching PR, ensuring repo, creating worktree, starting session. |
| Ensure repo | Open PR flow | `git clone` if missing, then `git fetch --all --prune` | Hidden inside open-PR promise | Yes | Longest operation; current UI has no durable status | Needs daemon operation lifecycle events. Frontend should show progress owned by the launcher/open-PR flow. |
| Fetch remotes | Diff detail open; ensure repo; explicit command | `git fetch --all --prune` | Hidden or panel warning | Sometimes | Can be long but not always blocking | If user opened diff detail, show panel sync state. If part of open PR, show launcher step. If background, stay quiet. |
| Worktree list pruning | Repo info/load worktrees | `git worktree prune`, `git worktree list --porcelain` | Hidden inside repo info/list events | No unless picker waits | Can delay repo options and branch metadata | Fold into repo-options state; never global. |
| Review loop snapshots | Start/end review loop iteration | `GetDefaultBranch`, `GetBranchDiffFiles` before and after reviewer work | Review loop bar has its own running/error state, but git snapshot time is not distinct | Yes for review loop startup/completion reporting | Slow snapshot can make review loop appear stuck before/after actual reviewer work | Keep inside review-loop panel state. Label as "capturing diff snapshot" only if slow. Do not show global git state. |
| Latent branch protocol commands | Currently protocol/daemon only; no active frontend caller found | `list_branches`, `get_default_branch`, `list_remote_branches` | No current UI surface | N/A | Future callers may accidentally inherit generic promise/no-UX behavior | Treat as modal/picker-owned if used for branch/worktree selection. Do not add global UI by default. |

## Current UI Pattern Buckets

- Button-local: PR refresh, PR approve/merge. Good for short explicit actions.
- Modal-local: Location picker, repo options, worktree cleanup prompt. This is where create/delete/open flows should report progress.
- Panel-local: Changes panel and diff detail. This is where diff/status/sync state belongs when the panel is visible.
- Silent background: branch monitor, git status polling, viewed-file change checks. These should keep prior data and avoid noisy progress.
- Missing global job surface: open PR/ensure repo before a session exists. This needs a lightweight launcher job surface that is not terminal or right-dock anchored.

## Proposed Fix Sequence

1. **State model first:** add a small frontend operation taxonomy and map existing calls to `background`, `panel`, `modal`, or `launcher` ownership. This can initially be frontend-only for request promises.
2. **Panel-local diff/status UX:** keep previous branch diff results during refresh, add subdued panel header state, and stop showing skeletons for routine refreshes after first load.
3. **Picker/worktree modal UX:** add operation state to repo options create/delete/refresh.
4. **Open PR launcher job:** show durable progress while `fetch_pr_details`, `ensure_repo`, `create_worktree_from_branch`, and `createSession` run before any terminal/right dock exists.
5. **Daemon lifecycle events:** add protocol-level `git_operation_started/updated/finished` only after the frontend ownership model is clear. Use this for true long-running git subprocesses, cancellation, and elapsed-time accuracy.
6. **Adaptive background behavior:** reduce refresh pressure when status/diff commands are repeatedly slow, and skip hidden panel refreshes where stale cached data is good enough.

## Open Questions

- Should hidden Changes panel refresh at all, or only refresh on open plus git status debounce?
- What threshold counts as "show slow state" for user-initiated actions: 500ms, 1s, or 2s?
- Which operations should be cancellable in the UI versus merely dismissible while they continue?
- Do we need a single global "jobs" tray eventually, or only launcher/modal/panel-local state for now?
