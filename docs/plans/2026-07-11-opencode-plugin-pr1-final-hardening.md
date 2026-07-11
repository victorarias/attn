# OpenCode PR 1 final hardening

## Durable changes

- Interactive metadata now distinguishes an OpenCode-selected default from a
  delegated pin. `pinned: false` resumes the same native session without
  enforcing an observed model or variant; legacy metadata without the marker is
  treated as pinned for safety.
- Every launch failure after `driver.spawn` starts receives
  `driver.session_closed`, so private run credentials, prompts, and launch
  files are removed even when the PTY or durable attn run cannot be created.
- Health, initial SSE readiness, native-session setup, and event callbacks have
  bounded requests. A setup failure aborts the event stream before its ordered
  `unknown` report.
- A transient established OpenCode SSE failure reconnects. Three consecutive
  established-stream failures or three failed reconnection setups instead
  degrade the run to `unknown`, avoiding an endless healthy-but-unmonitored
  loop.

## Evidence

- `bun test` in `plugins/attn-opencode`: 18 tests, 49 assertions.
- Final isolated `attn-dev` run: API-v2 plugin discovered after daemon restart;
  ordinary OpenCode TUI showed GLM 5.2/max, returned
  `OPENCODE_FINAL_LIVE_V6_OK`, and attn reached `idle`.
- Final interactive close/resume reused the same visible native conversation
  and persisted `pinned: false`, GLM model, `max`, and `resume: true`.
- Two independent native adversarial reviewers made final clean passes after
  their actionable findings were fixed. The previously requested external
  review service remains unavailable because platform policy blocks the local
  diff transfer.

## Scope

This does not add generic installed-plugin supervision, semantic
`waiting_input`, chief/workspace context parity, more effort mappings, or any
production installation.
