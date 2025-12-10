# Personality & Whimsy: Attn App Improvements
**Agent:** Web Designer
**Date:** 2025-12-10
**Phase:** 1 - Design
**Project:** Claude Manager (attn app)

## Overview

The attn app serves developers managing multiple Claude Code sessions - a focused, productivity tool. Personality should enhance functionality without creating distraction. Think "refined developer tools" not "playful consumer app."

## Design Principle

**Subtle Delight > Obvious Playfulness**

Users are in a workflow state, often stressed (waiting sessions, PRs need review). Interactions should feel smooth, predictable, and confidence-building. Whimsy is in the micro-interactions, not the macro experience.

## Micro-Interactions

### 1. PR Action Success States

**Approve Button Success:**
- Button background smoothly transitions to green (#22c55e)
- Checkmark icon (✓) fades in from center, scales from 0.8 to 1.0
- Button content fades out as checkmark fades in
- After 600ms, entire button fades out (PR updates in place)
- Timing: ease-out curve, feels responsive not sluggish

**Why this works:**
- Visual confirmation of success (green = good)
- Checkmark is universal success symbol
- Fade out prevents lingering UI clutter
- Fast enough to feel instant, slow enough to register

**Merge Button Success:**
- Similar to approve but uses purple (#a78bfa) to match PR theme
- Icon: merge symbol or checkmark
- Same fade pattern
- PR may slide out of list with smooth translation

**Why this works:**
- Purple associates with PR/git themes
- Motion gives sense of completion
- Slide animation = "this is done, moved along"

**Mute Action:**
- PR row opacity fades from 1.0 to 0.3 over 150ms
- Row height collapses to 0 over 200ms (staggered)
- Rows below slide up smoothly
- Undo toast slides up from bottom with gentle bounce

**Why this works:**
- Fade + collapse = clear removal
- Stagger prevents jarring instant removal
- Bounce on toast = friendly, recoverable action
- Not too bouncy (1.2x scale max, settles quickly)

### 2. Filesystem Autocomplete Interactions

**Directory Loading State:**
- When user types path, show subtle spinner next to input
- Spinner is small (12px), low-opacity (#555), rotates smoothly
- Appears after 100ms delay (avoids flicker for fast loads)
- Disappears with fade when results load

**Why this works:**
- Delay prevents flicker on fast filesystem reads
- Small + subtle = not demanding attention
- Users know system is working, don't worry

**Suggestion Highlight:**
- Keyboard navigation: smooth transition between highlights
- Background color transitions over 100ms (no instant jumps)
- Selected item gets subtle left border (2px, orange)
- Font weight increases slightly (400 → 500)

**Why this works:**
- Smooth transitions = polished feel
- Border + weight = clear focus without being loud
- Orange matches attention theme

**Tab Autocomplete:**
- When user presses Tab, text fills in with typewriter effect
- Characters appear 3 at a time, 30ms interval (very fast)
- Completed portion briefly flashes subtle highlight
- Feels like terminal autocomplete (familiar to developers)

**Why this works:**
- Typewriter = computer is helping you type
- Fast enough to not slow workflow
- Flash highlight = "I completed this part"
- Terminal familiarity = comfortable pattern

### 3. Sidebar Collapse Animation

**Collapse (⌘B pressed):**
- Sidebar width animates from 240px to 48px over 200ms
- Content fades out during first 100ms
- Icons fade in during last 100ms
- Ease-in-out curve (smooth start and end)

**Why this works:**
- Content fade prevents text scramble during collapse
- Icons appear when space allows
- Not too fast (feels controlled) not too slow (feels sluggish)

**Expand (⌘B pressed):**
- Width animates 48px to 240px over 200ms
- Icons fade out during first 100ms
- Content fades in during last 100ms
- Same ease curve

**Why this works:**
- Mirror of collapse = predictable
- No content shift awkwardness
- Clean transition

## Delightful Moments (Subtle)

### 1. First-Time PR Action Celebration

**Trigger:** User approves or merges their first PR via the app.

**Effect:**
- Success checkmark pulses slightly (1.0 → 1.15 → 1.0 scale)
- Subtle confetti burst (5-7 particles, orange/purple/green)
- Particles fade and fall over 800ms
- Never repeats (stored in localStorage: firstPRAction: true)

**Why this is good:**
- Celebrates meaningful action (first PR workflow)
- Only happens once (doesn't get annoying)
- Confetti is brief and tasteful (not overwhelming)
- Colors match app theme

**Why not overboard:**
- No sound
- No modal/blocking UI
- Particles are small and transparent
- Doesn't repeat every action

### 2. Perfect Path Autocomplete

**Trigger:** User types exact path that exists, presses Enter immediately.

**Effect:**
- Input field gets subtle green glow (1px box-shadow)
- Glow pulses once (opacity 0.3 → 0.6 → 0)
- Modal closes with slight zoom-out scale (1.0 → 0.95)
- Session appears with gentle zoom-in (0.95 → 1.0)

**Why this is good:**
- Rewards efficient workflow
- Green = confidence, correctness
- Scale transition = smooth flow between modals
- Acknowledges user skill

### 3. Undo Time Running Out

**Trigger:** 4 seconds into 5-second undo window for muted PR.

**Effect:**
- Undo button gets subtle orange pulse
- Text slightly emphasizes: "Undo" becomes bolder
- Pulse frequency increases in last second
- Toast fades out smoothly when time expires

**Why this is good:**
- Warns user action is about to be permanent
- Gives chance for last-second undo
- Not aggressive (no countdown numbers, no red)
- Pulse is calm, not panicked

## Animation Timing Reference

### Speed Guidelines
- **Instant (0ms):** Color changes on click (before action starts)
- **Very Fast (100ms):** Hover states, focus changes
- **Fast (200ms):** Micro-interactions, state changes
- **Medium (300-400ms):** Transitions between views
- **Slow (500-600ms):** Success confirmations, celebrations
- **Very Slow (800ms+):** Only for decorative elements (confetti fall)

### Easing Curves
- **Ease-out:** Things entering (drawers, modals, toasts)
- **Ease-in:** Things leaving (close, collapse, dismiss)
- **Ease-in-out:** Things transitioning (expand/collapse toggle)
- **Spring/Bounce:** ONLY for undo toast (very subtle, max 1.1x scale)

### Loading States
- **Spinner appearance delay:** 100ms (prevents flicker)
- **Spinner minimum duration:** 300ms (prevents flash)
- **Skeleton loader:** 150ms fade-in, stays until content ready

## Color Psychology in Animations

### Success (Green #22c55e)
- Use for: Approvals, completions, correct paths
- Never overuse: Only on actual success, not every action

### Attention (Orange #ff6b35)
- Use for: Waiting states, undo warnings, focus indicators
- Already established as "needs attention" color

### Progress (Purple #a78bfa)
- Use for: PR-related actions, merge states
- Matches PR role indicator theme

### Neutral (Grays #555, #2a2a2d)
- Use for: Loading, disabled states, secondary info
- Most animations stay neutral

## Motion Accessibility

**Respect `prefers-reduced-motion`:**

When user has reduced motion enabled:
- All transitions reduce to 50ms or instant
- No scale/bounce effects
- Opacity fades remain (not motion-based)
- Skeleton loaders stay (not motion-based)
- No confetti or decorative particles

**Implementation:**
```css
@media (prefers-reduced-motion: reduce) {
  * {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.01ms !important;
  }

  /* Keep fade-based effects */
  .toast, .fade-in, .fade-out {
    transition-duration: 100ms !important;
  }
}
```

## Sound Design (Optional, Future)

Currently: **No sound.**

If sound is added later (user preference opt-in):
- Approve: Soft "tick" (like pen click)
- Merge: Soft "whoosh" (like sliding drawer)
- Mute: Soft "thud" (like closing book)
- Error: Soft "bonk" (not harsh)
- All sounds < 100ms duration
- All sounds < 50dB perceived volume
- Never auto-play without user consent

**Why no sound now:**
- Desktop app may be in meetings/shared spaces
- Developer tools traditionally silent
- Can add later without breaking UX

## Easter Eggs

### 1. Konami Code on Dashboard
**Trigger:** User types Konami code (↑↑↓↓←→←→BA) on dashboard.

**Effect:**
- All waiting session dots momentarily turn green
- Sessions briefly animate in wave pattern
- Effect lasts 1 second, everything returns to normal
- No functional change, pure visual moment

**Why this is good:**
- Classic developer culture reference
- Doesn't break anything
- Requires intentional action (not accidental)
- Brief enough to not be annoying

### 2. Rapid Session Creation
**Trigger:** User creates 5 sessions within 10 seconds.

**Effect:**
- Sixth session modal has subtle "speedrun mode" badge
- Badge shows for 2 seconds, fades out
- Text: "⚡️ speedrun mode"

**Why this is good:**
- Acknowledges power user behavior
- Doesn't interfere with workflow
- Badge is decorative only
- Makes user smile without blocking

## Anti-Patterns to Avoid

**DON'T:**
- Animate on every hover (overwhelming)
- Use bounce on serious actions (merge, approve)
- Make users wait for animations to complete
- Block interactions during decorative animations
- Add sound without user permission
- Use animations longer than 600ms for functional UI
- Pulse/flash error states continuously
- Make undo window shorter for animation purposes
- Add loading animations < 100ms duration (flicker)
- Use red aggressively (harsh, panic-inducing)

**DO:**
- Animate to provide feedback
- Keep animations under 300ms for functional UI
- Allow skipping animations (click again = immediate)
- Use motion to guide attention
- Respect reduced motion preferences
- Test animations at 60fps (no jank)
- Make success feel good, errors feel recoverable
- Use easing curves that match physical motion

## Testing Checklist

Before shipping any animated interaction:

- [ ] Runs at 60fps on target hardware
- [ ] Respects `prefers-reduced-motion`
- [ ] Doesn't block user workflow
- [ ] Duration < 300ms for functional UI
- [ ] Provides clear feedback (what happened?)
- [ ] Feels natural, not mechanical
- [ ] Tested on keyboard navigation path
- [ ] Doesn't flash/flicker
- [ ] Looks good in both light/dark theme (app is dark)
- [ ] Accessible to screen readers (state changes announced)

## Implementation Priority

**Ship First (Core Functionality):**
1. Button loading states (spinner)
2. Success checkmarks
3. Mute fade-out + undo toast
4. Sidebar collapse/expand
5. Autocomplete highlight transitions

**Ship Second (Polish):**
1. PR row slide-out on merge
2. Typewriter autocomplete effect
3. Path validation glow
4. Undo warning pulse

**Ship Last (Delight):**
1. First PR action confetti
2. Konami code easter egg
3. Speedrun mode badge

This prioritization ensures core interactions work first, polish second, and delight last. If time is limited, ship only tier 1.

## Animation Performance Notes

**GPU Acceleration:**
Animate only these CSS properties for 60fps:
- `transform` (translate, scale, rotate)
- `opacity`

**Avoid animating:**
- `width`, `height` (causes reflow)
- `top`, `left` (causes repaint)
- `box-shadow` (expensive)

**Workarounds:**
- Sidebar width → Use `transform: scaleX()` with `transform-origin`
- Toast position → Use `transform: translateY()` not `bottom`
- Glow effect → Animate opacity of pseudo-element, not box-shadow value

**Use `will-change` sparingly:**
Only on elements that will definitely animate soon:
```css
.pr-action-btn:hover {
  will-change: transform, opacity;
}
```

Remove after animation completes.
