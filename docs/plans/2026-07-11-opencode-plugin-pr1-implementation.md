# OpenCode plugin PR 1 implementation

## Why / Alignment

An ordinary Cmd+T OpenCode session should behave like Claude or Codex: attn
launches the OpenCode TUI and OpenCode chooses its configured model, variant,
and first native session. The server-backed plugin observes that session and
persists its native identity once OpenCode creates it. A resume is unavailable
until that identity exists.

Delegated or otherwise pinned launches remain explicit: a staged prompt or a
model/effort pin causes the plugin to create and select the native session
through OpenCode's authenticated server API, preserving the prompt-to-pin
binding proved by the spike. This chunk changes only that launch-mode boundary;
it does not add a generic model picker, change OpenCode configuration, or
broaden effort mappings.

## Goal

Deliver the first installable, server-backed OpenCode driver for attn: delegated
sessions pin an OpenCode provider/model and verified effort variant, attn owns the
PTY lifecycle, and the plugin uses authenticated loopback HTTP/SSE for native
OpenCode session identity and state reporting.

## Runtime map

```text
plain Cmd+T
  -> attn-owned PTY -> opencode TUI uses its own defaults
  -> authenticated SSE observes OpenCode's first session.created
  -> plugin persists native identity and translates its lifecycle

delegated --agent opencode --model provider/model --effort low|max
  -> daemon resolves the installed driver capability and forwards pins
    -> attn-opencode driver.spawn / driver.resume (plugin JSON-RPC API v2)
      -> private run registry: password, staged prompt, launch config, sequence
      -> attn-owned PTY launches launcher.ts -> OpenCode loopback server + TUI
      -> authenticated HTTP/SSE
           health -> subscribe /event -> create or validate native session
           -> /tui/select-session -> /prompt_async (fresh runs)
           -> busy/retry => working; explicit idle => idle
```

The plugin process and its registry are rooted beside the daemon socket. This
makes an app-managed profile restart discover the plugins installed for that
profile even when the restarted process does not inherit `ATTN_PROFILE`.

## Contract and ownership

```ts
// daemon -> external driver
type DriverSpawnParams = {
  session_id: string
  run_id: string
  model?: string       // resolved delegated pin
  effort?: string      // resolved delegated pin
  initial_prompt?: string
  metadata?: unknown   // opaque native OpenCode identity on resume
}

// persisted by attn as opaque driver metadata
type OpenCodeMetadata = {
  schema: 1
  opencode_session_id: string
  opencode_version: "1.17.16" | "1.17.18"
  pinned?: boolean  // false means model/variant are observed, not constraints
  model?: string      // present for pinned or observable default selections
  variant?: string
}
```

- attn owns PTY launch/input/output, resize/replay, session and run identity,
  durable attn state, and process exit.
- `attn-opencode` owns only server startup coordination, per-run private files,
  OpenCode HTTP/SSE translation, native-session metadata, and ordered reports.
- A status record absent from `/session/status` is unobserved, never idle.
- Native session creation uses OpenCode's `{ providerID, id, variant }` shape;
  `prompt_async` uses `{ model: { providerID, modelID }, variant }`.
- The plugin only binds an unpinned interactive launch after its isolated
  OpenCode server emits `session.created`; it never guesses a resume ID before
  that event.

## Implemented

- [x] Added external-driver `model_pin` and `effort_pin` capabilities and passed
  resolved model/effort through spawn and resume requests.
- [x] Bumped the external plugin manifest/hello API to v2 and updated bundled
  manifests and SDK client.
- [x] Added installable `plugins/attn-opencode` with authenticated HTTP/SSE,
  native identity resume, atomic private registry files, bounded port retry,
  cleanup, version gating, and degraded health.
- [x] Added deterministic adapter coverage for authentication, request shapes,
  first-status absence, report ordering, resume validation, cleanup, failure,
  retry, and concurrent isolation.
- [x] Added daemon coverage for forwarding and capability-gating model/effort.
- [x] Verified the complete behavior in the isolated `attn-dev` profile with
  OpenCode 1.17.18 and `spotify-glm/zai-org/GLM-5.2-FP8` at `max` and `low`.
- [x] Let unpinned, promptless Cmd+T launches use OpenCode defaults and bind the
  first native session created on that run's private server.
- [x] Completed two independent native adversarial reviews and fixed their
  lifecycle, cleanup, and cancellation findings. The requested external review
  remains unavailable because platform policy blocks local-diff transfer.

## Decisions and deviations

- The external plugin wire API is v2. The app/daemon TypeSpec already carried
  delegated model and effort fields, so this does **not** change the public
  WebSocket schema or `ProtocolVersion`; it changes the separate daemon-plugin
  request shape and must fail closed against v1 plugins.
- Live OpenCode 1.17.18 schema inspection found that `prompt_async` has a
  different model field name from session creation. The adapter translates at
  the HTTP edge and the fake server rejects the previous invalid shape.
- Plugin discovery is derived from the daemon socket root, preserving an
  explicit `ATTN_PLUGIN_DIR` override. This was required for profile restarts
  observed during the non-production smoke test.
- Model and effort pins are optional driver inputs, not universal OpenCode
  launch requirements. They remain mandatory whenever the plugin receives a
  staged prompt, because server-side creation is what binds that prompt to the
  delegated model and effort variant without depending on TUI picker state.
- Live verification showed a plain OpenCode TUI home route has no native session
  before its first prompt. The plugin therefore remains `launching` in that
  interval and binds the `session.created` SSE event triggered by normal PTY
  input, rather than trying to adopt an unrelated historical session returned
  by the server's global session list.
- Native metadata records whether a model/variant is a delegated constraint or
  merely an observed interactive default. Interactive resume selects the same
  native session without rejecting a model that the user later changed in the
  TUI; older metadata without this marker remains conservatively pinned.
- Every HTTP and SSE setup step has a bounded request deadline. A failed setup
  aborts its established SSE stream before reporting `unknown`, so late events
  cannot overwrite degraded state. Any daemon-side failure after a driver run
  starts sends `driver.session_closed` and removes the plugin's private record.
- A normal OpenCode idle SSE stream may end independently of the native TUI.
  The plugin reconnects after an established-stream interruption, but reports
  degraded `unknown` after three consecutive established-stream failures or
  three failed reconnect setups. This preserves state monitoring without
  treating a transient stream reset as a completed run.
- Semantic `waiting_input`, generic installed-plugin supervision, chief/workspace
  guidance parity, production installation, and broader effort mappings remain
  out of scope for PR 1.

## Verification evidence

- `bun test` in `plugins/attn-opencode`: 18 tests, 49 assertions, including
  hanging health, SSE readiness, transient and repeated established-stream
  failures, post-subscription setup timeout, and stream cancellation cases.
- `go test ./internal/daemon -run 'TestPluginDirForSocketUsesSocketRuntimeRoot|TestDaemon_StartInstalledPlugins|TestPluginDriver|TestDelegate'`.
- `"$(go env GOBIN)/gotestsum" --format testdox -- ./...`.
- `make test-frontend`: 146 files, 1,431 tests.
- `make check-types`; generated Go and TypeScript files unchanged.
- `attn-dev` live proof: discovered the v2 plugin; completed distinct `max` and
  `low` GLM requests in separate loopback servers; showed both prompts and
  responses in the OpenCode TUI; observed steering `working` then `idle`;
  closed and resumed the same native session/model/variant; and confirmed the
  current run registry record was removed on close. No production app was
  built, installed, restarted, or used.
- `attn-dev` default-mode proof: an unpinned, promptless `spawn_session`
  launched the OpenCode TUI without a model or effort field; OpenCode displayed
  its own GLM 5.2/max default, a normal PTY prompt returned
  `OPENCODE_DEFAULT_LIVE_OK`, and the plugin persisted the resulting native ID,
  model, and variant before reporting `idle`. An explicit subsequent resume
  opened that same native session and visible conversation.
- Two independent native adversarial reviews: one found and verified fixes for
  observed-default metadata provenance, bounded HTTP/SSE setup, post-failure
  SSE cancellation, and launch cleanup; the other independently re-checked the
  daemon rollback path and found it clean. A final targeted pass found that
  repeated post-ready SSE failures needed their own bound; the fixed controlled
  adapter regression and both final reviewer checks are clean.
- Final exact-source `attn-dev` proof: the restarted dev daemon discovered the
  installed API-v2 plugin, a promptless OpenCode TUI visibly used its own GLM
  5.2/max default, returned `OPENCODE_FINAL_LIVE_V6_OK`, and attn reported
  that session idle. The preceding close/resume proof reopened the same native
  conversation with `pinned: false`, the observed GLM model/`max` variant, and
  `resume: true` in the private registry. No production app was used.

## Follow-ups

- Add mappings for more OpenCode effort variants only after adapter and live-app
  evidence for each mapping.
- Consider generic installed-plugin supervision separately; it is deliberately
  not part of this driver slice.
