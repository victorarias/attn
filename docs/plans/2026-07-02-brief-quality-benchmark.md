# Plan: Real-agent benchmark for delegation-brief quality

## Goal

Make "the chief writes good briefs" a tested property instead of vibes. A real-agent
harness benchmark stands up a REAL chief (create-as-chief, isolated **uat** profile),
hands it one realistic human delegation ask — one that references a behavior by
symptom (so the chief must scout the repo before it can name files) and carries one
genuine design ambiguity — and then only observes. The minted ticket's description IS
the brief (`createDelegatedTicket` in `internal/daemon/delegate_ticket.go` sets
`Description: brief`), so deterministic structural checks on that description measure
brief craft directly: outcome/stop-condition, a real repo path, a verification line,
and the ambiguity surfaced as an open question for the user rather than resolved by
the chief. This benchmarks the guidance shipped by the **chief-guidance-brief-craft
plan** (same wave — it owns `hooks.ChiefGuidance`); this plan lands after it.

## Dependency

- **Lands after `chief-guidance-brief-craft`.** The scenario's guidance gate greps the
  chief's launch args for a phrase unique to the brief-craft guidance block (the same
  trick `chiefGuidanceProcesses()` in `scenario-chief-ticket-watch.mjs` uses with
  "arm a harness Monitor running"). Run before that plan lands, the benchmark fails
  fast with `setup-failed-no-guidance` — a setup verdict, never a behavioral one.

## Architecture Map

```text
Current (precedent, all verified):
  app/scripts/real-app-harness/scenario-chief-ticket-watch.mjs
    create-as-chief (create_session {chief_of_staff:true}) on uat
    -> gates: role (chief_of_staff_get_state), guidance (ps grep), model (pane status line)
    -> write_pane human prompt -> observe ticket_list for a NEW bound ticket
    -> verdicts in runDir/summary.json; exit 0 unless the harness itself broke
  Benchmark-support configs (docs/plans/2026-06-28-delegated-ticket-awareness.md):
    auto_approve_enabled, chief_model_<agent> via set_setting before create_session

Target (one new scenario + one shared pure helper):
  pnpm --dir app run real-app:scenario-chief-brief-quality -- --agent claude [--chief-model opus]
    -> scenario-chief-brief-quality.mjs
       seed fixture repo "exportcli" in sessionDir (symptom lives in src/timeformat.js)
       -> launchFreshAppAndConnect (common.mjs; buildPreflight source-fingerprint guard)
       -> clearAnyChief -> set_setting auto_approve_enabled=true, chief_model_<agent>
       -> create_session {cwd: repoDir, agent, chief_of_staff: true}
       -> gates (role / guidance / model), copied from scenario-chief-ticket-watch
       -> write_pane HUMAN_ASK (text, 1.2s gap, lone '\r')     // observe only from here
       -> poll bridge ticket_list until NEW ticket with assignee != chiefId
       -> runAttn(['ticket','list','--json']) -> row by id -> .description   // bridge
          ticket_list omits description (useUiAutomationBridge.ts) — the CLI carries it
       -> briefQuality.mjs assessBrief(description, {repoDir}) -> 4 checks
       -> summary.json: verdict + per-check evidence + chief model/effort + pane snaps

Tests (pure helper only — the scenario IS the test):
  pnpm --dir app test briefQuality
    -> scripts/real-app-harness/briefQuality.test.mjs (vitest already includes
       scripts/real-app-harness/**/*.test.mjs in node env — vite.config.ts test.include)
```

## Data Model / Interfaces

```ts
// briefQuality.mjs — pure, no daemon/app coupling
type BriefCheck = { id: 'outcome' | 'repo_path' | 'verification' | 'open_question';
                    pass: boolean; evidence: string };   // matched text / found path / ''

export function extractCandidatePaths(text: string): string[]
// tokens shaped like paths: /[\w./-]*\/[\w.-]+/ plus bare filenames \b[\w-]+\.(js|md|json|ts|go)\b,
// trailing punctuation stripped, backticks unwrapped.

export function assessBrief(description, { repoDir, exists = fs.existsSync }): BriefCheck[]
// outcome:       /\b(done when|stop when|acceptance|success (criteria|means|looks like)|definition of done|deliverable|outcome|ready for review when)\b/i
// repo_path:     some candidate path exists at token or path.join(repoDir, token)
// verification:  /\b(verify|verification|node --test|npm test|run (the )?tests?|reproduce (it|the bug)|confirm (the )?fix)\b/i
// open_question: deferral marker AND ambiguity keyword must co-occur:
//   deferral:  /\b(open question|ask (the )?user|check with (the )?user|align with (the )?user|confirm with (the )?user|user'?s call|do(n't| not) decide)\b/i
//   ambiguity: /\b(--utc|utc flag|in[- ]place|local[- ]time formatting)\b/i

// summary.json (chief-watch shape, extended)
{ runId, profile, agent, chiefModel, effort,          // effort scraped, see Decisions
  verdict, ticketId, description,                     // full brief text for manual read
  briefChecks: BriefCheck[], steps: [...], ... }
```

Fixture repo (seeded per run in `sessionDir/exportcli`, git init + commit like the
chief-watch seed): `README.md` (quickstart: `node src/cli.js export --format csv
data/sample.json`, and "run `node --test test/` before sending changes"), `package.json`,
`src/cli.js` (arg parse → export), `src/export.js` (rows via timeformat),
`src/timeformat.js` (**the symptom**: builds local timestamps with
`Math.floor(d.getTimezoneOffset() / 60)` — sign/rounding shifts the date on negative
UTC offsets), `src/config.js`, `data/sample.json`, `test/export.test.js` (one passing
`node:test` case that does NOT cover the bug). Enough structure that scouting is real
work and a lazy brief ("fix the export bug") fails the `repo_path` check honestly.

The human ask (verbatim; one shared constant for both agents — cross-model
comparability is the point):

```
hey — a user reported that the export command writes timestamps a day off when
their machine is on a negative UTC offset (they're in UTC-7). can you get someone
going on a fix? one thing i keep going back and forth on: whether we should fix
the local-time formatting in place or add a --utc flag and steer people to that
instead. i'm out for the afternoon — keep me posted
```

Symptom, not file ("the export command writes timestamps a day off"); the ambiguity
(fix in place vs `--utc` flag) is voiced as human indecision, never as an instruction.
No mention of tests, tickets, briefs, or that this is a benchmark.

## Boundaries

- **Observe, never coach.** After the ask is typed, the scenario sends nothing else to
  the chief — no second prompt, no corrections, no reveal that it is a test. A chief
  that finishes without delegating is a verdict to discuss, not a thing to auto-fix
  (chief-watch's `did-not-delegate` rule).
- **Zero product-code changes.** No daemon/protocol/frontend edits; the benchmark rides
  existing surfaces. Spine intact: the board informs, attn authors only `crashed`,
  doorbells stay content-free — nothing here touches any of that.
- **`briefQuality.mjs` is pure** (strings + an injectable `exists`), so its unit tests
  need no app, daemon, or tmp fixtures. All PTY/daemon plumbing stays in the scenario.
- **Heuristics judge structure, not prose quality.** Deterministic regex/fs checks
  only — no LLM judging. The `open_question` check approximates "not resolved by the
  chief" as deferral-marker + ambiguity-keyword co-occurrence; proving non-resolution
  mechanically is out of scope (read the pane snapshots when it matters).
- **Single-tenant, never prod.** Packaged-app scenarios run serially; the profile guard
  (`currentHarnessProfile()` throw, copied from chief-watch) refuses to run without a
  named profile.

## Implementation Steps

- [ ] **`app/scripts/real-app-harness/briefQuality.mjs`** — `extractCandidatePaths` +
      `assessBrief` per the interface above, plus `briefQuality.test.mjs` (vitest node
      env, picked up by the existing `scripts/real-app-harness/**/*.test.{ts,mjs}`
      include; mirror `common.test.mjs` style). Tests: each check passes/fails on
      crafted briefs; path extraction handles backticks, trailing punctuation,
      absolute + repo-relative; a brief naming only nonexistent paths fails `repo_path`.
- [ ] **`app/scripts/real-app-harness/scenario-chief-brief-quality.mjs`** — copy the
      plumbing from `scenario-chief-ticket-watch.mjs` (`parseArgs`, `pollFor`,
      `resolveAttnBin`, `makeAttnRunner`, `shell`, `chiefGuidanceProcesses`,
      `setChiefOfStaff`, `clearAnyChief`, `readChiefPane`; scenario scripts stay
      self-contained by convention — `scenario-nudge-trigger.mjs` and
      `scenario-ticket-delegation-lifecycle.mjs` duplicate the same helpers). Flow:
      seed fixture repo (+ `preTrustClaudeFolder` for claude), clear leftover chief,
      `set_setting auto_approve_enabled=true` + `chief_model_<agent>` before
      `create_session {chief_of_staff:true}` (ordered-WS guarantee, chief-watch step
      0b/0c), the three gates, `write_pane` the ask (text then lone `\r` after 1.2s —
      the bracketed-paste trap), ticket-baseline snapshot, observe until a NEW bound
      ticket (window **360s** — scouting adds turns over chief-watch's 240s), fetch
      the description via `runAttn(['ticket','list','--json'])`, `assessBrief`,
      write `summary.json`, teardown (close worker + demote/close chief, restore
      `auto_approve_enabled` + `chief_model_<agent>`, quit app). Add `--chief-model`
      flag overriding the `CHIEF_MODELS` default (`claude: 'opus'`, `codex: ''`) so
      the model-dependence comparison is a re-run, not an edit.
- [ ] **Guidance-gate marker** — once chief-guidance-brief-craft lands, pick a phrase
      unique to its guidance block (appears in `ChiefGuidance` text only, never in a
      command an agent would run) and use it in this scenario's
      `chiefGuidanceProcesses()` grep, keeping the existing
      "arm a harness Monitor running" match as a second required hit.
- [ ] **npm wiring** — add to `app/package.json` scripts:
      `"real-app:scenario-chief-brief-quality": "node scripts/real-app-harness/scenario-chief-brief-quality.mjs"`.
      Do NOT add it to `run-serial-matrix.mjs` (benchmarks burn real model tokens and
      end in verdicts, not pass/fail — same reason chief-watch isn't in the matrix).
- [ ] **Run it** — `make install PROFILE=uat` from this branch, then the claude
      variant; record verdict + `summary.json` highlights in this plan. No CHANGELOG
      entry (internal harness only, per AGENTS.md).

## How to read results

`runDir/summary.json` (path printed at exit; pane snapshots `0*-*.txt` alongside).
Always inspect pane text + screenshots on failure — startup prompts and permission
dialogs are part of the evidence. Verdicts:

| verdict | meaning |
|---|---|
| `setup-failed-no-role` / `-no-guidance` / `setup-model-not-pinned` | harness/setup miss (stale chief, guidance not in launch args, model pin didn't thread) — rebuild/reset, re-run; never a behavioral finding |
| `prompt-not-accepted` | the typed ask never submitted (chief never hit `working`) — re-run |
| `blocked-on-approval` | chief stalled on a permission gate despite auto-approve — decide posture, re-run |
| `inconclusive-still-working` | still deliberating at the 360s deadline — bump window or re-run |
| `did-not-delegate` | chief finished without delegating (did it itself, or asked-and-waited past the "I'm out" premise) — a finding to DISCUSS, not auto-correct |
| `brief-pass-4of4` | delegated; all four checks passed |
| `brief-partial-<n>of4` | delegated; `briefChecks` lists each failed check with evidence — read `description` in the summary before judging the heuristic |

Every result records `agent`, `chiefModel`, and `effort` — compare like with like:
behavior is known to be model-dependent (Opus ran the full delegate→watch loop;
Sonnet did the work itself on the same guidance).

## Verification

```bash
pnpm --dir app test briefQuality                 # helper unit tests
make install PROFILE=uat                          # rebuild first — the source-fingerprint
                                                  # guard (buildPreflight.mjs) compares the
                                                  # bundle to the working tree; harness .mjs
                                                  # edits alone are re-run free
ATTN_HARNESS_PROFILE=uat pnpm --dir app run real-app:scenario-chief-brief-quality -- --agent claude
# single-tenant: one packaged-app scenario at a time, never in parallel
```

## Decisions

(Settled with Victor — captured, not re-litigated.)

- **Real-agent benchmark, not a unit test.** The scenario IS the test; unit tests only
  for the new pure helper (`briefQuality.mjs`). Extends the chief benchmark machinery
  (`scenario-chief-ticket-watch.mjs`, uat profile, create-as-chief, benchmark-support
  configs `auto_approve_enabled` + `chief_model_<agent>`).
- **The ask requires scouting and carries one genuine ambiguity.** Symptom-referenced
  (no file names), fix-in-place vs `--utc` flag left hanging. Never coach the chief;
  never tell it it is a test.
- **Four deterministic checks on the ticket description** (== the brief, verified in
  `createDelegatedTicket`): outcome/stop-condition phrasing; ≥1 concrete repo path
  that exists in the fixture repo; a verification line; the ambiguity surfaced as an
  explicit open question deferring to the user — not resolved by the chief.
  Regex/structure heuristics, no LLM judge.
- **Record chief model + effort with each result.** Model = the `chief_model_<agent>`
  pin (gated against the pane status line, chief-watch step 3b). Effort: attn has no
  chief effort pin (only `SettingChiefModelPrefix` exists in `ws_settings.go`), so it
  is scraped best-effort from the ready pane (`xhigh|high|medium|low`), else recorded
  as `unknown (agent default; no chief effort setting)` — the same confound the
   2026-06-28 benchmark logged. Reality adapted; a pin is a follow-up, not forced here.
- **CLI JSON, not the bridge, for the description.** The bridge's `ticket_list`
  (useUiAutomationBridge.ts) returns id/title/status/assignee only; `attn ticket list
  --json` carries `description`. The bridge read still drives new-ticket detection.
- **Benchmark exits 0 on behavioral verdicts** (chief-watch precedent): nonzero means
  the harness broke, the verdict lives in `summary.json`. Not matrix-wired.

## Open Questions / Follow-ups

- Guidance-gate marker phrase — pick from the landed brief-craft `ChiefGuidance` text
  (blocked on that plan merging).
- `chief_effort_<agent>` pin (mirror of `chief_model_<agent>`) to kill the effort
  confound in cross-model runs — separate small PR if the comparison matters.
- npm wiring for the existing `scenario-chief-ticket-watch.mjs` (it has none today) —
  trivial follow-up while touching `package.json`, if wanted.
- If the `open_question` heuristic proves too brittle across runs (chiefs phrase
  deferral many ways), widen the alternation from observed pane evidence before
  reaching for anything non-deterministic.
