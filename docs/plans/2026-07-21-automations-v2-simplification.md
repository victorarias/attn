# Plan: Automations v2 — simplification and rebuild

## Goal

Deliver the [attn Automations vision](../vision/attn-automations.md) on a
foundation Victor can trust. The v1 build (PRs #597–#623) shipped the behavior
but inverted the intended architecture: the engine and delivery modules are
ceremony while the real logic sits in `internal/daemon/automations.go` (1,587
lines) and lifecycle state machines embedded in store SQL. State is inferred
(withdrawal = failure sentinel string; continuity re-derived by replaying run
history; `enabled` has two authorities), which is where the v1 fix storms
concentrated. The feature is unused in production, so v2 is a clean-slate
rebuild of the internals with no back-compat: explicit state, one protocol
surface, and a structured form UI replacing the YAML editor. YAML remains the
CLI/agent interchange format only.

Aligned 2026-07-21: form UI with YAML for CLI only; clean-slate internals;
trust first (state model before UI).

## Architecture Map

```text
Current (v1):
observation / CLI / WS
  -> internal/daemon/automations.go        (all lifecycle decisions inline)
    -> internal/workdelivery               (7-port pass-through, zero logic)
      -> back into *Daemon port methods    (tickets/git/panes/sessions)
    -> internal/store/automations.go       (state machines inside SQL claims)
  internal/automation                      (schema/validation only)

Target (v2):
observation / CLI / WS
  -> daemon automation handlers (one command layer for socket + WS)
    -> internal/automation engine          (owns every durable state decision:
         claim, retry, cancel, continuity resolution, schedule evaluation,
         recovery decisions — against an explicit Store interface)
    -> daemon materialization              (one linear deliver function:
         ticket -> location -> workspace -> pane -> session -> verify;
         fresh / continue / resume forks decided ONCE, up front)
      -> internal/store                    (dumb rows + atomic transitions,
         no policy in SQL)

Tests:
engine unit tests -> in-memory Store fake + injected clock (no daemon)
daemon integration -> real SQLite via ScopeTestEnvironment
live -> packaged scenarios on a fresh throwaway profile (both proving cases)
```

## Data Model (v2 tables, clean-slate migration)

```text
automation_definitions
  id, name, enabled (COLUMN IS THE ONLY AUTHORITY — not in spec),
  revision, spec_json (canonical; spec_yaml dropped — YAML parsed/rendered
  at the CLI boundary only), created_at, updated_at, deleted_at

automation_runs
  id, definition_id, occurrence_id, definition_revision, snapshot_json,
  state: pending | delivered | failed | cancelled,
  cancel_reason: review_withdrawn | definition_disabled | definition_deleted,
  attempts, last_error, ticket_id/session_id/workspace_id/pane_id,
  resolved_location_json, timestamps
  -- withdrawal sentinel string and ticket-comment string-matching are gone

automation_continuity_bindings
  definition_id, continuity_key, ids...,
  status: active | released, released_reason: contract_rotated |
  ticket_swept | definition_deleted, timestamps
  -- rows are NEVER deleted; delivery reads binding status directly.
  -- hasPriorAutomationContinuityRun + run-history replay are deleted.

automation_review_request_edges
  definition_id, subject_key, host, active, cycle, timestamps
  -- accepted_cycle dropped: candidacy = "no run exists for (subject, cycle)";
     a pending run for the subject is re-delivered on each observation, so a
     retryable failure can no longer strand a run until daemon restart.

automation_occurrences, automation_provider_cursors: shape as v1.
```

Trigger spec absorbs the real policy choices; the policy block is deleted:

```yaml
# scheduled — the only trigger with genuine choices
trigger:
  type: scheduled
  schedule: { cron: "0 7 * * *", time_zone: America/New_York }
  continuity: fresh | singleton     # was policy.continuity
  catch_up: skip | latest           # was policy.catch_up
# manual            -> always fresh (implied)
# github_review_... -> always per_subject + latest + worktree (implied)
# overlap: only ever accepted "coalesce" — deleted until a second value exists
```

`ContinuationContract` (prompt/launch/location) survives unchanged — it is a
real concept and drives binding rotation.

## Boundaries

- `internal/automation` owns every durable state transition and the decision
  of what to do next; it never touches tickets, git, PTYs, or panes.
- The daemon owns materialization (tickets/worktrees/workspaces/sessions) as
  plain sequential code; `internal/workdelivery` is deleted, the
  `automationDeliveryHook` test seam remains.
- The store owns atomicity of transitions the engine asks for; no
  reconciliation/candidacy/retention policy expressed in SQL.
- Protocol: one command set and one handler layer shared by unix socket and
  WS; both return the same shaped summary types (never raw store rows);
  per-action result messages replace the 13-field grab-bag.
- Frontend: structured spec editing only (form -> JSON spec over WS);
  the app never parses or renders YAML.

## Implementation Steps

- [x] **PR1 — mechanical split.** Delete `internal/workdelivery` (inline the
      seven steps into one daemon deliver function); split
      `daemon/automations.go` by concern (definitions / observe-github /
      deliver / recover). No behavior change; tests move along. Live-verified
      on the dev profile (manual run reached `delivered`).
- [x] **PR2a — definitions: enabled + spec storage.** Migration 76 recreates
      `automation_definitions` without `spec_yaml`; `enabled` becomes
      column-only (spec loses the field; YAML containing `enabled:` is a
      parse error pointing at `attn automation enable|disable`); new CLI
      verbs `attn automation enable|disable`; `SetEnabledInYAML` and the
      spec-rewrite path in `SetAutomationEnabled` are deleted; the WS
      definition-YAML surface falls back to rendering YAML from spec JSON. Shipped as #629.
- [ ] **PR2b — v2 state model + claim semantics** (consolidates former
      PR2b/PR3/PR4 — one migration, one live-verify matrix, one review
      round). Migration 77 recreates `automation_runs` (adds `cancel_reason`,
      `attempts`), `automation_continuity_bindings` (append-only: surrogate
      id, `status` active|released, `released_reason`, `released_at`; unique
      active row per (definition, continuity_key)), and
      `automation_review_request_edges` WITHOUT `accepted_cycle`.
      Withdrawal / disable / delete become `cancelled` + reason; the
      `AutomationReviewWithdrawnError` sentinel and
      `hasPriorAutomationContinuityRun` history replay are deleted — delivery
      reads binding status directly. Go constants replace bare state strings.
      Candidacy = "no run exists for (subject, cycle)"; pending runs are
      re-delivered on each observation/tick (kills the stuck-pending class);
      scheduled cursor logic simplified to match. Policy block deleted:
      trigger-implied per the YAML sketch above; `scheduled` absorbs
      `continuity`/`catch_up`; validation matrix and snapshot shrink. Engine
      `Store` interface lands here. Built as sequential reviewable commits on
      one branch.
- [ ] **PR5 — protocol unification.** One handler layer for socket + WS;
      per-action results; drop dead wire fields (`continuity`, `catch_up`,
      `workspace_id`, `definition_revision`); embed `last_run` in the
      definition summary (kills the panel's N+1); protocol version bump
      (remember the useDaemonSocket.ts third lockstep spot).
- [ ] **PR6 — form UI.** Replace the YAML editor and validate-without-apply UI
      with a structured create/edit form (trigger picker with per-trigger
      fields, prompt, launch via the delegation picker components, location,
      enabled, delete affordance). Typed save errors (revision conflict is
      distinguishable); `expected_revision` guard survives.
- [ ] **PR7 — proving matrix.** Both vision proving cases end-to-end on a
      fresh profile (PR pre-review with continuity + scheduled worktree
      cleanup), daemon-restart recovery leg, changelog.

## Decisions

- Clean-slate migration over in-place evolution: the feature has zero real
  usage, so back-compat shims would preserve ceremony nobody depends on.
- YAML survives only at the CLI boundary (`attn automation apply|show`); the
  store keeps canonical JSON and the app edits structured spec. The in-app
  YAML editor (PR #623) is removed, which deletes the `enabled` split-brain
  class and most of the editor's concurrency machinery by construction.
- Policy block deleted rather than kept as forward surface: v1 proved knobs
  that accept one value still cost validation, snapshotting, wire fields, and
  contract-comparison complexity. Re-adding a value later is cheap.
- Withdrawal/disable/delete become `cancelled` + reason rather than a fourth
  state per cause: one terminal branch for the UI and retention to reason
  about.
- Continuity bindings become append-only with status instead of
  delete-then-infer: v1's three "found the hard way" inference subtleties were
  each production bugs; explicit state makes them unrepresentable.
- PR sizing is by coherence and CI-round efficiency, not line count (Victor
  removed the 1k-line preference 2026-07-21): every daemon PR costs CI +
  reviewer round + macOS live-verification evidence, so the former
  PR2b/PR3/PR4 are one consolidated PR with the final schema in a single
  migration 77. PR2a had already shipped separately (#629).
- Migration numbering: production `~/.attn/attn.db` is at 73 — the v1
  automation migrations 74–75 never reached production. v2 clean-slate
  migrations are 76/77 and must apply from both 73 and 75; they also null
  `tickets.automation_run_id` since all runs are wiped.
- `SetAutomationEnabled` no longer bumps `revision`: revision guards spec
  content, and enabled is no longer spec content. Enable still clears review
  edges and fences provider cursors, as in v1.
- Active binding whose ticket no longer exists self-heals at delivery time
  (release with `ticket_swept`, log, deliver fresh) rather than hard-failing:
  one well-defined transition, not inference.

## Open Questions

- Form UX for repository source overrides (identity -> local clone path map):
  full map editor in v2, or defer to CLI-only until it is actually used?
- Run-now for github-review definitions in the form UI: prompt for a PR URL,
  or defer run-now to manual/scheduled definitions in the first cut?
- Retention treatment of `cancelled` runs: same keep-window as failed, or
  shorter?

## Follow-ups

- Mark the remaining unchecked slices in
  `2026-07-18-attn-automations-implementation.md` as superseded by this plan.
- Future triggers from the vision (comments, Slack checks) enter as new
  trigger types against the v2 engine seam — first new one stresses the
  provider seam per the vision's hardening rock.
