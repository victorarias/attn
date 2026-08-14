# Plan: Consolidate annotation shared seams

## Goal

Share exactly five mechanics between terminal and markdown annotations without changing user-visible behavior, anchoring, rendering, payload formatting, clear ownership, or the two draft models beyond terminal label storage. The Task 3 amendment permits only the label field's protocol shape and version bump.

## Architecture Map

```text
Daemon draft commands
  session adapter  \
                    -> annotationDraftHandler -> generic draft store accessors
  markdown adapter /

Frontend annotation flows
  daemon socket -> shared pending-request correlation -> domain event decoder
  composer      -> shared QuickLabelPicker
                -> shared useAnnotationSend -> existing domain send function
```

Persisted terminal draft entries change only from `emoji` to `quickLabelId`; the session draft decoder translates old emoji-only entries while all new writes use IDs. Protocol 246 makes the new field optional beside the optional legacy field.

## Boundaries

- Daemon adapters own typed protocol result construction; the shared handler owns trim, validation, JSON encoding, stale-save mapping, storage, and reply sequencing.
- `daemonPendingRequests.ts` owns plain and keyed correlation; markdown event decoding only interprets result events.
- Each annotation stack keeps its anchor, highlight renderer, submit payload formatter, clear-on-send owner, and domain-specific draft fields.
- The shared picker accepts styling from its caller so both composers keep their current appearance.
- The shared send hook owns re-entrancy, outcome state, sent expiry, and shortcut registration; callers keep domain eligibility and messages.

## Implementation Steps

- [x] Task 1: validate and finish the shared daemon draft handler, shared submit statuses, and byte-level adapter tests.
- [x] Task 2: move markdown last-writer-wins correlation into `daemonPendingRequests.ts`.
- [x] Task 3: store terminal labels by `quickLabelId` and decode persisted emoji-only entries.
- [x] Task 4: move and reuse `QuickLabelPicker` with caller-provided styling.
- [x] Task 5: extract `useAnnotationSend` and cover states plus re-entrancy.
- [x] Add the internal annotations changelog fragment.
- [x] Run daemon/store tests, frontend tests, and `make build-app`.
- [x] Install and preflight a named profile; verify and record both send flows; publish evidence; clean the profile.
- [ ] Rebase onto main, open a ready PR, address figgyster/CI, merge, and update the ticket.

## Decisions

- Preserve request/result JSON bytes in Task 1 tests; this catches omitted fields and zero-value serialization changes that typed assertions can miss.
- Keep the old terminal label compatibility at the wire decoder only. No migration or write fallback is added.
- Keep each composer’s sent-expiry duration as a hook argument rather than normalizing behavior.
- The Task 3 amendment authorizes optional `quick_label_id`, optional legacy `emoji`, regenerated bindings, and one protocol-version bump.
