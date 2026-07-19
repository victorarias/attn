# Plan: attn-pi driver plugin (rock 1)

## Why / Alignment

Rock 1 of [docs/vision/pi-attn-plugins.md](../vision/pi-attn-plugins.md): pi
launches, resumes, and lives as an attn session. Grounding evidence:
[docs/grounding/pi-plugins.md](../grounding/pi-plugins.md).

Aligned with Victor (2026-07-19):

- **Pure driver, dumb state.** No `state_reporting`, no screen-scraping
  detectors, no state stub. Sessions ride the daemon's existing
  no-state_reporting path: created directly in `working`
  (`internal/daemon/ws_pty.go:707,445-447`), stay `working` while pi runs,
  close normally on exit. Declared state arrives with the pi-side suite
  (rock 2).
- **Capabilities:** `resume`, `initial_prompt`, `model_pin`, `effort_pin`.
  No `yolo` (pi has no approval gate to bypass), no `launch_instructions`
  (delegation-grade staging deferred).
- **Version gate:** minimum supported pi version + refuse downgrade on
  resume (opencode pattern). Exact-pin ceremony waits for rock 2, where the
  unstable extension API actually bites; rock 1 touches only pi's stable CLI
  surface.
- **Single PR**, split only if it balloons past ~1k lines. The live smoke
  spike opens implementation and its evidence rides in the PR.

Deferred: state declaration, the pi-side attn suite, delegation targets,
safety envelope, kitty-graphics replay fidelity.

## Architecture Map

```text
Target (spawn):
attn UI spawn (agent=pi)
  -> daemon ws_pty spawn
    -> pluginRegistry.driver("pi") -> callPlugin "driver.spawn"
      -> attn-pi plugin (bun, plugins/attn-pi)
        -> check pi availability + version floor (`pi --version`)
        -> mint pi session id (uuid)
        -> build argv: pi --session-id <id> [--model <pin>] [--thinking <effort>] [<initial prompt>]
        -> report metadata {pi_session_id, pi_version} (daemon queues during launch)
  -> daemon launches argv in the attn-owned PTY (worker)
  -> session record created state=working (plugin has no state_reporting)

Target (resume):
daemon "driver.resume" with persisted metadata
  -> validate installed pi version >= metadata.pi_version
  -> argv: pi --session-id <same id> (+ re-applied pins)

Close/exit:
PTY child exit -> daemon close path -> "driver.session_closed" -> driver drops in-memory run tracking

Tests:
bun test (plugins/attn-pi/test)
  -> driver with fake rpc + fake runCommand
    -> argv mapping, version gate, resume metadata validation, session_closed
```

Plugin layout mirrors `plugins/attn-opencode`:

```text
plugins/attn-pi/
  attn-plugin.toml        # attn_api_version = 4, bun entrypoint
  package.json
  AGENTS.md / CLAUDE.md   # already written (grounding capture)
  src/index.ts            # rpc wiring: driver.spawn/resume/session_closed handlers
  src/driver.ts           # PiDriver
  src/attn-rpc.ts         # reuse opencode's rpc transport pattern
  src/types.ts
  test/
```

## Data Model / Interfaces

```ts
// resume token, persisted daemon-side via session.report_metadata,
// handed back verbatim on driver.resume
type PiMetadata = {
  schema: 1;
  pi_session_id: string;   // minted by the driver at spawn
  pi_version: string;      // `pi --version` at spawn; resume refuses downgrade
  model?: string;          // pinned model pattern, if any
  thinking?: string;       // pinned thinking level, if any
};
```

Flag mapping (verified against pi v0.80.10 `args.ts`):

- attn model pin -> `--model <pattern>`
- attn effort pin -> `--thinking <level>` (attn effort names are valid pi levels)
- initial prompt -> positional argument (interactive mode accepts it)
- resume -> `--session-id <id>` (creates if missing; same flag both ways)

## Boundaries

- The daemon owns the PTY, session records, and state; the plugin only decides
  what argv to run.
- The plugin must not report state, touch the PTY, or scrape screens.
- pi owns its session files (default session dir); attn correlates via the
  minted `--session-id`, never by reading pi's files.
- No on-disk run registry (unlike opencode): pi needs no port/password/prompt
  staging — everything travels as argv — and with no monitor there is nothing
  to reattach on plugin restart. `driver.register`'s `active_runs` response is
  acknowledged but requires no reconciliation work.

## Implementation Steps

- [x] Smoke spike (phase-2 opener): launch pi manually inside a
      throwaway-profile attn session; verify TUI render, OSC 11/kitty
      negotiation against the worker responder, resize behavior,
      `--session-id` create + resume round-trip, and the project-trust prompt
      flow. Capture evidence for the PR.
      Done 2026-07-19 (headless, throwaway profile pismoke, shell session over daemon WS): TUI renders under the worker PTY; resize 90x24/140x40 re-flows; --session-id create + resume restores history; --model/--thinking pins apply; ctrl+d exits to shell.
- [x] Plugin skeleton: `attn-plugin.toml`, `package.json`, `src/index.ts`
      rpc wiring (mirror attn-opencode).
- [x] `src/driver.ts`: availability + version floor, `driver.register`
      (capabilities above), spawn/resume argv mapping, metadata report,
      `driver.session_closed` cleanup.
- [x] Unit tests: argv mapping (pins/prompt/resume), version gate incl.
      downgrade-on-resume refusal, malformed metadata rejection.
      13 tests green (`bun test` in plugins/attn-pi).
- [x] Bundling: add attn-pi to `scripts/build-bundled-plugins.sh` (bun
      compile + generated executable manifest) so
      `attn plugin install-bundled attn-pi` works. Also added attn-pi
      sources to the `scripts/source-fingerprint.sh` includes.
- [x] Live verification on a throwaway profile (pismoke, daemon built from
      this branch): `attn plugin install-bundled attn-pi` -> driver
      registered (agent=pi) -> spawn with effort pin -> state=working +
      PiMetadata persisted -> real turn -> ctrl+d -> idle -> re-spawn same
      session id -> driver.resume restored history with pins re-applied.
      Headless over the daemon WS, not the app UI: the PR touches no
      frontend code, and the picker is dynamic (see Open Questions).
- [x] CHANGELOG entry; PR includes plugins/attn-pi guidance docs and
      docs/grounding/pi-plugins.md.

## Decisions

- No on-disk run registry — opencode's existed for port/password/prompt
  staging and monitor recovery; pi has none of those needs. Revisit only if
  rock 2 requires driver-side persistence.
- Dumb state deliberately shows `working` for the whole pi run. Accepted
  trade-off: no attention states for pi until rock 2; no false ones either.
- Version floor starts at the grounded version (0.80.10); raising it is a
  one-line change gated on reading pi's CHANGELOG.
- Initial prompt travels as a positional argv (visible in `ps`) — same
  exposure class as existing agent launches; not worth staging machinery.
- pi's --model flag is a fuzzy pattern, not an exact id: in the smoke run, "--model gpt-5.5" matched an azure-openai-responses provider rather than the default openai one. The driver passes the pin through verbatim; provider-qualified patterns are the user's tool if it matters.

## Open Questions

- ~~pi's project-trust prompt~~ Resolved 2026-07-19: first launch in a fresh cwd with a fresh ~/.pi showed no trust prompt — pi went straight to the input UI.
- ~~Frontend picker~~ Resolved 2026-07-19 in code: `driver.register`
  broadcasts settings (`internal/daemon/plugin_driver.go:199`), the settings
  payload publishes `pi_available=true` plus capability keys for registered
  plugin drivers (`internal/daemon/ws_settings.go:238-243`), and the
  frontend parses any `<agent>_available` key
  (`app/src/utils/agentAvailability.ts`) — pi appears in the picker with no
  frontend change.
- `pi install local:` on-disk layout expectations (matters for rock 2's suite
  staging, not for this PR).

## Follow-ups

- Rock 2: attn citizenship — pi-side suite, session linking, declared state,
  doorbell/nudge steering.
- Delegation support (`launch_instructions` via `--append-system-prompt`).
- Kitty graphics images through vt10x snapshot/replay (fidelity check).
