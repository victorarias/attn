# Changelog

All notable changes to this project are documented in this file.

Format: `[YYYY-MM-DD]` entries with categories: Added, Changed, Fixed, Removed.

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
