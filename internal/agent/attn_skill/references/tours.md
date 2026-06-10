# Code Tours

Use a tour when the user wants a curated, interactive explanation of the
current branch. A useful tour is a reading path through the change, not a
restatement of the diff.

## Build The Guide

Create the guide through attn. This places it under the active profile's system
data directory (`~/.attn/tours` or `~/.attn-dev/tours`), never in the
repository:

```sh
guide="$("$ATTN_WRAPPER_PATH" tour create --name "<short-name>")"
```

Do not create `.attn`, `.jaunt-guide.yml`, ignore rules, or local excludes in
the repository.

Before writing the guide, inspect the complete branch diff against the best
base ref and understand the intent. Then design the tour:

1. Start with a concise summary of why the change exists, its main idea, and
   any architectural shape the reader should hold in mind.
2. Order files by understanding, usually entry point or contract first,
   implementation next, integration and persistence after that, then tests.
   For large changes, divide that path into a small number of semantic
   `chapters` so the reader can see progress and resume without navigating one
   flat file list.
3. Give every included file a note that explains why it appears at that point
   in the tour and what the reader should learn from it.
4. Use annotations sparingly for decisions, invariants, non-obvious mechanics,
   and important control flow. Do not annotate syntax that is self-explanatory.
5. Add `risk` only when a file contains a concrete review hotspot. State the
   failure mode or invariant to verify rather than assigning a vague severity.
6. Put generated, mechanical, or low-value files in `skip`. Leave genuinely
   relevant but untoured changes for the native Other section.
7. Use `view: content` when the whole file is the useful artifact; otherwise
   use `view: diff`.

Guide format:

```yaml
version: 1

summary: |
  Markdown overview. Mermaid diagrams are supported when they clarify flow.

chapters:
  - title: Contract and entry point
    summary: |
      What this chapter establishes before the implementation details.
    files:
      - path: internal/example.go
        view: diff
        note: |
          Why this file matters and what to notice before continuing.
        risk: |
          The compatibility invariant a reviewer should verify here.
        annotations:
          - anchor: "func importantPath("
            note: "Why this decision or invariant matters."
          - start: 40
            end: 55
            thread:
              - author: agent
                body: "Initial explanation."
              - author: reviewer
                body: "Useful prior context."

# `files` remains supported for short tours that do not need chapters.
files: []

skip:
  - app/src/types/generated.ts
```

Each annotation uses exactly one locator: `anchor`, `line`, or `start` plus
`end`. Each annotation uses exactly one content form: `note` or `thread`.
Anchors should be distinctive and stable. Markdown and supported Mermaid
blocks work in tour summaries, chapter summaries, file notes, risk notes, and
annotation comments.

## Start And Stay Attached

Starting a tour always means listening for the user's questions and feedback.
Do not ask whether you should listen, and do not hand off immediately after
opening it.

Run the listener in the background and keep its log:

```sh
tour_log="$(mktemp "${TMPDIR:-/tmp}/attn-tour.XXXXXX")"
"$ATTN_WRAPPER_PATH" tour start \
  --guide "$guide" \
  --name "<human-readable name>" \
  --base "<chosen-base-ref>" \
  >"$tour_log" 2>&1 &
tour_listener_pid=$!
```

Wait for `TOUR_READY`, then continue polling new log lines while the tour is
active. The ready payload contains the `tour_id`.

- `QUESTION_READY` contains an event ID plus file, line, and code context.
  Answer the actual question, then send:

  ```sh
  "$ATTN_WRAPPER_PATH" tour reply \
    --tour "<tour-id>" \
    --event "<event-id>" \
    --body-file "<answer-file>"
  ```

- `FEEDBACK_READY` is review feedback, separate from questions. Apply or
  discuss it as appropriate. When code or the guide changes, refresh the
  native panel with:

  ```sh
  "$ATTN_WRAPPER_PATH" tour refresh --tour "<tour-id>"
  ```

- `TOUR_ENDED` means the user explicitly ended the tour. The listener exits and
  no further polling is required.

Questions must not leak into the final feedback payload. Answer them through
`tour reply`; leave the listener running afterward. If the listener process
dies or disconnects, restart `tour start` with the same guide and session to
reattach to the active tour.

The tour is local to attn. Never submit comments, reviews, or approvals to GitHub.
