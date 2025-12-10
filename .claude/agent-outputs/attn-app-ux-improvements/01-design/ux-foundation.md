# UX Foundation: Attn App Improvements
**Agent:** Web Designer
**Date:** 2025-12-10
**Phase:** 1 - Design
**Project:** Claude Manager (attn app)

## Overview

This document outlines the UX foundation for three critical improvements to the attn app:
1. PR action buttons (approve, merge, mute)
2. Filesystem autocomplete for LocationPicker
3. Sidebar collapse keyboard shortcut visibility

## Component Hierarchy

### 1. PR Actions Component

**Dashboard View - PR Card:**
```
.pr-row
  ├── .pr-role (emoji)
  ├── .pr-number
  ├── .pr-title
  ├── .pr-reason (conditional)
  └── .pr-actions (NEW)
      ├── button.pr-action-btn[review] (already handled by link)
      ├── button.pr-action-btn[approve]
      ├── button.pr-action-btn[merge]
      └── button.pr-action-btn[mute]
```

**AttentionDrawer - PR Item:**
```
.attention-item (currently just a link)
  ├── .item-dot.pr
  ├── .item-name
  ├── .item-reason (conditional)
  └── .pr-actions-compact (NEW)
      ├── button.action-icon[approve]
      ├── button.action-icon[merge]
      └── button.action-icon[mute]
```

**Repo-Level Actions (Dashboard):**
```
.repo-header
  ├── .collapse-icon
  ├── .repo-name
  ├── .repo-counts
  └── .repo-actions (NEW)
      └── button.repo-action-btn[mute-all]
```

### 2. Filesystem Autocomplete Component

**Enhanced LocationPicker:**
```
.location-picker
  ├── .picker-header
  │   ├── .picker-title
  │   └── .picker-input-wrap
  │       ├── input.picker-input
  │       └── .picker-breadcrumb (NEW - shows current path)
  ├── .picker-results
  │   ├── .picker-section[filesystem] (NEW)
  │   │   ├── .picker-section-title "Directories"
  │   │   └── .picker-item[directory] (multiple)
  │   └── .picker-section[recent] (existing)
  │       ├── .picker-section-title "Recent"
  │       └── .picker-item[recent] (multiple)
  └── .picker-footer
      └── .shortcut (multiple)
```

### 3. Sidebar Collapse Shortcut

**Sidebar Footer Enhancement:**
```
.sidebar-footer (existing)
  ├── .shortcut-hint "⌘K drawer" (existing)
  └── .shortcut-hint "⌘B sidebar" (NEW)
```

## User Flows

### PR Actions Flow

**Primary Goal:** Allow users to take action on PRs without leaving the app.

**Flow 1: Approve PR from Dashboard**
1. User sees PR in Dashboard PRs card
2. User hovers over PR row → action buttons appear/fade in
3. User clicks "Approve" button
4. Button shows loading state (spinner)
5. Success: Button shows checkmark briefly, PR updates to show approved state
6. Error: Toast notification appears with error message
7. PR row may move to different section or update visually

**Flow 2: Merge PR from AttentionDrawer**
1. User opens drawer (⌘K)
2. User sees PRs needing attention
3. User clicks merge icon (compact action)
4. Modal confirmation appears: "Merge PR #123: Title? [Cancel] [Merge]"
5. User confirms
6. Loading state shown
7. Success: PR disappears from drawer (no longer needs attention)
8. Error: Error message shown in drawer

**Flow 3: Mute PR or Repo**
1. User wants to hide PR from attention lists
2. User clicks mute button (eye-slash icon)
3. PR immediately fades out and removes from list
4. Undo toast appears for 5 seconds: "PR muted [Undo]"
5. If undo clicked, PR reappears
6. Repo-level mute: Same flow but affects all PRs from that repo

### Filesystem Autocomplete Flow

**Primary Goal:** Help users navigate to any directory on their system, not just recent locations.

**Flow 1: Navigate to New Directory**
1. User opens LocationPicker (clicks + New)
2. Input is focused, empty
3. User types "/" → sees root directories (/, /Users, /Applications, etc.)
4. User types "/Users/" → sees user directories
5. User types "/Users/jo" → filters to directories starting with "jo" (john, joel, etc.)
6. User presses ↓ to highlight first match
7. User presses Tab → input autocompletes to "/Users/john/"
8. User continues typing or presses Enter to select highlighted directory

**Flow 2: Recent Location with Autocomplete**
1. User opens LocationPicker
2. Sees "Recent" section with history
3. Sees "Directories" section showing current input path's contents
4. User types part of recent path → both sections filter
5. User can select from either section

**Flow 3: Navigate Parent Directories**
1. User has typed "/Users/john/projects/app"
2. User presses Backspace to delete "app"
3. Autocomplete updates to show directories in /Users/john/projects/
4. User can navigate up the tree this way

### Sidebar Collapse Shortcut Flow

**Primary Goal:** Make the existing ⌘B shortcut discoverable.

**Flow:**
1. User sees sidebar footer with shortcuts
2. User reads "⌘B sidebar" hint
3. User presses ⌘B → sidebar collapses
4. Collapsed sidebar shows expand button with tooltip
5. User presses ⌘B again → sidebar expands

## Responsive Strategy

**Desktop (primary target):**
- PR actions shown on hover to reduce visual noise
- Filesystem autocomplete shows full paths
- Sidebar always visible (unless user collapses)

**Tablet (1024px - 1366px):**
- PR actions always visible (touch target needs)
- Filesystem autocomplete maintains full functionality
- Sidebar behavior unchanged

**Mobile (<1024px):**
- Not primary target for this Tauri desktop app
- If needed: PR actions as dropdown menu
- Filesystem autocomplete optimized for smaller screens

## Accessibility Considerations

### Keyboard Navigation

**PR Actions:**
- Tab through action buttons
- Enter/Space to activate
- Focus visible (ring outline)
- Confirmation modals support Escape to cancel

**Filesystem Autocomplete:**
- Arrow keys to navigate suggestions
- Tab to autocomplete/cycle through suggestions
- Enter to select
- Escape to close
- Home/End to jump to first/last suggestion
- Keyboard focus never lost

**Sidebar Shortcut:**
- ⌘B works globally (already implemented)
- Tooltip on hover for discoverability
- Focus indicator on collapse/expand buttons

### Screen Reader Support

**PR Actions:**
- Buttons have aria-label: "Approve PR #123: Title"
- Loading state announced: "Loading"
- Success/error announced via aria-live region
- Muted PRs announced: "PR #123 muted"

**Filesystem Autocomplete:**
- Input has aria-label: "Directory path"
- Suggestions list has role="listbox"
- Each suggestion has role="option"
- aria-activedescendant points to highlighted suggestion
- Section headers use aria-label for context
- Count of suggestions announced

**Sidebar Shortcut:**
- Hint text readable by screen readers
- Collapse/expand button has aria-label: "Collapse sidebar (Command+B)"
- State change announced

### Visual Indicators

**PR Actions:**
- Color contrast meets WCAG AA (4.5:1 minimum)
- Loading spinner visible against background
- Success/error states use both color and icon
- Focus rings on all interactive elements

**Filesystem Autocomplete:**
- Selected item has distinct background
- Keyboard focus separate from mouse hover
- Path breadcrumb helps orientation
- Directory vs file distinguished by icon

**Sidebar Shortcut:**
- Text contrast meets WCAG AA
- kbd element styling distinct
- Visible in both expanded and collapsed states

## State Management Needs

### PR Actions
- PR approval state (pending, loading, approved, error)
- PR merge state (pending, loading, merged, error)
- PR mute state (active, muted)
- Undo stack for mute actions (5 second window)
- Error messages per PR

### Filesystem Autocomplete
- Current input path
- Parsed directory to query
- Filesystem suggestions (loading, loaded, error)
- Recent locations (existing)
- Selected index (keyboard navigation)
- Autocomplete mode (directory suggestions vs filtered recent)

### Sidebar Shortcuts
- Collapsed state (existing)
- No additional state needed

## Technical Considerations

### PR Actions
- Requires GitHub API integration (REST API)
- Needs OAuth token management
- Rate limiting consideration (GitHub API limits)
- Optimistic updates vs confirmed updates
- Undo mechanism for mute (local storage)

### Filesystem Autocomplete
- Tauri filesystem API for reading directories
- Permission handling for protected directories
- Performance for large directories (limit to 50 suggestions)
- Path parsing and validation
- Platform differences (Windows vs Unix paths)

### Sidebar Shortcut
- No technical changes needed
- Pure UI update

## Error Handling

### PR Actions
- Network errors: "Failed to connect. Check your connection."
- Auth errors: "Authentication failed. Please reconnect GitHub."
- Permission errors: "You don't have permission to merge this PR."
- Rate limit: "Rate limited. Try again in X minutes."
- Generic: "Something went wrong. Please try again."

### Filesystem Autocomplete
- Permission denied: "Cannot access directory. Permission denied."
- Directory not found: "Directory does not exist."
- Invalid path: "Invalid path format."
- Read error: "Error reading directory contents."

### Sidebar Shortcut
- No error states (pure UI)

## Success Metrics

### PR Actions
- Users can approve/merge PRs without opening browser
- Reduced time from PR notification to action
- Mute functionality reduces unwanted attention noise
- Error recovery is clear and actionable

### Filesystem Autocomplete
- Users can navigate to any directory on system
- Reduced clicks to reach target directory
- Combined recent + filesystem suggestions improve efficiency
- Keyboard navigation is fast and predictable

### Sidebar Shortcut
- Users discover ⌘B through visible hint
- Sidebar collapse usage increases
- No support questions about "how to collapse sidebar"
