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

- [ ] Smoke spike (phase-2 opener): launch pi manually inside a
      throwaway-profile attn session; verify TUI render, OSC 11/kitty
      negotiation against the worker responder, resize behavior,
      `--session-id` create + resume round-trip, and the project-trust prompt
      flow. Capture evidence for the PR.
- [ ] Plugin skeleton: `attn-plugin.toml`, `package.json`, `src/index.ts`
      rpc wiring (mirror attn-opencode).
- [ ] `src/driver.ts`: availability + version floor, `driver.register`
      (capabilities above), spawn/resume argv mapping, metadata report,
      `driver.session_closed` cleanup.
- [ ] Unit tests: argv mapping (pins/prompt/resume), version gate incl.
      downgrade-on-resume refusal, malformed metadata rejection.
- [ ] Bundling: add attn-pi to `scripts/build-bundled-plugins.sh` (bun
      compile + generated executable manifest) so
      `attn plugin install-bundled attn-pi` works.
- [ ] Live verification on a throwaway profile per AGENTS.md (preflight
      first): spawn from the app UI, type a turn, quit, resume, confirm
      session lifecycle + working/idle states.
- [ ] CHANGELOG entry; PR includes plugins/attn-pi guidance docs and
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

## Open Questions

- pi's project-trust prompt: does trust persist across launches per cwd, or
  will every attn spawn re-prompt? Smoke spike answers; if it re-prompts,
  consider `--approve` implications instead of accepting the friction.
- Frontend: pi should appear in the agent picker automatically via
  `broadcastSettings` on driver registration — verify in smoke; if not, a
  small frontend follow-up is needed.
- `pi install local:` on-disk layout expectations (matters for rock 2's suite
  staging, not for this PR).

## Follow-ups

- Rock 2: attn citizenship — pi-side suite, session linking, declared state,
  doorbell/nudge steering.
- Delegation support (`launch_instructions` via `--append-system-prompt`).
- Kitty graphics images through vt10x snapshot/replay (fidelity check).
