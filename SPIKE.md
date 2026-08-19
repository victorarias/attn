# Spike: streaming markdown in the nisse conversation pane

Throwaway branch. Not for merge. The findings live in the ticket; this is only
how to run what is here.

## Watch it

```bash
make install PROFILE=mdspike        # or any throwaway profile name
env -u ATTN_WS_PORT ATTN_PROFILE=mdspike ATTN_HARNESS_PROFILE=mdspike \
  ATTN_HARNESS_PARK_VISIBLE_PX=0 \
  node app/scripts/real-app-harness/nisse-markdown-demo.mjs
```

It launches the profile's app, opens a nisse conversation, and replays a
recorded reply — 7,845 chars over 317 coalesced deltas in 13.8 s — into the
pane at the pacing it was recorded at. No model is called. The app is left
running: scroll back mid-replay and watch the view hold still, or stay at the
bottom and watch it follow.

- `--recording md-long` replays the 27,540-char reply instead.
- `--theme light` relaunches in the light theme first.

## Check it

```bash
env -u ATTN_WS_PORT ATTN_PROFILE=mdspike ATTN_HARNESS_PROFILE=mdspike \
  node app/scripts/real-app-harness/scenario-nisse-markdown-stream.mjs
```

The same replay, asserted: one step per criterion the spike was set. It writes
`follow-series.json`, `frame-budget.json`, `settled-message.html` and
screenshots into its artifacts dir.

```bash
pnpm --dir app test -- --run src/components/ConversationPane src/components/Markdown
```

The offline half: both recordings replayed through the real pane in happy-dom,
including the streamed-DOM-equals-settled-DOM check and the cost comparison
that picked the settled/tail split.

## Where the code is

- `app/src/components/Markdown/streaming.ts` — tail completion and the
  settled/tail split, with the measurements in its header comment.
- `app/src/components/Markdown/index.tsx` — the `streaming` prop.
- `app/src/components/Markdown/MarkdownBoundary.tsx` — a render failure falls
  back to the raw text rather than blanking the transcript.
- `app/src/components/ConversationPane/index.tsx` — the pane renders markdown,
  and follow mode is decided by the reader's own scrolling.
- `app/src/components/ConversationPane/__recordings__/` — the two recordings,
  captured off a real host's fd 3.

## Clean up

```bash
attn profile clean mdspike
```
