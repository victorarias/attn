# Changelog

All notable changes to this project are documented in this file.

Format: `[YYYY-MM-DD]` entries with categories: Added, Changed, Fixed, Removed.

---

## [2026-01-06]

### Added
- **Won't Fix Action**: New comment action for marking comments as "won't fix"
  - Mutually exclusive with Resolved (setting one clears the other)
  - Visual indicator with amber styling
  - Available in both Review Panel and reviewer agent
- **Markdown Support**: Comment content now renders Markdown
  - Supports code blocks, links, lists, bold/italic, blockquotes
  - Uses ReactMarkdown for saved comments, marked for CodeMirror widgets

### Fixed
- **Font Size Shortcuts**: Cmd+/- no longer loses collapsed regions or comments
  - Added fontSize to effect dependencies so decorations rebuild after editor recreation
- **Font Size Scaling**: Comment UI elements now scale with font size changes
  - Author badges, action buttons, textarea, collapsed regions all respect zoom level

---

## [2026-01-02]

### Added
- **Review Panel**: New full-screen diff review interface
  - File list with "NEEDS REVIEW" and "AUTO-SKIP" sections
  - CodeMirror 6 with One Dark theme for syntax highlighting
  - Unified diff view with clear red/green highlighting
  - Auto-skip detection for lockfiles (pnpm-lock.yaml, package-lock.json, etc.)
  - Hunks/Full toggle to collapse unchanged regions
  - Keyboard navigation: `j`/`k` navigate, `]` next unreviewed, `e`/`E` expand
  - Font size controls: `⌘+`/`⌘-` zoom, `⌘0` reset
  - Entry point: "Review" button in Changes panel header
