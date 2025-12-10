# Design Summary: Attn App UX Improvements
**Agent:** Web Designer
**Date:** 2025-12-10
**Phase:** 1 - Design
**Project:** Claude Manager (attn app)

## Overview

This design specification covers three critical UX improvements for the attn app, a Claude Code session manager built with Tauri + React. The improvements focus on enabling actionable PR workflows, enhancing path navigation, and improving keyboard shortcut discoverability.

## High-Level Design Vision

**From:** Passive monitoring tool (see sessions and PRs, click links to act elsewhere)
**To:** Active workflow hub (act on PRs directly, navigate filesystem efficiently, discover shortcuts naturally)

### Design Principles

1. **Minimal friction** - Reduce steps from attention to action
2. **Keyboard-first** - Every action has a keyboard path
3. **Non-blocking feedback** - Loading/success states don't interrupt flow
4. **Discoverable power** - Advanced features visible but not overwhelming
5. **Developer-focused** - Technical audience, no hand-holding

## The Three Improvements

### 1. PR Actions (Approve, Merge, Mute)

**Problem:** Users see PRs that need attention but must open browser, authenticate, navigate to PR, then act. Context switching kills productivity.

**Solution:** Add action buttons directly to PR items in both Dashboard and AttentionDrawer.

**Key Decisions:**

- **Placement:** Buttons appear on hover in Dashboard (reduce visual noise), always visible in Drawer (touch-friendly)
- **Actions:** Approve (green checkmark), Merge (purple merge icon), Mute (gray eye-slash)
- **Confirmation:** Merge requires confirmation modal, Approve is immediate, Mute has 5-second undo
- **Feedback:** Loading spinner → Success checkmark (600ms) → PR updates in place
- **Repo-level:** "Mute all" button on repo headers to bulk-mute noisy repositories

**User Flow:**
```
User sees PR → Hovers → Clicks "Approve" → Loading → Checkmark → PR state updates
```

**Technical Requirements:**
- GitHub REST API integration (`POST /repos/{owner}/{repo}/pulls/{number}/reviews`)
- OAuth token management
- Optimistic UI updates
- Local storage for mute state
- Undo mechanism (5-second window)

**Success Metrics:**
- Reduced time to PR action (measure clicks to action)
- Increased PR action rate (more approvals/merges via app)
- Mute usage indicates noise reduction

### 2. Filesystem Autocomplete

**Problem:** LocationPicker only shows recent history. Users can't navigate to new directories without typing full paths or manually remembering them.

**Solution:** Add real-time filesystem directory suggestions as user types, with keyboard navigation and Tab autocomplete.

**Key Decisions:**

- **Dual-mode:** Show both filesystem suggestions AND recent locations
- **Smart parsing:** Parse input to determine which directory to query (e.g., `/Users/j` queries `/Users/`)
- **Keyboard nav:** Arrow keys navigate, Tab autocompletes, Enter selects
- **Visual hierarchy:** Directories section first (primary), Recent section second (supplementary)
- **Performance:** Limit to 50 suggestions, debounce queries, handle permissions gracefully
- **Breadcrumb:** Show "Current: ~/path/" to help users track location

**User Flow:**
```
User clicks "+ New" → Types "/Use" → Sees /Users/ → Types "/" → Sees user dirs → Arrows down → Tab completes → Enter selects
```

**Technical Requirements:**
- Tauri filesystem API (`readDir`)
- Path parsing (handle ~, /, ./, ../)
- Permission error handling
- Platform path differences (Windows vs Unix)
- Debouncing (150ms) to avoid excessive queries

**Success Metrics:**
- Users navigate to non-recent directories easily
- Reduced frustration with path entry
- Faster session creation overall
- Keyboard usage increases (Tab autocomplete)

### 3. Sidebar Collapse Shortcut Visibility

**Problem:** ⌘B keyboard shortcut exists to collapse sidebar but isn't visible in UI. Users don't know it exists.

**Solution:** Add "⌘B sidebar" hint to sidebar footer, matching existing "⌘K drawer" pattern.

**Key Decisions:**

- **Placement:** Sidebar footer (existing pattern location)
- **Format:** "⌘B sidebar" (matches "⌘K drawer" style)
- **Visibility:** Shown in both expanded and collapsed states
- **No technical changes:** Pure UI update, shortcut already works

**User Flow:**
```
User sees footer → Reads "⌘B sidebar" → Presses ⌘B → Sidebar collapses → Presses again → Sidebar expands
```

**Technical Requirements:**
- CSS update to sidebar footer
- No new functionality (⌘B already implemented)

**Success Metrics:**
- Increased sidebar collapse usage
- Reduced "how do I collapse" questions
- Users discover shortcut naturally

## Design Philosophy

### Visual Language

**Dark, focused, developer-friendly:**
- Dark backgrounds (#0a0a0b, #111113)
- Orange for attention (#ff6b35)
- Green for success/working (#22c55e)
- Purple for PR-related (#a78bfa)
- JetBrains Mono for code-related text
- System fonts for UI text

**Interaction patterns:**
- Hover reveals actions (reduce clutter)
- Focus rings for accessibility (orange)
- Smooth transitions (150-300ms)
- Success states brief (600ms)
- Loading states clear (spinner)

### Content Strategy

**Minimal, direct, technical:**
- Button labels: "Approve" not "Approve PR" (context clear)
- Error messages: "Connection failed. Check your network." (direct)
- No hand-holding: "Directory not found" not "Oops! We couldn't find..."
- Consistent terminology: "PR" not "pull request", "mute" not "hide"

### Personality & Delight

**Subtle, not distracting:**
- Success checkmarks scale in smoothly (300ms)
- Undo toast slides up with gentle bounce
- First PR action gets confetti burst (one-time celebration)
- Typewriter effect on Tab autocomplete (terminal-like)
- Sidebar collapse/expand animates smoothly (200ms)

**What we avoid:**
- Sound (desktop app, shared spaces)
- Overt animations (productivity tool)
- Cute copy (technical audience)
- Blocking interactions (never make users wait)

## Key Design Decisions & Rationale

### Decision 1: Hover vs Always-Visible PR Actions

**Choice:** Hover for Dashboard, always visible for Drawer.

**Rationale:**
- Dashboard has more space, many PRs → Hover reduces visual noise
- Drawer is narrow, fewer items → Always visible acceptable
- Touch devices → Drawer actions always visible works better
- Consistency trade-off accepted for context-appropriate design

### Decision 2: Merge Confirmation, Approve No Confirmation

**Choice:** Merge requires modal confirmation, Approve is immediate.

**Rationale:**
- Merge is destructive (closes PR, may delete branch)
- Approve is non-destructive (can be changed)
- Confirmation adds friction only where needed
- Developers expect this pattern (GitHub does same)

### Decision 3: Mute Undo Window (5 seconds)

**Choice:** 5-second undo with toast notification.

**Rationale:**
- 5 seconds is long enough for "oops" moments
- Short enough to not clutter screen
- Toast position (bottom center) doesn't block content
- Undo pattern familiar from email, file operations
- After 5 seconds, assumes intentional action

### Decision 4: Filesystem + Recent Combined

**Choice:** Show both filesystem suggestions and recent locations in same modal.

**Rationale:**
- Users want both: familiar recents AND new exploration
- Separate modals would add complexity
- Visual hierarchy (Directories first) guides attention
- Filtering works across both sections naturally
- No mode switching needed

### Decision 5: Tab for Autocomplete

**Choice:** Tab key autocompletes current suggestion.

**Rationale:**
- Terminal convention (familiar to developers)
- Enter is reserved for selection (standard)
- Tab cycles through suggestions if multiple
- Doesn't interfere with form navigation (single input)
- Keyboard efficiency (Tab right under fingers)

## Implementation Priorities

### Phase 1: Core Functionality (Ship First)
1. PR action buttons with loading/success states
2. GitHub API integration (approve, merge)
3. Filesystem autocomplete with keyboard nav
4. Sidebar shortcut hint

**Goal:** Get working features in users' hands.

### Phase 2: Polish (Ship Second)
1. Confirmation modals for merge
2. Undo toast for mute
3. Error handling with user-friendly messages
4. Repo-level mute actions

**Goal:** Refine UX, handle edge cases.

### Phase 3: Delight (Ship Last)
1. Success animations (checkmark scale-in)
2. Typewriter autocomplete effect
3. First PR action confetti
4. Smooth sidebar collapse animation

**Goal:** Add personality without blocking core work.

## Accessibility Compliance

### WCAG AA Standards Met

**Perceivable:**
- Color contrast ratios ≥ 4.5:1 for text
- Focus indicators on all interactive elements
- Screen reader labels on all buttons (aria-label)
- State changes announced (aria-live regions)

**Operable:**
- All functionality available via keyboard
- Focus order logical and predictable
- Keyboard shortcuts don't conflict with system
- No time limits on interactions (except undo, which has escape hatch)

**Understandable:**
- Consistent navigation patterns
- Clear error messages with recovery paths
- Labels and instructions present
- Predictable behavior (no surprise navigation)

**Robust:**
- Valid HTML/ARIA markup
- Works with assistive technologies
- Respects prefers-reduced-motion
- Degrades gracefully (no JS fallbacks needed for Tauri)

### Motion Accessibility

**prefers-reduced-motion:**
- All transitions reduce to 50ms or instant
- No scale/bounce effects
- Opacity fades remain (not motion-based)
- Decorative animations removed (confetti, typewriter)

## Technical Considerations

### GitHub API Integration

**Endpoints needed:**
- `POST /repos/{owner}/{repo}/pulls/{pull_number}/reviews` (approve)
- `PUT /repos/{owner}/{repo}/pulls/{pull_number}/merge` (merge)

**Authentication:**
- OAuth token stored securely (Tauri secure storage)
- Token refresh handling
- Permission scope: `repo` (full repo access)

**Rate Limiting:**
- GitHub API: 5,000 requests/hour (authenticated)
- Show rate limit errors clearly
- Consider caching PR state (5-minute TTL)

### Filesystem Access

**Tauri APIs:**
- `@tauri-apps/plugin-fs` for directory reading
- Permission handling for protected directories
- Platform path normalization

**Performance:**
- Debounce directory queries (150ms)
- Limit suggestions to 50 items
- Cancel in-flight requests when input changes
- Cache recent directory reads (1-minute TTL)

### State Management

**Zustand stores:**
- `prActionsStore`: Track PR action states (loading, success, error)
- `muteStore`: Track muted PRs and repos (local storage persistence)
- `filesystemStore`: Cache directory listings (ephemeral)

**Optimistic updates:**
- Show success immediately
- Rollback if API call fails
- Toast notification for errors

## Success Criteria

### Quantitative
- PR actions complete in < 2 seconds (from click to success)
- Filesystem suggestions appear in < 200ms
- Zero blocking animations (user can always proceed)
- 60fps animations on target hardware

### Qualitative
- Users find PR actions intuitive (no training needed)
- Filesystem autocomplete feels natural (like terminal)
- Sidebar shortcut is discoverable (users mention using ⌘B)
- Error messages are clear (users know how to fix issues)

### User Feedback
- "I don't need to leave the app to approve PRs anymore"
- "Tab autocomplete for paths is just like terminal"
- "I didn't know ⌘B existed, thanks for showing it"
- "Mute is perfect for noisy repos I don't care about"

## Risks & Mitigations

### Risk 1: GitHub API Rate Limiting

**Impact:** Users hit rate limits, can't act on PRs.

**Mitigation:**
- Show clear error with time until reset
- Cache PR state to reduce queries
- Consider implementing read-only mode (show PRs, disable actions)

### Risk 2: Filesystem Permission Errors

**Impact:** Users can't browse protected directories.

**Mitigation:**
- Show clear permission error
- Allow typing path directly (bypass suggestions)
- Document required permissions in README

### Risk 3: Animation Performance

**Impact:** Animations janky on slower hardware.

**Mitigation:**
- Test on target hardware (MacBook Air M1+)
- Use only GPU-accelerated properties (transform, opacity)
- Provide prefers-reduced-motion escape hatch
- Remove decorative animations if needed

### Risk 4: Undo Confusion

**Impact:** Users don't understand undo window, accidentally mute.

**Mitigation:**
- Make undo button prominent (orange highlight)
- Add subtle pulse in last second (warning)
- Consider extending to 10 seconds (user testing needed)

## Next Steps (For Implementation)

1. **Set up GitHub API integration** - OAuth flow, token storage, endpoint wrappers
2. **Create PR action components** - Buttons, modal, toast with all states
3. **Implement filesystem autocomplete** - Path parsing, directory reading, keyboard nav
4. **Add sidebar shortcut hint** - CSS update to footer
5. **Test all keyboard flows** - Ensure Tab, Enter, Escape, Arrows work
6. **Add error handling** - Network errors, permissions, rate limits
7. **Implement undo mechanism** - Toast with 5-second timer
8. **Polish animations** - Success states, transitions, prefers-reduced-motion
9. **Accessibility audit** - Screen reader testing, keyboard-only testing, contrast check
10. **User testing** - Validate workflows, gather feedback, iterate

## Design Files Location

All design specifications are located in:
```
.claude/agent-outputs/attn-app-ux-improvements/01-design/
├── ux-foundation.md        (Component hierarchy, user flows, accessibility)
├── content-strategy.md     (Copy, messaging, microcopy, tone)
├── personality-whimsy.md   (Micro-interactions, animations, delight)
├── visual-design.md        (Colors, typography, spacing, CSS specs)
└── design-summary.md       (This file - overview and decisions)
```

## Questions for Product/Engineering

1. **GitHub API:** Do we have OAuth flow set up, or should this be first implementation task?
2. **Mute storage:** Is local storage acceptable, or should muted PRs sync across machines?
3. **Filesystem permissions:** What's minimum required permission scope for directory browsing?
4. **Platform support:** Is Windows support required, or Mac-only initially?
5. **Error handling:** Should errors be logged/reported anywhere, or just shown to user?
6. **Rate limiting:** What's acceptable degradation when rate limited? Read-only mode vs disable feature?
7. **Animation performance:** What's minimum target hardware? (affects animation complexity)
8. **User testing:** Can we get 3-5 beta users to test PR actions before wide release?

## References & Research

### GitHub API Documentation
- [REST API endpoints for pull requests](https://docs.github.com/en/rest/pulls/pulls) - GitHub Docs
- [Approving a pull request with required reviews](https://docs.github.com/articles/approving-a-pull-request-with-required-reviews) - GitHub Docs
- [How to Use the GitHub Pulls API](https://stateful.com/blog/github-pulls-api-manage-prs) - Stateful

### UX Patterns
- [Autocomplete Pattern | UX Patterns for Developers](https://uxpatterns.dev/patterns/forms/autocomplete)
- [Five Simple Steps For Better Autocomplete UX](https://smart-interface-design-patterns.com/articles/autocomplete-ux/) - Smart Interface Design Patterns
- [9 UX Best Practice Design Patterns for Autocomplete](https://baymard.com/blog/autocomplete-design) - Baymard Institute
- [Keyboard Navigation Patterns for Complex Widgets](https://www.uxpin.com/studio/blog/keyboard-navigation-patterns-complex-widgets/) - UXPin

### Technical Implementation
- Tauri filesystem plugin documentation
- React keyboard event handling best practices
- CSS GPU-accelerated animations (transform, opacity)
- WCAG 2.1 AA compliance guidelines

---

**Design handoff ready for implementation.** All specifications are implementation-ready with CSS code, component structures, user flows, and success criteria defined. Engineering can begin work on Phase 1 (core functionality) immediately.
