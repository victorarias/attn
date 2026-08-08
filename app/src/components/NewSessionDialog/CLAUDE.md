# NewSessionDialog Components

## Component Hierarchy

```
LocationPicker.tsx (state owner)
├── PathInput.tsx (text input with ghost text)
└── RepoOptions.tsx (shown when path is a git repo)
```

## Ghost Text System

Ghost text shows what the user will get if they press Tab or Enter.

**Source:** `LocationPicker.getSelectedPath()` returns the full path at `selectedIndex`.

**Display:** `PathInput` shows only the untyped portion:
```typescript
const visibleGhost = ghostText.startsWith(value)
  ? ghostText.slice(value.length)  // Show remaining portion
  : '';                             // Hide if doesn't match
```

## Keyboard Behavior Contract

Keyboard navigation is critical — users must be able to navigate entirely with keyboard.

- **Tab** — accepts ghost text, fills the input with the full path, fetches suggestions for that path, and resets `selectedIndex` to 0. Mental model: "accept this, show me what's inside".
- **Arrow keys (↑/↓)** — move through suggestions, changing ghost text but NOT the input value. Mental model: "show me other options at this level".
- **Enter** — confirms. What it confirms depends on whether the user made an *intentional selection* since the last Tab.

### The `hasSelectedSinceTab` rule

Tab auto-selects the first child suggestion, so after Tabbing into a directory the ghost text shows a child the user never chose. Enter must distinguish that from a deliberate choice:

- Typing or arrow navigation sets `hasSelectedSinceTab = true` → Enter accepts the ghost text (it completes what the user chose).
- Tab sets `hasSelectedSinceTab = false` → Enter confirms the current input value, ignoring the auto-selected child ghost.

```typescript
// PathInput.tsx
const pathToSelect = (ghostText && ghostText.startsWith(value) && hasSelectedSinceTab)
  ? ghostText
  : value;
```

Regression to guard: Tab into `~/projects/victor/attn/` (ghost shows first child like `.beads`), press Enter — must select `attn/`, not the child. Type `att` (ghost `n/`), press Enter — must select `attn/`.

## Selection Flow

`PathInput` calls `onSelect(path)` → `LocationPicker.handleSelect` checks whether the path is a git repo → shows `RepoOptions` or closes.

## RepoOptions focus zones

Creating a worktree is the dominant reason to reach this step, so the create form is **always expanded** at the top of the chooser with a generated name (`worktreeNames.ts`) pre-filled and selected. Existing destinations sit below it.

Focus is modelled as two zones rather than one index list:

- `create` — the name input holds focus. Only `Enter` (create), `Tab` (toggle start point), `↓` (into the destination list), `⌃R`/`⌘R` (reroll the name), and `Escape` (back) are intercepted; every other key must keep reaching the text input, so the destination shortcuts (`D`, `R`, `1`–`9`) are deliberately inert here.
- `destinations` — the list behaves as it always has: arrow keys commit as they move, `Enter` opens, `D` deletes, `R` refreshes, digits jump. `↑` off the top returns to the create form.

The initial zone is `create` **except** in two cases. When the incoming `selectedPath` resolves to a specific worktree (index > 0): typing an exact worktree path is an explicit "open this one", so Enter must still open it. And when `preferredDestination` is `main_repo`: the chooser opens on the main repo row so Enter reuses the checkout.

## Remembered destination

A repo is habitually worked on in a fresh worktree or habitually in its main checkout, so `LocationPicker` records the last deliberate choice per repo and feeds it back as `preferredDestination`. The two recorded values are `new_worktree` (a worktree was created) and `main_repo` (the checkout was opened); opening an *existing* worktree is a one-off and writes nothing.

Storage is the daemon `settings` table, under `new_session_destination_<scope>_<repoRoot>` where scope is `local` or `endpoint_<id>` — the same path on this machine and on a remote endpoint are different checkouts. Unset means `new_worktree`, the default the picker has always had. The daemon validates the value (`validateNewSessionDestination` in `internal/daemon/ws_settings.go`); a new value has to be accepted there too, or `set_setting` rejects it.

The way back is the picker itself: choosing the other destination overwrites the memory, and `↑` from the main repo row returns to the create form.

Regressions to guard:
- Repo root → Enter creates a worktree without any typing.
- Exact worktree path → Enter opens that worktree, and `onCreateWorktree` is not called.
- A generated name never collides with an existing branch (`generateWorktreeName` takes the taken list). `RepoInfo` only carries the current branch and branches with an attached worktree, so an ordinary local branch with no worktree is invisible to that list; `attemptCreateWorktree` rerolls and retries on git's "branch already exists" failure as a backstop (`isBranchAlreadyExistsError` in `worktreeNames.ts`).
- `.repo-options-destinations` keeps a `min-height` floor; without it the always-open form collapses the destination list to nothing in a small window.
