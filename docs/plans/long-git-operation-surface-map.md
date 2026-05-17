# Long Git Operation Surface Map

Date: 2026-05-15
Last updated: 2026-05-17

Purpose: map every product surface that waits on git today before choosing UI patterns. The problem is not one spinner. Different surfaces need different treatment depending on whether the user is blocked, whether existing data can stay visible, and whether the surface is even mounted.

## Recent Work Coverage

Recent PRs already cover part of the giant-repo latency problem:

- Changes panel branch diff refreshes are demand-driven and coalesced while the panel is visible.
- Diff detail keeps cached content visible and caps background viewed-file checks.
- Active-session git status refreshes are coalesced and slow full status scans fall back to a limited tracked-only result.
- Git status, branch-diff, and file-diff reads are routed through a daemon repo coordinator, with branch-diff snapshots owned per repo/base ref and in-flight work shared across connected clients.

The remaining latency gap is not that session navigation blocks on Git. It is that slow repositories can still leave panel-owned Git data in a first-load state when the daemon has no prior snapshot. Clients should keep rendering daemon-owned state and avoid adding independent Git intelligence.

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

## Current Git Refresh Source Map

This section maps the actual producers of git work today. It is intentionally lower-level than the surface inventory because the same UI surface can be fed by several refresh paths with different policy needs.

| ID | Producer | Trigger | Code path | Git work | Current behavior | Classification |
| --- | --- | --- | --- | --- | --- | --- |
| R1 | Session registration branch metadata | CLI register or websocket PTY spawn registers a session | `internal/daemon/daemon.go` `handleRegister`; `internal/daemon/ws_pty.go` spawn registration | `GetBranchInfo`: `rev-parse --is-inside-work-tree`, `symbolic-ref --short HEAD`, fallback `rev-parse --short HEAD`, `rev-parse --git-dir` | Synchronous inside registration path; failures ignored; session appears with missing branch metadata if git lookup fails | Correctness-adjacent background. Do not show progress. Keep registration moving if metadata is slow or unavailable. |
| R2 | Session branch monitor | Daemon startup, then every 15s for all stored sessions | `internal/daemon/daemon.go` `monitorBranches` / `checkAllBranches` | Same `GetBranchInfo` sequence as R1 for each session | Silent polling; broadcasts sessions only when branch/worktree metadata changed | Silent background. Candidate for measurement and daemon-side backoff, especially across many sessions. |
| R3 | Active session git status subscription | Frontend enters session view and active session has a directory; daemon sends immediate result then polls every 2s | `app/src/App.tsx` active-session subscribe effect; `internal/daemon/ws_git.go` `sendGitStatusUpdate`; `internal/daemon/gitstatus.go` `getGitStatus` | `git status --porcelain -z --untracked-files=all`; optional filesystem walk for collapsed untracked dirs; `git check-ignore --stdin`; `git diff --numstat`; `git diff --numstat --cached` | Daemon hashes and suppresses unchanged events, but still runs the commands every 2s. Errors are logged and not sent unless status parsing returns a structured "not a git repo" status. | Silent active-session background. Needs adaptive cadence before UX. Never show every poll. |
| R4 | Changes panel branch diff, initial and interval refresh | Any session view with active repo directory; immediately and every 30s | `app/src/App.tsx` `refreshBranchDiff` interval effect | `get_branch_diff_files`; daemon gets default branch if none supplied, then `GetBranchDiffFiles` | Runs from app-level state, not dock visibility. Keeps previous result after the first load; exposes loading/refreshing/error only when `ChangesPanel` renders. | Panel-owned data but currently app-level producer. First adaptive target: skip or delay when Changes panel is closed. |
| R5 | Changes panel branch diff on status update | Every `git_status_update` for the active repo | `app/src/App.tsx` gitStatus effect calls `refreshBranchDiff` | Same as R4 | Every status change schedules branch diff refresh. `useDaemonSocket` dedupes in-flight requests by directory, but completed slow diffs can still be repeated on every status change. | Panel-owned reactive refresh. Must be debounced/backed off separately from R3. |
| R6 | Diff detail branch file list, local refs path | Opening Diff Detail panel without cache; delayed 150ms unless remote sync wins | `app/src/components/DiffDetailPanel.tsx` open effect `runLocalDiff` | `get_branch_diff_files` with default base ref | Panel shows loading only if no cached list. Local result is ignored if newer remote-ref result lands first. | User-visible panel load. Do not skip when panel is open; can use cached stale data while refreshing. |
| R7 | Diff detail remote sync and refreshed branch file list | Opening Diff Detail panel | `DiffDetailPanel` open effect `sendFetchRemotes(...).then(sendGetBranchDiffFiles)` | `git fetch --all --prune`, then same branch diff as R6 | Shows panel sync state/warning. Cache is shown immediately when present. | User-visible panel sync. Keep local fallback; consider making fetch opt-in/backed off for slow repos later. |
| R8 | Diff detail selected-file diff | Selected file changes, base ref changes, or selected file stats key changes | `app/src/App.tsx` `fetchDiffForReview`; `DiffDetailPanel` selected-file effect; `internal/daemon/ws_git.go` `handleGetFileDiff` | `git show base:path`; optionally `git show :path`; filesystem read for working copy | First view shows per-file loading; later updates avoid flicker. Errors appear in diff area. | User-blocking panel action. Do not background-throttle. Cache last successful file diff where possible. |
| R9 | Diff detail viewed-file change detection | Every active-session `git_status_update` while Diff Detail panel has viewed files | `DiffDetailPanel` viewed-files effect | One `get_file_diff` per viewed non-selected file still in the file list | Silent background fan-out; no in-flight cap; errors ignored; only changed badge is surfaced | Silent panel background. Needs concurrency/in-flight guard before cadence tuning. |
| R10 | Location picker path inspection | User submits a path from the new-session picker | `app/src/components/LocationPicker.tsx` `handleSelectPath`; `internal/daemon/ws_picker.go` `inspect_path` | Filesystem stat/canonicalization plus repo root detection via `ResolvePickerRepoTarget` / `rev-parse --show-toplevel` / worktree main repo detection | Picker shows inline operation text (`Inspecting path`). Request generation prevents stale results applying. | User-blocking modal action. No background backoff; keep keyboard focus/cancel behavior. |
| R11 | Location picker repo options initial load | Path inspection resolves a repo root | `LocationPicker` `handleSelectPath`; `internal/daemon/branch.go` `handleGetRepoInfoWS` | `GetCurrentBranch`, `GetHeadCommitInfo`, `GetDefaultBranch`, `ListWorktrees` (`worktree prune`, `worktree list --porcelain`) | Picker shows inline operation text (`Loading repo options`). On failure, error is shown and direct launch continues. | User-blocking modal action, but stale options can be reused once loaded. |
| R12 | Repo options manual refresh | User presses refresh in repo options | `LocationPicker` `handleRefresh`; `RepoOptions` refresh prop | Same as R11 | Existing repo options stay visible; refresh flag passed to `RepoOptions`; errors logged | Explicit modal refresh. Run immediately; keep stale data visible. |
| R13 | Repo options create worktree | User submits create-worktree form | `LocationPicker` `handleCreateWorktree`; `RepoOptions`; `internal/daemon/worktree.go` `handleCreateWorktree` | Optional `ListRemotes`, optional `fetch remote branch`, `git worktree add`; store update and worktree event | Row/form operation state exists. On success launches session; on failure reports error. | User-blocking modal action. Covered by long-operation UX; not part of background adaptive policy. |
| R14 | Repo options delete worktree and refresh | User confirms delete from repo options | `RepoOptions` delete state; `LocationPicker` delete callback; `internal/daemon/worktree.go` `handleDeleteWorktree`; then `get_repo_info` | Resolve main repo/list worktrees; terminate sessions; `git worktree remove` or prune; optional `git branch -D`; then R11 work | Row-level delete state, then repo-options refresh. | User-blocking row action followed by explicit modal refresh. Keep localized. |
| R15 | Close worktree cleanup delete | User closes a session and confirms cleanup | `app/src/App.tsx` cleanup prompt; `WorktreeCleanupPrompt`; daemon `delete_worktree` | Same delete path as R14 | Prompt collapses to small progress surface if slow; failure keeps retry/keep options | User-blocking cleanup action. Already localized; keep out of adaptive refresh policy. |
| R16 | Open PR launcher ensure repo | User opens PR into a worktree | `app/src/hooks/useOpenPR.ts`; `internal/daemon/branch.go` `handleEnsureRepoWS` | `git clone` if missing; `git fetch --all --prune` | Launcher progress step exists; errors are classified for retry/reporting | User-blocking launcher action. Needs daemon lifecycle coverage, not background throttling. |
| R17 | Open PR launcher create worktree from branch | After R16 succeeds | `useOpenPR`; `internal/daemon/branch.go` `handleCreateWorktreeFromBranchWS` | `git worktree add` from local or remote branch | Launcher progress step exists; create result resolves the job | User-blocking launcher action. Needs daemon lifecycle coverage, not background throttling. |
| R18 | Diff/review loop snapshots | Review loop starts and each iteration computes change stats | `internal/daemon/review_loop.go` `captureReviewLoopSnapshot`; `computeReviewLoopIterationChangeStats` | `GetDefaultBranch`; `GetBranchDiffFiles` | Work is folded into review-loop running state; snapshot phase is not separately visible | Correctness-critical workflow work. Do not skip; show review-loop-local slow phase if needed. |
| R19 | Latent branch lookup protocol commands | No active frontend caller found in current app code | `list_branches`, `get_default_branch`, `list_remote_branches` websocket handlers | Branch list/default/remote branch metadata commands | Promise/result plumbing exists; no active UI surface owns it today | Future modal/picker-owned. Any new caller must declare ownership before use. |

## Refresh Classification

### User-Blocking

These operations directly answer a user action. They can be slow, but they should run immediately, report progress in the owning surface, and fail locally with retry/keep options where appropriate.

- R8 selected-file diff in Diff Detail.
- R10 path inspection.
- R11 repo options initial load after path inspection.
- R13 create worktree from repo options.
- R14 delete worktree from repo options.
- R15 close-session worktree cleanup.
- R16 ensure repo for Open PR.
- R17 create worktree for Open PR.
- R18 review-loop snapshots, because the review loop depends on the snapshot being accurate.

### Visible Panel Refresh

These refreshes should keep stale data visible and only show subtle state in the panel that owns the data. They should not create global operation UI.

- R4 Changes panel initial/interval branch diff when the Changes panel is visible.
- R5 Changes panel branch diff after active git status changes, when the Changes panel is visible.
- R6 Diff Detail local branch file list.
- R7 Diff Detail remote sync and refreshed branch file list.

### Hidden Or Background Refresh

These are the first candidates for adaptive behavior. The user did not ask for them at that moment, and loud progress would be noise.

- R2 branch monitor across all stored sessions.
- R3 active-session git status polling.
- R4 Changes branch diff interval while the Changes panel is closed.
- R5 Changes branch diff after git status while the Changes panel is closed.
- R9 viewed-file change detection fan-out.

### Metadata That Should Stay Silent

These paths update useful labels/badges but should not block the main product experience.

- R1 registration-time branch metadata.
- R2 branch monitor branch/worktree metadata.
- R19 latent branch lookup commands until a concrete UI caller owns them.

## Adaptive Policy Boundaries

- Start adaptive work with R4/R5 hidden Changes refreshes. They are expensive, repeated, and currently detached from dock visibility.
- Measure R3 before changing cadence. It feeds several downstream behaviors, so backing it off blindly can make the app feel stale in surprising places.
- Add an in-flight/concurrency guard for R9 before any timing policy. A single status update can fan out to multiple file diffs.
- Do not apply background backoff to R8, R10, R11, R13-R18. Those are user-blocking or correctness-critical.
- Treat R7 fetch-remotes separately from branch diff. It is panel-visible, but network fetch cost and failure semantics differ from local diff cost.

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
