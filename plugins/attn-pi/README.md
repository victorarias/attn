# attn-pi

attn driver plugin for [pi](https://github.com/earendil-works/pi): pi launches,
resumes, and lives as an attn session. Pure driver with dumb state — the daemon
owns the PTY and session records; this plugin only decides what argv to run.

See `AGENTS.md` for the pi invariants this driver relies on and
`docs/plans/2026-07-19-pi-driver-plugin.md` for the implementation plan.
