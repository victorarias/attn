# Archived GPUI Native Client

This directory contains the retired Rust/GPUI native client and the earlier
canvas archive it already carried.

The GPUI client established useful daemon and Ghostty external-I/O mechanics,
but it is no longer an active implementation. Native Ghostty `NSView`
surfaces and GPUI modal presentation do not compose into the terminal-first
application required by attn.

Use this archive only for reference when porting:

- daemon WebSocket and PTY transport mechanics;
- Ghostty external-I/O callbacks and replay handling;
- automation isolation/profile behavior;
- workspace-first sidebar and launcher intent.

Do not extend it or reintroduce it as the active native UI. The active
macOS client belongs in `../native-ui/` and is planned in
`../docs/plans/2026-05-24-swift-native-workspace-client.md`.
