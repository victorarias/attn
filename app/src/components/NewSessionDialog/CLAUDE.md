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
