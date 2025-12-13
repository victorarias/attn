# Projects Directory Setting Design

## Overview

Add a configurable "projects directory" setting that maps GitHub repos to local paths, enabling the PR "Open" button to create worktrees automatically.

## User Flow

1. User configures projects directory in Settings (e.g. `~/projects`)
2. User clicks "Open" on a PR from `owner/repo-name`
3. System finds local repo at `~/projects/repo-name`
4. System creates worktree for PR branch
5. System opens terminal session in worktree

## Database

New `settings` table (key-value store):

```sql
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT
);
```

Initial key: `projects_directory` storing absolute path.

## Store Methods

```go
func (s *Store) GetSetting(key string) string
func (s *Store) SetSetting(key, value string)
```

## Protocol

Version bump: 5 → 6

New commands:
- `get_settings` → `{ projects_directory: string | null }`
- `set_setting` with `{ key: string, value: string }` → success/error result

New event:
- `settings_updated` - broadcast when settings change

## Frontend

### useDaemonSocket.ts
- Add `settings` state received on connect
- Add `sendSetSetting(key, value)` method

### SettingsModal.tsx
- Add "Projects Directory" section
- Text input with current path or "Not configured" placeholder
- Tauri file dialog for directory picker
- Save on blur/enter

### App.tsx - handleOpenPR
1. Check `projects_directory` is set
2. Extract repo name from `owner/repo-name`
3. Build path: `{projectsDir}/{repoName}`
4. Verify `.git` exists (Tauri fs)
5. Get PR branch: `gh pr view {n} --repo {repo} --json headRefName`
6. Create worktree via `sendCreateWorktree`
7. Create session in worktree path

## Error Handling

- No projects directory configured → "Configure projects directory in Settings"
- Repo not found locally → "Repo not found at {path}"
- Worktree creation fails → Show error from daemon
- gh CLI fails → Show error message

## Implementation Tasks

1. Add settings table to SQLite schema
2. Add store GetSetting/SetSetting methods
3. Add protocol messages (bump version to 6)
4. Add daemon handlers for settings
5. Update useDaemonSocket with settings state
6. Update SettingsModal with projects directory input
7. Implement handleOpenPR flow in App.tsx
