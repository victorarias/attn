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

## Key Behaviors

**Keyboard navigation is critical** - users should be able to navigate entirely with keyboard:
- Tab to drill into directories
- Arrow keys to select from suggestions
- Enter to confirm selection

### Tab Key
- Accepts the ghost text → fills input with full path
- Triggers new suggestions for that path
- `selectedIndex` resets to 0 (first suggestion selected)
- **Mental model:** "Accept this, show me what's inside"

### Arrow Keys (↑/↓)
- Navigate through suggestions list
- Changes `selectedIndex` → changes ghost text
- Does NOT change the input value
- **Mental model:** "Show me other options at this level"

### Enter Key (Current Logic)
```typescript
const pathToSelect = (ghostText && ghostText.startsWith(value))
  ? ghostText      // Accept ghost text as completion
  : (value || ghostText);  // Use typed value
```
**Mental model:** "Confirm my selection"

## Three Navigation Patterns

### 1. Autocomplete (typing + Enter)
User types partial path, Enter completes it:
```
Type: ~/projects/att
Ghost: n/
Enter → ~/projects/attn/  ✓ (correct - ghost is completion of typing)
```

### 2. Drill-down (Tab + Enter)
User Tabs through directories, Enter confirms:
```
Tab → ~/projects/
Tab → ~/projects/victor/
Tab → ~/projects/victor/attn/
Ghost: .beads (first child)
Enter → ~/projects/victor/attn/  ✗ (BUG - opens .beads instead)
```

### 3. Browse (Arrow + Enter)
User navigates with arrows, Enter selects:
```
Value: ~/projects/
Arrow ↓ → ghost: ~/projects/victor/
Arrow ↓ → ghost: ~/projects/other/
Enter → ~/projects/other/  ✓ (correct - user explicitly navigated)
```

## Known Issue: Tab-then-Enter Behavior

**Scenario:**
1. User at `~/projects`
2. Tab → value becomes `~/projects/victor/`
3. Tab → value becomes `~/projects/victor/attn/`
4. Ghost text shows `.beads` (first child of `attn/`)
5. User presses Enter expecting to select `attn/`
6. **Bug:** Selects `.beads` instead

**Root Cause:**
Enter logic treats "Tab-completed path" same as "user-typed partial path":
- Both result in `ghostText.startsWith(value) === true`
- No way to distinguish "user typed att" from "user Tabbed to attn/"

**User's Mental Model:**
- Tab = "accept this level, show me what's inside"
- Enter = "confirm the current path"

**Current Behavior:**
- Tab = "fill in ghost text, get new suggestions"
- Enter = "accept currently selected suggestion" (which is now a child)

## Proposed Fix

Track whether user has **intentionally selected** since last Tab:

```typescript
// In LocationPicker state
hasSelectedSinceTab: boolean

// On Tab:
setState({ hasSelectedSinceTab: false, inputValue: ghostText })

// On typing:
setState({ hasSelectedSinceTab: true })

// On arrow key navigation:
setState({ hasSelectedSinceTab: true })

// On Enter (in PathInput - needs prop from LocationPicker):
const pathToSelect = (ghostText && ghostText.startsWith(value) && hasSelectedSinceTab)
  ? ghostText      // User intentionally selected, accept ghost
  : value;         // User just tabbed, confirm current path
```

This allows:
- Type "att" → ghost "n/" → Enter accepts "attn/" ✓ (typed = intentional)
- Arrow to "other/" → Enter accepts "other/" ✓ (arrow = intentional)
- Tab to "attn/" → ghost ".beads" → Enter confirms "attn/" ✓ (no selection = use value)

**Key insight:** The distinction is between:
- **Intentional selection** (typing or arrow navigation) → use ghost text
- **Auto-selection** (Tab resets selectedIndex to 0) → use current value

## State Flow

```
User types → PathInput.onChange → LocationPicker.handlePathInputChange
           → setState({ inputValue })
           → useFilesystemSuggestions fetches
           → selectedIndex resets to 0
           → ghostText recalculated
           → PathInput receives new ghostText prop

User Tabs  → PathInput.handleKeyDown
           → onChange(ghostText)
           → (same flow as typing)

User Enter → PathInput.handleKeyDown
           → onSelect(pathToSelect)
           → LocationPicker.handleSelect
           → Check if git repo → show RepoOptions or close
```

## Edge Cases

| Scenario | ghostText.startsWith(value) | Expected Behavior |
|----------|----------------------------|-------------------|
| User types "att", ghost "n/" | true | Enter → "attn/" |
| User Tabs to "attn/", ghost ".beads" | true | Enter → "attn/" (BUG: currently selects ".beads") |
| Ghost doesn't match value | false | Enter → use value |
| Empty input, ghost exists | true | Enter → ghost text |
| No suggestions | N/A | Enter → use typed value directly |
