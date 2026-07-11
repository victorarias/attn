# OpenCode default-launch revision

## Decision

Plain, promptless OpenCode sessions no longer require attn to provide a model
or effort pin. Attn launches the normal OpenCode TUI and OpenCode applies its
own configured defaults. The server-backed adapter subscribes to that run's
authenticated event stream and records the native session when normal TUI input
creates it.

Delegated staged prompts and explicit model/effort requests retain the original
server-side creation flow. That path is the only one that needs an attn-provided
model/variant, because it must bind the staged prompt to a selected native
session deterministically.

## Evidence

The `attn-dev` live app launched an unpinned session with no model, effort, or
initial prompt. OpenCode rendered its own GLM 5.2/max default. A normal PTY
prompt returned `OPENCODE_DEFAULT_LIVE_OK`; the plugin persisted the resulting
native ID, model, and variant and attn reported `idle`. An explicit subsequent
resume reopened that same visible conversation.

The TUI home route has no native session before the first prompt, so attn keeps
the run `launching` during that interval. It must not adopt a historical entry
from OpenCode's global session list.

## Verification

- `bun test` in `plugins/attn-opencode`: 11 tests, 40 assertions.
- Non-production `attn-dev` live launch, terminal steering, identity capture,
  idle report, and resume. Production was not touched.
