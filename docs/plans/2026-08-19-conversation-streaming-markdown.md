# Streaming markdown in the nisse conversation pane

The pane showed a reply as raw text while it arrived and as raw text once it
landed. This is what it took to draw it as markdown *while* it is being
written, and how to watch and check the result.

## The problem the design is answering

Markdown is not prefix-stable. Half a table is not a table, three backticks
are not a code block until there are six, and a link is a bracket until its
parenthesis closes. Re-parsing the whole document on every delta both costs
the parse and shows the reader syntax that is about to stop being syntax.

Two functions carry it, both in `app/src/components/Markdown/streaming.ts`
with their measurements in the header comment:

- `prepareStreamingMarkdown` completes the open tail — closes a fence, drops
  a half-written link — so a partial document parses as what it is becoming.
  An unterminated mermaid fence is renamed to a pending language so half a
  graph never reaches mermaid, which would draw its parse error where the
  picture goes.
- `splitStreamingMarkdown` cuts the text at the last point that cannot change
  meaning. The settled head is the same string for tens of deltas in a row, so
  React skips that subtree — parser included — and only the tail is re-read.

Cost, measured on the 27,540-char recording: whole-document reparse p50 19.4ms
vs settled/tail split p50 0.54ms, a 35x p50 ratio; on a second machine the same
test read 12ms vs 0.68ms, 17.6x. The ratio is not a contract — it moves with
the box, and happy-dom is not a browser, so the absolute numbers mean less
still. `Markdown.streamingCost.test.tsx` prints both legs on every run, which
is the receipt to read rather than either number here.

Highlighting rides the same split. `VolatileTextContext` is true only inside
the tail document, and shiki is held off there, so it never runs on text that
is still being written.

## Reading chrome is opt-in

The same `Markdown` component renders ticket descriptions, comments and
Present summaries. A transcript wants a document's type scale and a frame
around every block that is not prose; a ticket description wants neither. So
the chrome is opted into per surface — `ReaderPresentation` — and a test pins
that a fence rendered outside it stays bare.

The reading scale lives on `.conversation-pane` rather than in the global type
tokens for the same reason: attn's `--font-size-*` are cut for dense tool
chrome, and a reader holding 2,500 words is a different job. `--ui-scale`
still multiplies through, so scaling the interface scales the column with it.

## Watch it

```bash
make install PROFILE=mdspike        # or any throwaway profile name
env -u ATTN_WS_PORT ATTN_PROFILE=mdspike attn plugin install-bundled attn-pi
env -u ATTN_WS_PORT ATTN_PROFILE=mdspike ATTN_HARNESS_PROFILE=mdspike \
  ATTN_HARNESS_PARK_VISIBLE_PX=0 \
  node app/scripts/real-app-harness/nisse-markdown-demo.mjs
```

A fresh profile ships attn-pi bundled but not installed, and without it
`create_session` refuses with `agent "nisse" is not available`.

It launches the profile's app, opens a nisse conversation, and replays a
recorded reply — 7,845 chars over 317 coalesced deltas in 13.8 s — into the
pane at the pacing it was recorded at. No model is called. The app is left
running: scroll back mid-replay and watch the view hold still, or stay at the
bottom and watch it follow.

- `--recording md-long` replays the 27,540-char reply instead. Use this one to
  watch a mermaid diagram draw: md-tour's only diagram is invalid mermaid as
  the model wrote it (line 5, a bare `[` inside an edge label), so it settles
  into mermaid's parse error. That is the recording, not the renderer.
- `--theme light` relaunches in the light theme first.

## Check it

```bash
env -u ATTN_WS_PORT ATTN_PROFILE=mdspike ATTN_HARNESS_PROFILE=mdspike \
  node app/scripts/real-app-harness/scenario-nisse-markdown-stream.mjs
```

The same replay, asserted: one step per criterion. It writes
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
- `app/src/components/Markdown/index.tsx` — the `streaming` prop, the
  presentation context, and the `<pre>` renderer that frames a fence.
- `app/src/components/Markdown/CodeFrame.tsx` — the frame a code block gets:
  its language and a copy action.
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
