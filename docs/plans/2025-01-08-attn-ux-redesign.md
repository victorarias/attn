# Attn UX Redesign

## Overview

Attn is a developer attention hub. One place to see everything waiting for input, reducing context switching across terminals, browsers, and other tools.

### Core Purpose
- Surface things waiting for attention (sessions, PRs, later: Slack, todos, etc.)
- Central place to maintain focus
- Reduce context switching

## Layout: Dashboard + Drawer

Two modes of interaction:

### 1. Dashboard View (Home)
When no session is selected, show a dashboard with:
- **Sessions card** - Local sessions with state indicators
- **PRs card** - Grouped by repo, collapsible, with muting
- Future: Additional attention source cards

### 2. Session View
When a session is active:
- Sidebar shows sessions list (narrow)
- Terminal takes main area
- **Attention drawer** slides out from right on demand
- Badge in corner shows attention count

## Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| `⌘1-9` | Switch to session N |
| `⌘↑/⌘↓` | Previous/next session |
| `⌘K` or `⌘.` | Toggle attention drawer |
| `⌘J` | Jump to next waiting session |
| `⌘N` | New session (opens location picker) |
| `⌘W` | Close current session |
| `⌘D` or `Esc` | Go to dashboard |

Shortcuts should be visible in the UI (footer bar or drawer).

## Attention Drawer

Right-side panel that slides in/out:

### Sections
1. **Sessions Waiting** - Other sessions needing input
2. **PRs - Review Requested** - PRs where user is reviewer
3. **PRs - Your PRs** - User's PRs with feedback
4. **Muted** - Collapsed section showing muted repo count

### PR Organization
- Grouped by repository
- Repos are collapsible (click to expand/collapse)
- Repo-level muting (right-click or button)
- Shows: PR number, title (truncated), reason badge

## Location Picker

Custom directory picker replacing native file dialog.

### Modes

**Initial State**
- Empty input
- Shows recent locations (persisted)
- Shows favorites (if any)
- Arrow keys navigate, Enter selects

**Fuzzy Search**
- Type to filter across history + filesystem
- Matched characters highlighted
- History items scored higher

**Path Mode** (when input starts with `~` or `/`)
- Tab completion like shell
- Ghost text shows completion preview
- Tab completes to longest common prefix

**Directory Browser**
- After completing to a directory, shows contents
- Git repos highlighted with badge
- Breadcrumb navigation
- `..` to go up
- `⌘Enter` to select current directory

### Keyboard
| Key | Action |
|-----|--------|
| `↑/↓` | Navigate items |
| `Enter` | Select / open folder |
| `Tab` | Complete to suggestion |
| `⌘Enter` | Select current directory |
| `Esc` | Cancel |

### Persistence
- Recent locations stored in local storage
- Favorites can be pinned (future)

## Visual Design

### Aesthetic
- Industrial/utilitarian - this is a command center
- Dark theme (already established)
- Clear state indicators (orange = waiting, green = working)

### Key Elements
- Attention items should demand attention (orange accent, pulsing badge)
- Terminal maximized when in session
- Minimal chrome, maximum content

## Implementation Notes

### Data Flow
- Sessions: Local state (PTY) + daemon WebSocket
- PRs: Daemon WebSocket (from cm daemon)
- Location history: Local storage

### State Management
- Add drawer open/closed state
- Add muted repos list (persist to local storage)
- Add location history (persist to local storage)

### Components to Build
1. `AttentionDrawer` - Right slide-out panel
2. `LocationPicker` - Modal with input + results
3. `KeyboardShortcuts` - Global handler + display
4. `PRGroup` - Collapsible repo group
5. Update `Sidebar` - Simplify for sessions only
6. Update `App` - Add dashboard vs session view routing

## Sketches

Reference sketches in `app/sketches/`:
- `layout-options.html` - Initial layout exploration
- `option-a-session-active.html` - Drawer variations
- `location-picker.html` - Location picker states
