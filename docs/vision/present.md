# Vision: Present — the agent's document channel

> Supersedes [docs/plans/2026-07-02-pr-tour-port.md](../plans/2026-07-02-pr-tour-port.md)
> (the jaunt port direction plan). That plan overlaid a tour on the existing diff
> panel; this vision replaces the panel and broadens the primitive. jaunt
> (`victorarias/jaunt`) and plannotator (`backnotprop/plannotator`) are the two
> donor products — jaunt for the pedagogy, plannotator for the annotation
> vocabulary — but Present is neither's port.

## End state (the why)

An agent finishes a change, or reaches a plan worth vetting, or uncovers
something worth explaining — and **presents** it. A quiet notice lands in attn's
top banner. One click opens the presentation in its own window: throw it on the
second display while agents keep running in the main one. The document is laid
out the way its author would walk you through it — summary first, reading order
chosen, every consequential line carrying the *why* that the content alone can't
show — rendered richly: prose, highlighted code, diagrams, tables. You read
keyboard-first. Where you push back, you annotate — the feedback pins to the
exact text or line, quoted, structured. You hand the round back; the agent gets
its doorbell, reads your notes through the CLI, revises, presents round two —
and round two opens on *what changed since round one*.

The same surface reviews a branch diff, gates a plan before the agent builds,
walks you through an unfamiliar subsystem, or teaches you something the agent
learned. Presenting is the agent's **document channel** — the terminal stays the
conversation channel, and walls of terminal text stop being how agents show
work.

Why it matters: attn's whole model is many agents, one Victor. Reading and
judging agent output is the bottleneck — and the trust ceiling. A
cognition-friendly, pedagogic presentation surface raises both. The automated
review-loop failed because it removed the human; Present succeeds by centering
him.

## North-star principles

- **Human-centric, always.** Agents author and present; the human reads and
  decides. No headless review loops, ever — that is the review-loop's grave, and
  we don't dig it up.
- **The presenter carries the frame.** Every presentation states its content
  explicitly — repo, base, head, doc paths. Nothing is inferred from "the active
  session"; the frame ambiguity that broke the old diff panel cannot exist here.
- **Pedagogy over inventory.** Teach, don't list. Reading order, pinned whys,
  contested decisions pre-empted (jaunt's voice). Multi-modal by default —
  prose, code, diagrams, structure. Cognition-friendly UI/UX is a non-negotiable,
  not polish.
- **Zero friction.** Banner → reading in under a second. Keyboard-first
  traversal. Progress durable. No port dances, no browser tabs, no blocked
  agent processes.
- **Rounds are the unit of trust.** A presentation advances in rounds; each
  round pins its content (SHAs for diffs, content hash for docs). Drift is
  visible — "the branch is N commits ahead of this round" — never silent.
  What-changed-since-last-round is first-class.
- **Feedback is structured and quoted.** Annotations pin to content and quote
  it, so the agent never resolves anchors. They flow back as data through the
  CLI; the doorbell says *go look* — content is never streamed into a PTY.
- **Guidance and response scale independently.** Guidance: just show it →
  reading order → notes → full annotated tour. Response: FYI → annotations →
  gate (approve/deny) → iterating rounds. Every combination is legitimate;
  every level is useful; cost scales with stakes.
- **One spine, many uses.** Change review, plan gating, explainers, teaching —
  one instance model, one window, one feedback path. A new use is a new
  manifest shape, not a new subsystem.

## Scope & non-goals

**In scope:** the presentation instance (agent-triggered, several per session,
bound to session/ticket); the manifest (evolved-from-jaunt YAML — content refs,
guidance, response mode); the banner → own-window surface (minimize back to
banner); readers for changes (diff) and documents (rendered markdown);
annotation and comment machinery with structured vocabulary; rounds with
pinning and drift; the agent CLI + doorbell loop; authoring skills (jaunt's
discipline, ported); a shared markdown renderer that also pays the ticket-board
rendering debt; pull ("give me a tour of this"), gates, and teaching (`/teach`)
as the surface matures; eventual removal of the ⌘⇧E diff panel, which this
replaces.

**Non-goals:** posting to GitHub/GitLab (feedback belongs to the agent loop);
chat inside the presentation (conversation stays in the session terminal);
headless agent-to-agent review; jaunt web-app compatibility for the manifest;
mobile/remote rendering of the presentation window in v1.

## Big rocks (the arc)

- [ ] **Spine** — presentation instance + rounds domain model, manifest schema,
      protocol, store, `attn present` CLI, doorbell semantics. Retires the
      `(repo_path, branch)` review identity.
- [ ] **Window shell** — banner notice → dedicated OS window, minimize back;
      keyboard-first player chrome. (Tauri multi-window: ground pass first.)
- [ ] **Change reader** — guided diff at levels 0–1 (explicit frame + reading
      order + skip), replaces ⌘⇧E for daily review; inline comments; handback.
- [ ] **Full tour guidance** — per-file notes, line-pinned annotations and
      threads, summary card; authoring skill ported from jaunt.
- [ ] **Document reader** — rendered markdown presentation + annotation
      (select/pinpoint, quoted anchors — plannotator parity where it earns it).
- [ ] **Rounds & drift** — pinned SHAs/hashes, drift banner,
      changes-since-round view.
- [ ] **Rendering palette** — shared markdown renderer (ticket board pays off
      its debt here), syntax highlighting, mermaid, callouts; more as needed.
- [ ] **Feedback vocabulary** — quick labels with agent-facing tips,
      delete-this, looks-good; suggested-code later.
- [ ] **Gates** — presentations that block: plan-mode interception, rounds that
      require approval.
- [ ] **Pull** — request a presentation of any diff/doc on demand,
      reviewer-side generated.
- [ ] **Teaching** — `/teach`: agent-authored lessons on the same surface.
- [ ] **HTML artifacts** — sandboxed rendering + annotation; last, and may
      dissolve if markdown + diagrams cover the need.
- [ ] **Demolition** — remove the old diff panel and its review UX once Present
      covers daily review.

## Open questions

Known unknowns:

- Manifest schema: how far it drifts from jaunt v1, and how levels/modes are
  expressed without schema sprawl.
- Fate of existing `ReviewComment` rows and viewed-marks data — migrate into
  presentation instances or start clean.
- Gate ↔ `pending_approval` interplay: is a gated presentation a session state,
  a ticket state, or its own thing?
- How the board/chief surface presentations (a ticket's rounds, "waiting on
  your review" visibility) without the board gating anything.
- Where plannotator remains in the toolkit (non-attn contexts, external repos).

Blindspots — run a `ground` blindspot pass before their first chunk:

- **Tauri multi-window**: second WebviewWindow lifecycle, WS client sharing vs
  separate connection, state sync, shortcut routing, menu accelerators.
- **Markdown annotation anchoring** inside attn's own renderer (plannotator's
  dual-anchor approach is documented prior art, but attn renders its own DOM).
- **Teaching mode**: what a lesson artifact is, whether lessons persist, how
  `/teach` fits the notebook/knowledge-base world.
