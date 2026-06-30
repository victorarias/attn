# Plan: stop a delegated (non-chief) agent from re-delegating

Ticket: `delegated-agents-not-chief`. **Implemented.** Two instruments: a
**delegate-time leaf line** (code, closes the zero-signal leaf) and a **skill
role front-door + installer orphan-pruning fix** (prose + code, closes the
read-the-skill misread). See "What actually shipped" below.

## The bug

A delegated agent delegated its own task onward to a third agent. It had read
the attn skill and applied the "delegate if you're the chief" framing to itself.
Nothing told it that it is a *leaf*, not the chief.

## Root cause: an inference gap, not disobedience

The agent did not ignore a rule — it lacked the information to know the rule
applied to it. attn marks the **chief** with a concrete, passive, startup-time
signal; it marked a **leaf** with nothing analogous.

| Role | Passive env signal | Initial-prompt framing | System prompt | Active query (`attn list --json`) |
|---|---|---|---|---|
| Chief of staff | **`ATTN_CHIEF_GUIDANCE`** present | n/a | "You are the chief of staff…" (`ChiefGuidance`, `hooks.go:130`) | `chief_of_staff: true` |
| Tracked leaf (delegated *by chief*) | none | ticket-report contract appended (`delegate.go:418-440`) | non-chief `AgentInstructions` | `delegated_from_chief: true` |
| Ordinary session | none | none | non-chief `AgentInstructions` | both absent |
| **Leaf delegated by a non-chief** (the bug) | **none** | **none — raw brief** | **non-chief `AgentInstructions`** | **both absent — identical to ordinary** |

The chief is *positively* marked. A leaf was defined only by the *absence* of
the chief markers — an absence it shares with every ordinary top-level session.
The skill's chief-vs-leaf distinction therefore rested on the agent
**inferring** a role for which no positive signal was plumbed to it.

### The actual mechanism behind the reported incident — a stale install, not a live design defect

Initial analysis (this plan's first draft) diagnosed the misread as caused by a
file `references/chief-of-staff.md` that bundled the chief's delegate license
with leaf reporting guidance, and was reachable by a tracked leaf via its own
load-trigger text. That file is real — but **it does not exist in the skill's
canonical source** (`internal/agent/attn_skill/`). It was deliberately deleted
at commit `b1fad9b0` ("feat(tickets): chief watch + daemon backstop for
delegated-ticket awareness"), as part of an earlier, intentional refactor that
moved the chief's delegation license into the always-on, Go-injected system
prompt (`hooks.ChiefGuidance`) and left `delegated-agent.md` as the leaf-only
home — already tested against carrying chief content back
(`attn_skill_test.go`'s `chiefOrLegacy` checks).

The copy of `chief-of-staff.md` actually read during this investigation came
from `~/.claude/skills/attn/references/` — an **installed**, not source,
location. `installAttnSkill` (`internal/agent/attn_skill.go`) only ever
writes/overwrites files present in the current embed; it never deleted files
that fell out of the bundle. So a reference retired from source in late June
survived indefinitely on an already-installed machine, fully able to keep
teaching a delegated leaf the old, no-longer-current "use the normal `attn
delegate` workflow" framing. **This — an orphaned installed file, not a gap in
the current skill's design — is the direct, concrete mechanism of the reported
incident.** Fixed by adding orphan-pruning to the installer (below).

### Two misread vectors — they needed different instruments

- **(a) The read-the-skill misread.** A *chief-delegated* (tracked) leaf opens
  stale chief-only content (the orphaned `chief-of-staff.md`) and bleeds the
  chief's delegate license onto itself. Closed by installer pruning (removes the
  stale file) plus a role front-door in `SKILL.md` as defense-in-depth, so even a
  leaf reading delegation content for an unrelated reason sees the role gate
  before anything else.
- **(b) The zero-signal leaf.** A *non-chief-delegated* leaf has no env marker,
  no prompt framing, and no load-trigger pointing it at any leaf reference. It
  may never open the skill at all (the lazy-load-miss flagged in
  `docs/plans/2026-06-28-delegated-ticket-awareness.md`). Prose alone cannot
  reach it — only a spawn-time signal does. Closed by the delegate-time leaf
  line, injected into *every* delegation's initial prompt.

## What actually shipped

### 1. Delegate-time leaf line (`internal/daemon/delegate.go`)

`withLeafIdentity` prepends a terse identity preamble to every delegated
agent's initial prompt — tracked or not, chief-delegated or not. Composes with
the existing `delegatedTicketPrompt` ticket-report contract for tracked leaves
(identity line + report contract + brief) and is the only signal for a
non-chief delegation (identity line + brief). Tests updated:
`TestDelegateSpawnsAgentInSourceWorkspaceWithBrief`,
`TestDelegateAcceptsCopilotInitialPrompt` (both previously asserted the prompt
equaled the raw brief; now assert it contains the leaf line + the brief).

### 2. Installer orphan-pruning (`internal/agent/attn_skill.go`)

`installAttnSkill` now collects the set of paths the current embed expects and
calls `pruneOrphanedSkillFiles` to remove anything under the installed skill
dir that isn't in that set. This is the actual fix for the reported incident's
mechanism: a retired reference can no longer survive a future install. Covered
by `TestEnsureAttnClaudeSkillInstalledPrunesOrphanedFiles`, which seeds a
leftover `chief-of-staff.md` and an orphaned subdirectory and asserts both are
gone after install. Verified live: rebuilt and installed `attn-dev.app`
(`make dev`), confirmed the real `~/.claude/skills/attn/references/
chief-of-staff.md` (Victor's own machine, dated 2026-06-28) was removed by the
next settings-driven install pass.

### 3. Skill role front-door (`internal/agent/attn_skill/SKILL.md`)

Added a "Confirm Your Role First" section ahead of the Capability Index — the
first thing any agent that loads the skill sees, regardless of why it loaded
it. States the three roles (chief / delegated leaf / ordinary session) and what
delegation means for each, before any capability-routing happens. The
`assertAttnSkillTree` byte-budget test (`len(index) > 3000`) that previously
held `SKILL.md` to ~2930/3000 bytes was removed (Victor's call: "that test
should must go") — it left no room for genuinely load-bearing content; the file
is now governed by reviewer judgment instead, same as any other reference.

### 4. `delegated-agent.md` — the leaf's home, restructured

Split into two sections reflecting what's actually true at runtime: "You Are A
Leaf, Not A Coordinator" (universal — applies to every delegated leaf, carries
the positive identity rule) and "If Your Work Is Tracked, Report Your State"
(conditional — only chief-tracked leaves get the ticket-report contract). Load
trigger broadened from "your task says your work is tracked" to "you are a
delegated leaf" (matches the now-universal delegate-time line).

### 5. `delegation.md` — pointer to the role check

Replaced the self-applicable "while you continue coordinating the wider task"
framing (read naturally by a leaf as describing itself) with a one-line pointer
back to `SKILL.md`'s role check and an explicit "if that might be you, read
`delegated-agent.md` first" redirect.

### Not done (re-scoped after the chief-of-staff.md finding)

The first draft of this plan also proposed moving `notebook.md`'s "As Chief Of
Staff" coda into a (to-be-recreated) `chief-of-staff.md`, and giving
`chief-of-staff.md` a dedicated home for board-reading altitude. Both are
**dropped**: recreating `chief-of-staff.md` would undo the deliberate
commit-`b1fad9b0` refactor that moved chief identity into the always-on system
prompt specifically so it doesn't depend on a skill-file load. `notebook.md`'s
coda is real but minor (journaling altitude, not a delegation-license grant —
not part of this bug's mechanism) and is left as a follow-up, not bundled into
this fix.

## One leaf definition, shared in spirit between code and prose

The delegate-time preamble (terse, runtime) and `delegated-agent.md`'s "You Are
A Leaf" section (fuller, with the why) say the same thing in different
registers — both: you are a leaf, not a coordinator; do the work here; native
subagents for your own subtasks, not `attn delegate`; delegate again only if
the user steering *this* session explicitly asks.

## Decisions — consequences of being "too strong" (why we do NOT just ban delegation)

The leverage is in the *signal*, not the *imperative*.

1. **Strength can't substitute for signal.** Every prose rule is "if you are a
   leaf, don't X" — and the antecedent is exactly what the agent cannot
   observe. Hardening the consequent ("don't" → "NEVER") does nothing about an
   uncertain antecedent: the model still over-applies (muzzles non-leaves) or
   under-applies (the original bug). Louder consequent + fuzzy antecedent =
   **more variance, not less.** Make the antecedent observable (the
   delegate-time line + pruning a stale file that misrepresented it), then the
   consequent can stay calm.
2. **The blast-radius trap — named so we don't do it.** The tempting cheap fix
   is to move `delegationBoundary` into always-on `AgentInstructions`
   (`hooks.go:103`). Don't: that path also feeds *every ordinary top-level
   session*, which legitimately delegates on user request. A leaf rule there
   muzzles ordinary sessions — the exact false-positive radius of conditioning
   on the absence-signal. The signal must live where the leaf is *known*
   (`delegate.go`).
3. **A hard tool-level block is the most "too strong" lever — avoid it.**
   Making `attn delegate` refuse from a delegated session moves policy into the
   tool where it can't see user intent, breaks the legitimate "user steering a
   delegated agent asks for a worker" case, is un-overridable by prose, and
   reintroduces parent-child lineage attn deliberately omits. If anything at
   the tool level, a soft confirm — never a refusal. Not built.
4. **Keep the consequent calm: "don't self-offload," not "never delegate."** A
   delegated agent IS a full steerable session; if its present user asks for a
   visible worker, it should comply. Default to native subagents for *your
   own* subtasks; delegate only on an explicit ask from the user in this
   session. Both `delegated-agent.md` and the delegate-time line say this
   explicitly, not a blanket prohibition.
5. **Prompt bloat dilutes the skill.** Repeating a heavy guard across surfaces
   burns attention budget and trains the model to skim past skill absolutes.
   Landed in three places only: the front-door, the one leaf rule, the
   delegate-time line — not the four-plus surfaces the first draft considered.

## Verification

- `go build ./...`, `go vet ./...` clean.
- `go test ./internal/agent/...` (incl. `TestEnsureAttnClaudeSkillInstalled`,
  the new `TestEnsureAttnClaudeSkillInstalledPrunesOrphanedFiles`,
  `TestEnsureAttnCodexSkillInstalled`, `TestAttnSkillInstallsAreIdentical`) —
  green.
- `go test ./internal/daemon/...` delegation tests (`TestDelegate*`,
  `TestChiefOfStaffDelegate*`, `TestOrdinaryDelegation*`,
  `TestDelegatedFromChief*`) — green.
- `go test ./internal/hooks/...` — green (untouched, confirms no regression).
- `gofmt -l` clean on all edited files.
- Live: `make dev` rebuild + install; confirmed the real installed skill at
  `~/.claude/skills/attn` picked up the new `SKILL.md`/`delegated-agent.md`
  content and the stale `chief-of-staff.md` orphan was pruned.

## Open / Follow-ups

- **Assignment is still blocked.** Taking this ticket needs the prod daemon
  updated (`make install-daemon`) — the running prod daemon predates `ticket
  take` (rejects `ticket_take`/`ticket_list`). Gated on Victor's approval; not
  worked around via a direct DB write.
- `ATTN_DELEGATED` env / `presence` surfacing of `delegated_from_chief` — a
  nice-to-have observable-antecedent path, not built.
- `notebook.md`'s "As Chief Of Staff" coda is a minor, separate IA smell
  (journaling altitude duplicated with `hooks.ChiefGuidance`) — not part of
  this bug's mechanism, left untouched.
- A hard tool-level re-delegation guard was deliberately not built (Decision 3).
