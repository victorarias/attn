# Plan: delegation reporting on seeds

The weight transfer named in
[the garden-era epic](2026-08-14-garden-era-epic.md) — "delegation reporting
IS tickets today, so the garden must carry that weight before the ticket
verbs go signpost". This doc records the mechanism choices that outlive the
PR; the design rulings are the epic's, not restated here.

Ticket machinery is untouched and keeps working exactly as today. A
delegation binds both during the transition. Cutover — signposts, backlog
conversion, the app's ticket surfaces — is a later epic step.

## The binding is the dispatch record

`internal/garden`'s `dispatches` collection already keys a session to the
crown it was dispatched at. That record becomes the delegation's seed
binding: one document, keyed by session id, already declared and indexed, so
"which seed does this session report to" is one read from anywhere rather
than a field private to the delegate path. The later agent ledger reads the
same record.

It stays scope inference in the sense the collection's doc means it — it is
not a fence and not an assignment. What is new is that a brief-shaped
delegation now has a crown to be dispatched at, because it plants one.

- `attn delegate --plot <crown>` binds that crown. The dispatch record is
  already written today; nothing is planted and nothing is tended. Reports
  land on the crown's log, which is the plot's plan.
- Any other delegation plants a seed — title is the delegation's name, body
  is the brief — records the dispatch at it, and tends it with the delegate
  session. That is the epic's "the brief is the seed's body, the delegate
  session its tender".

Recovery is idempotent through the same record: a reserved session that
already has a dispatch is re-bound, never re-planted.

An outpost is the one named exception. The garden fence refuses every write
there, so the seed bind is skipped with a log line rather than failing the
delegation — retiring outpost delegations is gated on the uplink per
[the arc plan](2026-08-10-home-garden-crew-arc.md). Every other bind failure
fails the delegation atomically, for the same reason a ticket failure does:
a session nobody can reach is worse than no session.

## Status reports mirror onto the log

The daemon mirrors, not the CLI: `attn ticket status` is unchanged and
learns nothing about seeds. `handleSetTicketStatus` writes a log note on the
reporting session's bound seed after the ticket moves.

Gated on the moved ticket being the reporting session's own. The explicit
`--ticket <id>` form is deliberately permissive — any session may nudge any
ticket for awareness — and a peer nudging a board column is not a status
report about their own work, so it mirrors nothing.

A mirror failure never fails the report. The ticket already moved; losing
the note is worth a log line, not an error handed to an agent that did
nothing wrong.

**The status verbs do not move the seed's lifecycle.** Reporting `completed`
writes a note; it does not harvest. Both plan docs rule this as "status
reports become log notes", and a delegate reporting `completed` with review
still pending is exactly the case where an automatic harvest would close
work nobody accepted. Closing stays a deliberate `attn seed harvest`.

## Artifacts

The wire shape shipped with the reading surface (#928): `SeedNote.artifact`
carrying a typed `SeedArtifactReference`, and the frontend's
`currentSeedArtifacts` projection rendering the attach-minus-detach set in
both the panel drill and the seed tile. This step adds the writer.

- Two note kinds, `attach` and `detach`, beside `note` and `handoff`.
- A reference is valid for exactly one kind and carries only that kind's
  fields: `notebook` a notebook document id, `markdown_file` a path (plus an
  optional repository), `repository` a repository and a path, `url` a URL.
  The daemon never reads meaning out of a string — it validates the typed
  shape and stores it.
- The note body is what a person reads on the log. When a caller writes none,
  the daemon renders one from the typed reference. That is rendering the
  association, not parsing it.
- Storage does not move. The canonical-artifact lifecycle
  ([2026-07-18](2026-07-18-canonical-plan-artifact-lifecycle.md)) keeps
  deciding where documents live; the seed records the association only.

## Steering reaches the tender

`attn agent msg` already delivers to a live session. The gap was addressing:
a caller holding a seed id had to read the tender out of `attn seed show`
first. `agent msg` now accepts a seed id, and the daemon resolves it to that
seed's tender session.

Per the epic's ruling B the wire carries `target_seed_id` as its own typed
field. The CLI decides which field a positional argument fills — a seed id
has a shape `garden.ValidateID` recognizes — so the daemon is handed
authority, never a string to sniff. An untended seed refuses by name and
says who to look for.
