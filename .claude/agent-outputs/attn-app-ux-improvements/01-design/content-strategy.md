# Content Strategy: Attn App Improvements
**Agent:** Web Designer
**Date:** 2025-12-10
**Phase:** 1 - Design
**Project:** Claude Manager (attn app)

## Overview

Content strategy for PR actions, filesystem autocomplete, and sidebar shortcuts focuses on clarity, minimal text, and developer-friendly language.

## Copy Guidelines

### Tone & Voice
- **Direct and technical** - Users are developers working with code
- **Minimal verbosity** - Every word earns its place
- **Action-oriented** - Buttons are verbs, states are clear
- **No hand-holding** - Assume technical competency
- **Honest about errors** - Clear failure messages

### Examples of Good vs Bad Copy

**Good:**
- "Approve"
- "Merge PR"
- "Muted"
- "Directory not found"

**Bad:**
- "Would you like to approve this PR?" (too wordy)
- "Click here to merge" (obvious)
- "This PR has been successfully muted" (verbose)
- "Oops! We couldn't find that directory" (too casual)

## PR Actions Copy

### Button Labels

**Primary Actions:**
- "Approve" - Not "Approve PR" (context is clear)
- "Merge" - Not "Merge PR" (same reason)
- "Mute" - Not "Hide" or "Dismiss" (consistent with existing muted field)

**Icon-Only Actions (Drawer):**
- Use icons with tooltips
- Tooltip: "Approve PR #123"
- Tooltip: "Merge PR #123"
- Tooltip: "Mute PR #123"

**Repo-Level:**
- "Mute all" - Appears on repo header

### Loading States
- Button text changes to: (spinner icon, no text)
- Tooltip while loading: "Approving..."
- Alternative: Replace button content with spinner icon only

### Success States
- Button shows checkmark icon briefly (500ms)
- No success text needed (visual feedback sufficient)
- PR state updates in UI automatically

### Confirmation Dialogs

**Merge Confirmation:**
```
Title: "Merge PR #123?"
Body: "{PR title}"
Buttons: [Cancel] [Merge]
```

**Merge with Options (if needed later):**
```
Title: "Merge PR #123"
Body: "{PR title}"
Checkbox: "Delete branch after merge"
Buttons: [Cancel] [Merge]
```

**Repo Mute Confirmation:**
```
Title: "Mute all PRs from {repo}?"
Body: "You can unmute individual PRs later."
Buttons: [Cancel] [Mute All]
```

### Error Messages

Keep error messages under 100 characters when possible.

**Network Errors:**
- "Connection failed. Check your network."

**Authentication:**
- "GitHub authentication failed. Reconnect in settings." (if settings exist)
- "GitHub authentication required."

**Permissions:**
- "You can't approve your own PR."
- "You can't merge this PR. Insufficient permissions."
- "Approval required before merge."

**Rate Limiting:**
- "GitHub rate limit reached. Try again in {minutes}m."

**Generic:**
- "Failed to approve PR. Try again."
- "Failed to merge PR. Try again."

### Undo Toast

**Mute Undo:**
```
"PR #{number} muted [Undo]"
```

Duration: 5 seconds, then fade out.
Position: Bottom center (doesn't block content).

## Filesystem Autocomplete Copy

### Input Placeholder
- "Type path or search recent..."

This covers both use cases without being too long.

### Section Headers

**Directories Section:**
- "Directories" - Clear, not "Suggestions" or "Filesystem"

**Recent Section:**
- "Recent" - Existing pattern, keep consistency

### Empty States

**No Directories Found:**
```
"No directories found"
```

**No Recent Locations:**
```
"No recent locations"
```

**No Matches:**
```
"No matches. Press Enter to use path."
```

This teaches users they can use arbitrary paths.

### Error Messages

**Permission Denied:**
```
"Cannot access directory"
```

**Path Not Found:**
```
"Directory does not exist"
```

**Invalid Path:**
```
"Invalid path format"
```

**Read Error:**
```
"Error reading directory"
```

### Helper Text

**Breadcrumb Display:**
When user types `/Users/john/projects/`, show:
```
Current: ~/projects/
```

This helps users track where they are without cluttering input.

### Keyboard Hints (Footer)

Update existing footer to:
```
↑↓ navigate    Tab autocomplete    Enter select    Esc cancel
```

This adds "Tab autocomplete" to existing hints.

## Sidebar Shortcut Copy

### Footer Hint

**Current:**
```
⌘K drawer
```

**Updated:**
```
⌘K drawer    ⌘B sidebar
```

Simple addition, maintains existing pattern.

### Tooltips

**Collapse Button (expanded sidebar):**
```
title="Collapse sidebar (⌘B)"
```

**Expand Button (collapsed sidebar):**
```
title="Expand sidebar (⌘B)"
```

### Icon Button Labels (collapsed state)

No changes needed - existing tooltips already include shortcuts like "(⌘D)", "(⌘N)".

## Accessibility - Screen Reader Text

### PR Actions

**Approve Button:**
```
aria-label="Approve pull request #{number}: {title}"
```

**Merge Button:**
```
aria-label="Merge pull request #{number}: {title}"
```

**Mute Button:**
```
aria-label="Mute pull request #{number}"
```

**During Loading:**
```
aria-label="Approving pull request #{number}"
```

**After Success:**
```
aria-label="Pull request #{number} approved"
```

### Filesystem Autocomplete

**Input:**
```
aria-label="Directory path"
aria-describedby="picker-hints"
```

**Suggestions Container:**
```
role="listbox"
aria-label="Directory suggestions"
```

**Individual Suggestions:**
```
role="option"
aria-label="/Users/john/projects - john/projects"
```

(Full path, then shortened for clarity)

**Section Headers:**
```
<div role="group" aria-label="Recent directories">
```

**Active Item Announcement:**
```
aria-activedescendant="suggestion-{index}"
```

This announces which item is keyboard-focused.

### Sidebar Shortcut

**Collapse Button:**
```
aria-label="Collapse sidebar. Keyboard shortcut Command B"
```

**Expand Button:**
```
aria-label="Expand sidebar. Keyboard shortcut Command B"
```

## Microcopy Patterns

### Consistent Terminology

Throughout the app, use:
- "PR" not "pull request" (except in aria-labels for clarity)
- "Mute" not "hide" or "dismiss"
- "Directory" not "folder" (technical audience)
- "Session" not "terminal" or "instance"
- "Drawer" not "sidebar" for the attention panel

### Number Formatting
- PR numbers: `#{number}` (e.g., "#123")
- Counts: Just the number, no padding (e.g., "3" not "03")
- Time: "5m" not "5 minutes" for brevity

### State Labels
- "waiting" (lowercase, orange)
- "working" (lowercase, green)
- "muted" (lowercase, gray)
- "approved" (lowercase, green)
- "merged" (lowercase, purple)

## Content Hierarchy

### PR Row (Dashboard)
1. **Role icon** - Immediate visual classification (👀 reviewer, ✏️ author)
2. **PR number** - Secondary identifier (#123)
3. **Title** - Primary readable content
4. **Reason tag** - Why it needs attention (if author)
5. **Action buttons** - Clear actions (appear on hover)

Reading order: Icon → Title → Actions

### LocationPicker Results
1. **Section header** - Context (Directories / Recent)
2. **Item icon** - Visual type indicator (📁)
3. **Item name** - Primary label (folder name)
4. **Item path** - Secondary context (~/projects/app)

Reading order: Name → Path (top to bottom)

### Attention Drawer
1. **Section title** - Category (Review Requested)
2. **Count badge** - Quantity (3)
3. **Item list** - Individual PRs/sessions
4. **Action icons** - Quick actions (compact)

Reading order: Title + Count → Items → Actions

## Content Length Guidelines

### Maximum Lengths
- Button labels: 10 characters max
- PR titles in list: Truncate at 50 characters with ellipsis
- Error messages: 100 characters preferred, 150 max
- Tooltips: 60 characters max
- Toast messages: 80 characters max

### Truncation Strategy
- PR titles: Middle truncation for paths, end truncation for text
  - "Fix: Update login component to support..." → "Fix: Update login component to supp..."
  - "/Users/john/projects/app/src/components/..." → "/Users/.../components/..."
- Directory paths: Always show leaf directory
  - "/Users/john/projects/attn/app" → "~/projects/attn/app"

## Placeholder Copy for Development

### PR Actions (for testing)
```typescript
// Example PR data
const mockPR = {
  id: "pr_123",
  repo: "org/repo",
  number: 123,
  title: "Fix: Update terminal rendering for wide screens",
  role: "reviewer",
  reason: "review_requested"
};
```

### Filesystem Suggestions (for testing)
```typescript
// Example directory suggestions
const mockDirectories = [
  { path: "/Users/john/projects", label: "projects" },
  { path: "/Users/john/Documents", label: "Documents" },
  { path: "/Users/john/Downloads", label: "Downloads" },
];
```

### Error Messages (for testing)
```typescript
// Test each error state
const errors = {
  network: "Connection failed. Check your network.",
  auth: "GitHub authentication required.",
  permission: "Insufficient permissions.",
  rateLimit: "Rate limit reached. Try again in 15m.",
};
```

## Special Character Usage

### Icons in Copy
- ✓ for approved/success states
- ✗ for errors (if needed)
- ⌘ for Command key (Mac)
- Ctrl for Control key (Windows/Linux)
- ↑↓ for arrow keys
- → for navigation/flow indication

### Emoji Usage
- 👀 for reviewer role (existing pattern)
- ✏️ for author role (existing pattern)
- 📁 for directories (existing pattern)
- No other emojis needed - keep it professional

## Internationalization Considerations

While not implementing i18n now, structure copy to be i18n-friendly:

**Good (translatable):**
- Separate button text from icons
- Use complete sentences in error messages
- Avoid concatenating strings

**Bad (hard to translate):**
- "Mute" + " " + prNumber (concatenation)
- "PR #" + number + " muted" (embedded variable)

**Better:**
- Template: "{prNumber} muted" with prNumber = "PR #123"

This makes future i18n easier if needed.
