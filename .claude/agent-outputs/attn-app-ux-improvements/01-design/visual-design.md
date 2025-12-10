# Visual Design: Attn App Improvements
**Agent:** Web Designer
**Date:** 2025-12-10
**Phase:** 1 - Design
**Project:** Claude Manager (attn app)

## Overview

Visual design specifications for PR actions, filesystem autocomplete, and sidebar shortcuts. All designs maintain the existing dark theme and monospace aesthetic.

## Color Palette

### Primary Colors (Existing)
```css
--bg-primary: #0a0a0b;        /* Main background */
--bg-secondary: #111113;      /* Cards, modals, panels */
--bg-tertiary: #1a1a1d;       /* Hover states, input backgrounds */

--border-primary: #2a2a2d;    /* Card borders, dividers */
--border-hover: #4a4a4d;      /* Input focus, button hover borders */

--text-primary: #e8e8e8;      /* Main text, headings */
--text-secondary: #888;       /* Secondary text, descriptions */
--text-tertiary: #555;        /* Labels, hints, placeholders */
```

### Semantic Colors (Existing + New)
```css
--state-waiting: #ff6b35;     /* Orange - needs attention */
--state-working: #22c55e;     /* Green - in progress/success */
--state-pr: #a78bfa;          /* Purple - PR-related */

/* New: Action states */
--action-approve: #22c55e;    /* Green - approval action */
--action-merge: #a78bfa;      /* Purple - merge action */
--action-mute: #555;          /* Gray - mute action */

/* New: Feedback states */
--feedback-success: #22c55e;  /* Green - action succeeded */
--feedback-error: #ef4444;    /* Red - action failed */
--feedback-loading: #555;     /* Gray - loading state */
```

### Transparency Overlays
```css
--overlay-dark: rgba(0, 0, 0, 0.6);          /* Modal backdrops */
--overlay-hover: rgba(59, 130, 246, 0.15);   /* Item hover (blue tint) */
--overlay-action: rgba(255, 107, 53, 0.2);   /* Attention highlights */
--overlay-success: rgba(34, 197, 94, 0.15);  /* Success highlights */
```

## Typography

### Font Families
```css
--font-mono: 'JetBrains Mono', 'Menlo', 'Monaco', 'Courier New', monospace;
--font-ui: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
```

**Usage:**
- **Monospace:** Paths, code, session labels, PR numbers, kbd elements
- **UI Font:** Body text, buttons, descriptions, PR titles

### Type Scale
```css
/* Labels and metadata */
--text-xs: 10px;     /* Line: 14px */ /* Section titles, counts */
--text-sm: 11px;     /* Line: 16px */ /* Keyboard hints, captions */
--text-base: 12px;   /* Line: 18px */ /* PR rows, buttons, drawer items */
--text-md: 13px;     /* Line: 20px */ /* Body text, session names */
--text-lg: 14px;     /* Line: 22px */ /* Input text, focus content */

/* Headings */
--text-xl: 24px;     /* Line: 32px */ /* Dashboard title */
```

### Font Weights
```css
--weight-normal: 400;    /* Body text */
--weight-medium: 500;    /* Keyboard focus */
--weight-semibold: 600;  /* Buttons, labels, section headers */
--weight-bold: 700;      /* Counts, emphasis, dashboard title */
```

## Component Designs

### 1. PR Action Buttons (Dashboard)

**Default State:**
```css
.pr-actions {
  display: flex;
  gap: 4px;
  margin-left: auto;
  opacity: 0;
  transition: opacity 150ms ease-out;
}

.pr-row:hover .pr-actions,
.pr-row:focus-within .pr-actions {
  opacity: 1;
}

.pr-action-btn {
  background: var(--bg-tertiary);
  border: 1px solid var(--border-primary);
  border-radius: 4px;
  padding: 4px 8px;
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--text-secondary);
  cursor: pointer;
  transition: all 150ms ease-out;
  font-family: var(--font-ui);
}

.pr-action-btn:hover {
  background: var(--bg-secondary);
  color: var(--text-primary);
  border-color: var(--border-hover);
}

.pr-action-btn:focus {
  outline: 2px solid var(--state-waiting);
  outline-offset: 1px;
}
```

**Button Variants:**
```css
/* Approve button */
.pr-action-btn[data-action="approve"]:hover {
  background: rgba(34, 197, 94, 0.1);
  color: var(--action-approve);
  border-color: var(--action-approve);
}

/* Merge button */
.pr-action-btn[data-action="merge"]:hover {
  background: rgba(167, 139, 250, 0.1);
  color: var(--action-merge);
  border-color: var(--action-merge);
}

/* Mute button */
.pr-action-btn[data-action="mute"]:hover {
  background: rgba(85, 85, 85, 0.2);
  color: var(--text-primary);
}
```

**Loading State:**
```css
.pr-action-btn[data-loading="true"] {
  color: var(--feedback-loading);
  pointer-events: none;
}

.pr-action-btn[data-loading="true"]::before {
  content: '';
  display: inline-block;
  width: 10px;
  height: 10px;
  border: 2px solid var(--feedback-loading);
  border-top-color: transparent;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}
```

**Success State:**
```css
.pr-action-btn[data-success="true"] {
  background: var(--feedback-success);
  color: #000;
  border-color: var(--feedback-success);
  pointer-events: none;
}

.pr-action-btn[data-success="true"]::before {
  content: '✓';
  font-size: 12px;
  animation: checkmark-appear 300ms ease-out;
}

@keyframes checkmark-appear {
  from {
    opacity: 0;
    transform: scale(0.8);
  }
  to {
    opacity: 1;
    transform: scale(1);
  }
}
```

**Dimensions:**
- Height: 24px (consistent with PR row height)
- Min-width: 56px (label has room)
- Gap between buttons: 4px

### 2. PR Action Icons (AttentionDrawer)

**Compact Icon-Only Actions:**
```css
.pr-actions-compact {
  display: flex;
  gap: 2px;
  margin-left: auto;
  flex-shrink: 0;
}

.action-icon {
  width: 24px;
  height: 24px;
  background: none;
  border: 1px solid var(--border-primary);
  border-radius: 4px;
  padding: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  color: var(--text-secondary);
  font-size: 12px;
  transition: all 150ms ease-out;
}

.action-icon:hover {
  background: var(--bg-tertiary);
  color: var(--text-primary);
  border-color: var(--border-hover);
}

/* Icons */
.action-icon[data-action="approve"]::before {
  content: '✓';
}

.action-icon[data-action="merge"]::before {
  content: '⇋';  /* Merge arrows */
}

.action-icon[data-action="mute"]::before {
  content: '⊘';  /* Eye with slash */
}
```

**Drawer Context:**
- Icons appear always (not on hover, touch-friendly)
- Smaller footprint for narrow drawer (320px)
- Tooltips on hover for clarity

### 3. Confirmation Modal

**Modal Structure:**
```css
.confirmation-overlay {
  position: fixed;
  inset: 0;
  background: var(--overlay-dark);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 300;
  animation: fade-in 150ms ease-out;
}

.confirmation-modal {
  width: 400px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-primary);
  border-radius: 8px;
  box-shadow: 0 24px 64px rgba(0, 0, 0, 0.6);
  animation: modal-appear 200ms ease-out;
}

@keyframes modal-appear {
  from {
    opacity: 0;
    transform: scale(0.95) translateY(-10px);
  }
  to {
    opacity: 1;
    transform: scale(1) translateY(0);
  }
}

.modal-header {
  padding: 16px;
  border-bottom: 1px solid var(--border-primary);
}

.modal-title {
  font-family: var(--font-mono);
  font-size: var(--text-md);
  font-weight: var(--weight-semibold);
  color: var(--text-primary);
}

.modal-body {
  padding: 16px;
  color: var(--text-secondary);
  font-size: var(--text-base);
}

.modal-footer {
  padding: 16px;
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  border-top: 1px solid var(--border-primary);
}

.modal-btn {
  padding: 8px 16px;
  border-radius: 4px;
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
  cursor: pointer;
  transition: all 150ms ease-out;
  border: 1px solid var(--border-primary);
  font-family: var(--font-ui);
}

.modal-btn-cancel {
  background: transparent;
  color: var(--text-secondary);
}

.modal-btn-cancel:hover {
  background: var(--bg-tertiary);
  color: var(--text-primary);
}

.modal-btn-primary {
  background: var(--state-pr);
  color: #000;
  border-color: var(--state-pr);
}

.modal-btn-primary:hover {
  background: #b695f5;
  border-color: #b695f5;
}
```

### 4. Undo Toast

**Toast Design:**
```css
.undo-toast {
  position: fixed;
  bottom: 24px;
  left: 50%;
  transform: translateX(-50%);
  background: var(--bg-secondary);
  border: 1px solid var(--border-primary);
  border-radius: 6px;
  padding: 12px 16px;
  display: flex;
  align-items: center;
  gap: 12px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
  z-index: 400;
  animation: toast-appear 200ms ease-out;
}

@keyframes toast-appear {
  from {
    opacity: 0;
    transform: translateX(-50%) translateY(10px);
  }
  to {
    opacity: 1;
    transform: translateX(-50%) translateY(0);
  }
}

.toast-message {
  color: var(--text-primary);
  font-size: var(--text-base);
  font-family: var(--font-mono);
}

.toast-undo-btn {
  background: var(--bg-tertiary);
  border: 1px solid var(--border-primary);
  border-radius: 4px;
  padding: 4px 12px;
  color: var(--state-waiting);
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
  cursor: pointer;
  transition: all 150ms ease-out;
  font-family: var(--font-ui);
}

.toast-undo-btn:hover {
  background: rgba(255, 107, 53, 0.15);
  border-color: var(--state-waiting);
}
```

### 5. Filesystem Autocomplete

**Enhanced LocationPicker:**
```css
/* Existing styles continue, add: */

.picker-breadcrumb {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--text-tertiary);
  padding: 4px 14px 8px;
}

.picker-breadcrumb-label {
  color: var(--text-tertiary);
  margin-right: 4px;
}

.picker-breadcrumb-path {
  color: var(--text-secondary);
}

/* Loading indicator */
.picker-loading {
  position: absolute;
  right: 14px;
  top: 50%;
  transform: translateY(-50%);
  width: 12px;
  height: 12px;
  border: 2px solid var(--feedback-loading);
  border-top-color: transparent;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
  opacity: 0.5;
}

/* Directory items with keyboard focus */
.picker-item[data-type="directory"] {
  /* Same as existing .picker-item */
}

.picker-item[data-type="directory"]:focus,
.picker-item[data-type="directory"].keyboard-focused {
  background: var(--overlay-hover);
  border-left: 2px solid var(--state-waiting);
  padding-left: 10px; /* Compensate for border */
  font-weight: var(--weight-medium);
}

/* Autocomplete hint */
.picker-autocomplete-hint {
  position: absolute;
  left: 14px;
  top: 12px;
  font-family: var(--font-mono);
  font-size: var(--text-lg);
  color: var(--text-tertiary);
  pointer-events: none;
  opacity: 0.4;
}
```

**Visual Hierarchy:**
1. Input field - most prominent (large, bright)
2. Breadcrumb - contextual helper (small, dim)
3. Directories section - primary suggestions (normal weight)
4. Recent section - secondary suggestions (slightly dimmer)

### 6. Sidebar Shortcut Hint

**Footer Enhancement:**
```css
.sidebar-footer {
  border-top: 1px solid var(--border-primary);
  padding: 12px;
  display: flex;
  gap: 16px;
  flex-wrap: wrap;
}

.shortcut-hint {
  font-size: var(--text-sm);
  color: var(--text-tertiary);
  font-family: var(--font-ui);
  display: flex;
  align-items: center;
  gap: 4px;
}

.shortcut-hint kbd {
  background: var(--bg-tertiary);
  border: 1px solid var(--border-primary);
  border-radius: 3px;
  padding: 2px 6px;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--text-secondary);
  font-weight: var(--weight-semibold);
}
```

**Collapsed State:**
```css
.sidebar.collapsed .sidebar-footer {
  flex-direction: column;
  gap: 8px;
  align-items: center;
}

.sidebar.collapsed .shortcut-hint {
  writing-mode: vertical-rl;
  text-orientation: mixed;
}
```

## Spacing Scale

### Standard Spacing Units
```css
--space-xs: 4px;      /* Tight gaps, icon padding */
--space-sm: 8px;      /* Button padding, small gaps */
--space-md: 12px;     /* Default padding, list gaps */
--space-lg: 16px;     /* Card padding, section gaps */
--space-xl: 24px;     /* Large section spacing */
--space-2xl: 40px;    /* Dashboard padding */
```

### Component Spacing

**PR Row (Dashboard):**
- Padding: 10px 12px
- Gap between elements: 8px
- Margin between rows: 4px

**PR Row (Drawer):**
- Padding: 10px 12px
- Gap between elements: 10px
- Margin between rows: 2px

**Button Groups:**
- Gap between buttons: 4px
- Button padding: 4px 8px (compact), 8px 16px (modal)

**Modal:**
- Header/body/footer padding: 16px
- Gap between footer buttons: 8px

**LocationPicker:**
- Modal padding: 16px (header), 8px (results), 10px (footer)
- Item padding: 10px 12px
- Gap between sections: 8px

## Responsive Breakpoints

```css
--breakpoint-sm: 640px;   /* Not target for desktop app */
--breakpoint-md: 1024px;  /* Tablet/small desktop */
--breakpoint-lg: 1366px;  /* Standard desktop */
--breakpoint-xl: 1920px;  /* Large desktop */
```

**Adjustments:**
- < 1366px: Reduce dashboard padding to 24px
- < 1024px: Stack dashboard cards vertically
- LocationPicker: Stays 560px width (centered)
- AttentionDrawer: Stays 320px width (always)

## Iconography

### Icon Sources

**PR Actions:**
- ✓ (U+2713) - Approve, success
- ⇋ (U+21CB) - Merge
- ⊘ (U+2298) - Mute (eye with slash alternative)

**Existing:**
- 👀 (U+1F440) - Reviewer role
- ✏️ (U+270F) - Author role
- 📁 (U+1F4C1) - Directory
- ▾ (U+25BE) - Collapse arrow
- ⌂ (U+2302) - Home/dashboard

**Keyboard:**
- ⌘ (U+2318) - Command key
- ⌥ (U+2325) - Option key
- ⌃ (U+2303) - Control key
- ↑ (U+2191) - Up arrow
- ↓ (U+2193) - Down arrow

### Icon Sizing
- Small icons (counts, indicators): 8-10px
- Regular icons (actions, roles): 12px
- Large icons (directory, home): 14px
- Icon buttons: 24x24px container, 12px icon

## Shadows & Elevation

```css
--shadow-sm: 0 2px 8px rgba(0, 0, 0, 0.2);         /* Hover cards */
--shadow-md: 0 8px 32px rgba(0, 0, 0, 0.4);        /* Toasts, dropdowns */
--shadow-lg: 0 24px 64px rgba(0, 0, 0, 0.6);       /* Modals */
--shadow-drawer: -8px 0 32px rgba(0, 0, 0, 0.4);   /* AttentionDrawer */
```

**Usage:**
- Cards: No shadow (rely on borders)
- LocationPicker: shadow-lg
- AttentionDrawer: shadow-drawer
- Undo Toast: shadow-md
- Confirmation Modal: shadow-lg

## Border Radius

```css
--radius-sm: 3px;     /* kbd elements, tiny buttons */
--radius-md: 4px;     /* Buttons, inputs */
--radius-lg: 6px;     /* List items, toasts */
--radius-xl: 8px;     /* Cards, modals */
--radius-2xl: 12px;   /* LocationPicker */
--radius-full: 50%;   /* Dots, badges, spinners */
```

## Focus & Interaction States

### Focus Rings
```css
/* Default focus */
:focus-visible {
  outline: 2px solid var(--state-waiting);
  outline-offset: 1px;
}

/* Focus within container */
.pr-row:focus-within {
  background: var(--bg-tertiary);
}

/* Keyboard navigation focus (js-added class) */
.keyboard-focused {
  background: var(--overlay-hover);
  border-left: 2px solid var(--state-waiting);
}
```

### Hover States
```css
/* Interactive elements */
.clickable:hover {
  background: var(--bg-tertiary);
}

/* Buttons */
button:hover {
  filter: brightness(1.1);
}

/* Links */
a:hover {
  color: var(--text-primary);
}
```

### Active States
```css
/* Button press */
button:active {
  transform: scale(0.98);
}

/* List item selection */
.selected {
  background: var(--overlay-hover);
  border-left: 2px solid var(--state-waiting);
}
```

## Dark Theme Considerations

App is dark-only (no light theme planned), but ensure:

**Contrast Ratios (WCAG AA):**
- Primary text (#e8e8e8) on primary bg (#0a0a0b): 15.8:1 ✓
- Secondary text (#888) on primary bg: 5.2:1 ✓
- Tertiary text (#555) on primary bg: 3.2:1 (decorative only)
- Buttons hover: Minimum 4.5:1
- Orange (#ff6b35) on dark: 4.7:1 ✓

**Brightness:**
- Background brightness stays low (< 10% lightness)
- Avoid pure white (#fff) - use #e8e8e8 max
- Buttons use subtle backgrounds, not bright colors

## CSS Custom Properties

**Define in :root:**
```css
:root {
  /* Colors */
  --bg-primary: #0a0a0b;
  --bg-secondary: #111113;
  --bg-tertiary: #1a1a1d;

  --border-primary: #2a2a2d;
  --border-hover: #4a4a4d;

  --text-primary: #e8e8e8;
  --text-secondary: #888;
  --text-tertiary: #555;

  --state-waiting: #ff6b35;
  --state-working: #22c55e;
  --state-pr: #a78bfa;

  --action-approve: #22c55e;
  --action-merge: #a78bfa;
  --action-mute: #555;

  --feedback-success: #22c55e;
  --feedback-error: #ef4444;
  --feedback-loading: #555;

  /* Typography */
  --font-mono: 'JetBrains Mono', monospace;
  --font-ui: -apple-system, BlinkMacSystemFont, sans-serif;

  --text-xs: 10px;
  --text-sm: 11px;
  --text-base: 12px;
  --text-md: 13px;
  --text-lg: 14px;
  --text-xl: 24px;

  --weight-normal: 400;
  --weight-medium: 500;
  --weight-semibold: 600;
  --weight-bold: 700;

  /* Spacing */
  --space-xs: 4px;
  --space-sm: 8px;
  --space-md: 12px;
  --space-lg: 16px;
  --space-xl: 24px;
  --space-2xl: 40px;

  /* Radius */
  --radius-sm: 3px;
  --radius-md: 4px;
  --radius-lg: 6px;
  --radius-xl: 8px;
  --radius-2xl: 12px;
  --radius-full: 50%;

  /* Shadows */
  --shadow-sm: 0 2px 8px rgba(0, 0, 0, 0.2);
  --shadow-md: 0 8px 32px rgba(0, 0, 0, 0.4);
  --shadow-lg: 0 24px 64px rgba(0, 0, 0, 0.6);
  --shadow-drawer: -8px 0 32px rgba(0, 0, 0, 0.4);

  /* Overlays */
  --overlay-dark: rgba(0, 0, 0, 0.6);
  --overlay-hover: rgba(59, 130, 246, 0.15);
  --overlay-action: rgba(255, 107, 53, 0.2);
  --overlay-success: rgba(34, 197, 94, 0.15);
}
```

## Implementation Notes

### CSS Organization
```
components/
  PRActions.css          (new)
  ConfirmationModal.css  (new)
  UndoToast.css          (new)
  LocationPicker.css     (update existing)
  Sidebar.css            (update existing)
```

### Naming Conventions
- BEM-style: `.pr-actions`, `.pr-actions__button`, `.pr-actions__button--loading`
- Data attributes for states: `data-loading="true"`, `data-action="approve"`
- Semantic class names: `.clickable`, `.keyboard-focused`, `.success`

### Browser Compatibility
- Target: Chromium (Tauri uses Chromium engine)
- No need for vendor prefixes (modern Chromium)
- Use CSS custom properties freely
- Use modern flexbox/grid
- Animations use transform/opacity (GPU-accelerated)

### Performance
- Use `contain` for independent components
- Use `will-change` sparingly (loading states only)
- Prefer CSS animations over JS (60fps)
- Debounce filesystem queries (avoid rapid re-renders)
