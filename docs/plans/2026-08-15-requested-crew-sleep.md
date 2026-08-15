# Plan: requested crew sleep and live bindings

## Goal

Let Victor ask an awake crew member to close its day and sleep, without killing
it or waking a successor. A crew binding must represent a live process, so a
dead day releases its seat immediately and a later wake can always start a new
day.

## Architecture Map

```text
CLI `attn crew sleep <member>` / awake-row moon button
  -> crew_sleep command
    -> daemon resolves the live member day
      -> persist a user-originated sleep request
      -> deliverAgentMessage to the member's composer
    <- delivered/queued or named already-asleep result

PTY exit
  -> handlePTYExit
    -> release the exited crew binding
    -> retain its id for the next wake receipt
      -> crew.released fact -> crew_updated snapshot

crewWake
  -> inspect stored binding
    -> probe the bound runtime
      live -> AlreadyAwake
      exited -> release binding -> launch a fresh day
```

## Data Model / Interfaces

```text
crew_sleep { member, request_id? }
  -> { member, session_id?, already_asleep, delivery_status?, detail }

crew_wake result
  + released_session_id?  // the dead day displaced by this wake
```

The sleep request uses the durable agent-message queue and its delivery rail,
but has user-origin composition. It must not masquerade as a message from
another agent.

## Boundaries

- The member consents to closure by filing `attn handoff --sleep`; the request
  never kills the process or edits the binding itself.
- The daemon owns binding liveness, release, command results, and roster pushes.
- The frontend renders the result and roster. It does not infer that a delivered
  request means the member has already slept.
- Plain `attn handoff` remains presence-decided. `--sleep` and `--nap` are the
  explicit overrides.

## Implementation Steps

- [x] Add the sleep request protocol, daemon handlers, client, CLI, and tests.
- [x] Add the awake-row sleep control, socket correlation, and frontend tests.
- [x] Teach auto-sleep and wake priming the three handoff flag semantics.
- [x] Release crew bindings on PTY exit and probe liveness before AlreadyAwake.
- [x] Update glossary/changelog and regenerate protocol types.
- [x] Run focused tests, `make test-quick`, and the frontend suite.
- [x] Verify both scenarios in an isolated installed profile and record evidence.
- [ ] Rebase on main, commit, push, and open a ready-for-review PR.

## Decisions

- The awake row gets a direct moon button beside its actions button: sleep and
  wake are lifecycle twins, and the generic session menu does not own crew
  identity.
- A sleep request is persisted before delivery. If the composer cannot take it,
  the result says queued and the existing state-change drain retries it.
- An inconclusive runtime probe is an error, never `AlreadyAwake`; only an
  observed live process may keep the seat.
