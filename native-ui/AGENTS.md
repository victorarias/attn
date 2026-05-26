# Native Swift Client

This directory contains the active Swift macOS client.

- Build and test through SwiftPM; do not introduce an Xcode project.
- The daemon is authoritative for sessions, PTYs and workspace layout.
- Use the forked Ghostty native surface host for terminal rendering and input.
- Do not implement a custom terminal cell renderer.
- Do not hide or snapshot terminal surfaces to present native UI above them.
- Implement non-frontmost automation for a real daemon-backed terminal pane
  before porting New Workspace or Add Pane dialogs.
- Do not import or revive code from `../native-gpui-archive/`; it is reference
  material only.
