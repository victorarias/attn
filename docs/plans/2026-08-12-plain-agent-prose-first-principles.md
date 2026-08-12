# Plain agent prose: problem statement and dead ends

A step back. Everything here was measured. Nothing here is a proposal.

This exists so someone can attack the problem fresh without repeating four days
of dead ends, and without inheriting the framing that produced them.

The attempt that generated these numbers is
[2026-08-10-prose-density-gate.md](2026-08-10-prose-density-gate.md). Its design
is dead; that file now records the autopsy, in more detail than this one. No
code from it shipped, and none needs reviving.

## The problem

Agents write densely. Jargon, hedging, nouns stacked on nouns, sentences that
carry three ideas. The user then has to stop and ask for it again in plain
words. The goal, as stated:

> automatically make agents easier to understand without user asking

Two parts of that carry weight. **Automatically** — an agent that writes plainly
only when told has not solved anything. **Without user asking** — whatever we
build must not need the user to notice, complain, or configure.

Here is where an agent's prose becomes a lasting record, and where something
could step in before a person reads it:

- `attn ticket new` — the ticket description
- `attn ticket comment` — a comment on a ticket
- `attn delegate` — the brief handed to a delegated agent

Chat messages inside a session are where the complaints actually happen. But
those stream straight to a terminal, so there is no write to intercept.

## What bad and good look like

Both pairs below are real. Each shows a message the user objected to, what they
said, and the revision they accepted. These pairs are the clearest statement of
the target we have.

### Pair 1 — the reply was "plain language please"

Rejected:

> **What landed**
>
> The four background kinds run on `internal/jobs`; `internal/tasks` is deleted
> (~3,500 lines). Each kind's inputs moved from the `Subject`/`Meta` string bag
> to a typed JSON payload, and coalescing moved from the record's identity to an
> explicit `UniqueKey`. The wire shape is unchanged — `protocol.Task.Subject` is
> populated from `UniqueKey` — so no protocol bump and no frontend work.

Accepted:

> **What I did**
>
> All four background jobs the daemon runs (compacting workspace context,
> summarizing sessions, writing journal entries, reconciling tickets) now run on
> the new queue. The old task runner is deleted. Each job carries its inputs as
> proper structured data instead of a bag of strings.
>
> [...] Nothing changed on the app side. Same panel, same data, no version bump.

### Pair 2 — the reply was "can uy explain iun plain langage?"

Rejected:

> ## The diagnosis contradicts your three-way split
>
> It's **options 1 and 2 combined** — and that rules out option 3
> (correct-by-design). The workspace `directory` is not a deliberate "workspace
> home" that merely differs from "the repo to branch from". It's a field with
> *no invariant at all*.

Accepted:

> ## What went wrong
>
> Every workspace in attn stores a "directory" — one folder path. You'd assume it
> means "this workspace's project folder." It doesn't mean anything that
> reliable.
>
> That path gets overwritten constantly. [...] Drag a pane into a new workspace,
> and the new one copies the *old workspace's* folder — not the folder the thing
> you dragged actually lives in.

### What actually changes between them

Worth spelling out, because the obvious reading is wrong:

- **The accepted version is not shorter.** Both revisions here are longer than
  what they replaced. Compression is not the fix, and "be concise" is not the
  instruction.
- **Shorthand becomes things.** "The four background kinds" turns into the four
  jobs, named. A symbol the reader would have to look up gets replaced by what
  it points at.
- **Headings stop being clever.** "The diagnosis contradicts your three-way
  split" becomes "What went wrong."
- **Every fact survives.** File paths, counts, branch names, PR numbers — all
  still there in the accepted versions. Plainness never traded one away.
- **The reader's question gets answered first.** The accepted versions open with
  what happened. The rejected ones open with how the agent was thinking about it.

## What we know about the complaints

Where the numbers come from: every agent message over 120 words that the user
typed a reply to, covering about a month. There were two pulls, hours apart, and
the counts differ, so both are pinned here. The first walked 1,675 transcript
files and kept 1,217 messages. The second walked 2,066 files, kept 1,250, and
added the conversation each message was read in; it reuses the first pull's
labels, matched by file and line. Tables that drop messages under 150 words sit
on a subset of the first pull: 1,042 messages, of which 22 are complaints. The
full pinning, including which table uses which label set, is in the
[companion doc](2026-08-10-prose-density-gate.md#the-trigger-does-not-work).

Only replies a human actually typed count — task
notifications, teammate relays, and compaction summaries all arrive as user
turns too, and they are full of words like "concise" that have nothing to do
with the message before them.

Searching those replies for complaint words flagged 49. **Reading all 49 by hand
left 22.** The other 27 were about design ("a plain rename should be fine"),
speed, or scope. The word "simple" gets used constantly for things that are not
writing. Anyone working from this data must check the labels by hand; the
automatic version is wrong more than half the time.

**The user complains about writing about 22 times a month, out of roughly 1,250
substantial messages.** That rate is the most important number in this document.
It caps every approach that needs a human to label things.

Read the 22 and they are two different problems wearing one coat:

1. **Style** — about a third. "plain language bro", "too verbose", "loaded
   terms". This is a property of the message.
2. **Comprehension** — the majority. They quote one specific passage and say
   they do not understand it. "explain 20s / 5 min, what does that mean." "i
   dont understand what you mean by 'stuffed into the settings map'." This is
   not a property of the message. It is the gap between the message and what
   the reader has already seen.

## What was tried, and what it measured

The tables use AUC. It answers one question: pick a complained-about message and
an accepted one at random — how often does the measurement rank them correctly?
0.5 means no better than a coin. 1.0 means perfect. With only 22 complaints to
test against, anything between 0.5 and 0.6 could be luck.

**Predicting which messages will draw a complaint. Every attempt failed.**

| attempt | result |
| --- | --- |
| Six surface measurements (long words, sentence length, em dashes, prepositions, word length, long sentences) | AUC 0.44-0.56 |
| Limits tuned on the first, smaller collection of pairs | fires on **84.6%** of messages nobody complained about |
| Message shape (length, paragraphs, headings, lists, code, tables, bold) | best 0.60 (word count); structure runs *backwards* — code blocks 0.19, tables 0.21, headings 0.33 |
| Haiku scoring the message alone, 0-10, "will they push back" | AUC 0.534 |
| gpt-5.6-luna, same question | AUC 0.522; scores 96% of all prose at 5 or higher |
| Haiku shown the conversation the reader had seen, then the message | AUC 0.568 |
| How many of the message's words never appeared earlier in the conversation | AUC 0.49-0.50; new backticked identifiers run *backwards* at 0.39 |

Nothing predicts it. The most likely reason is that it is not a property of the
text at all. The same message draws a complaint on a day the reader is trying to
decide something, and passes on a day they are not.

**Why the first attempt fooled us.** We compared complained-about messages
against the revisions that followed them — writing produced right after a
complaint, on best behaviour. Any limit set just above that fires on everything
less careful, which is nearly everything. The 94% success rate was real, but it
answered a different question: is a revision plainer than what it replaced? Yes,
almost always. That is not the same as knowing in advance which messages need
one.

**Improving prose. This part works.** The instruction is attn's `/bro` skill
text plus two clauses about what has to survive:

> Restate your last message. Stop using jargon and speak coherently. State it
> more simply and concisely, like one human talking to another. Keep what
> carries information the prose cannot — diagrams, tables, code, commands,
> links. Improve them if you can; just don't drop them. Keep every number,
> measurement, and file or line reference, and keep stated uncertainty stated —
> a receipt or a caveat is information, not clutter.

Replaying it against the real complaints: it brought word-level density down in
93% of cases, landed where the user's own revisions landed, and beat a plain
"please simplify" by 20 to 30 points.

**Judging which of two versions is plainer. This part works, with care.** Show a
model both versions and ask which one wins — then ask again with the sides
swapped. Against the 17 pairs the user judged personally:

| judge | picks the accepted revision | picks the rejected one | changes its mind when sides swap |
| --- | --- | --- | --- |
| sonnet, effort low | 16/17 | 0 | 1 |
| sonnet, effort high | 11/17 | 0 | 6 |
| gpt-5.6-terra, low | 14/17 | **1** | 2 |
| haiku, any prompt | 3/17 | 4 | 10 |

Two findings hide in that table. **Comparing two versions works where scoring
one does not** — the same models asked to rate a single message 0-10 were coin
flips. And **more thinking makes it worse**, steadily, in both model families.
Given room to reason, the judge talks itself back into rewarding detail and
precision, which is the very thing being complained about.

The prompt mattered more than the model. A first version asking for plainness
*and* completeness in one question scored 47-53%. Splitting it, so the judge
ranks only plainness while a separate check watches for lost information, moved
it to 85-97%.

**The rewrite drops receipts. This is the real defect.** Run it over 100 real
ticket descriptions and comments and numbers, `file.go:73` citations,
arithmetic, session IDs, commit SHAs, and hedges disappear in 37% of rewrites.
Three rewrites dropped the entire ticket body and still won the plainness
comparison. Two changed the meaning — "two 30m windows" became "about an hour".
Adding the receipts clause to the instruction cut losses on chat text from 31%
to 10%, but ticket text, which is thick with identifiers, stayed at 37%.

**Which model should do the rewriting: unfinished.** 40 ticket texts through six
rewriters, each scored by the checks above. Only two rows are valid. The rest
died on a CLI failure while being scored and were never measured.

| rewriter | passes every check | plainer than the original | keeps every fact | p50 |
| --- | --- | --- | --- | --- |
| opus-5 | 20/40 | 40/40 | 20/40 | 16s |
| sonnet-5 | 11/40 | 39/39 | 12/39 | 10s |
| haiku-4.5, luna, terra, sol | not measured | — | — | 14-26s |

The two valid rows agree with everything else here: rewriters are almost always
plainer, and they lose facts about half the time on fact-dense text. Making
prose plain is easy. Keeping the receipts is the hard part.

## Where this left us

The design that survived predicts nothing. Rewrite every gated write, compare
the two versions both ways, keep whichever wins, and let a fact check and a
structure check send it back to the original. Every layer is measured, and it
cannot ship something worse than what the agent wrote. What is wrong with it:

- It spends a model call on every write to improve maybe half of them.
- On ticket text about 60% of writes ship the original anyway, because the
  rewrite dropped an identifier.
- The evidence that its output is genuinely better is thin. Six blind pairs,
  where the user picked the rewrite 6 times out of 6 (p ≈ 0.016) — on chat
  messages, not on ticket text.
- It only touches three CLI surfaces.

That last point deserves its own line. **Every complaint in the data happened
somewhere this design cannot reach.** They all happened in chat. It intercepts
ticket and delegation writes, which nobody has ever complained about.

## Framings nobody has tried

Starting points, not recommendations. None of these has been measured.

- **Fix the writer, not the message.** Everything above steps in after the prose
  exists. The instruction could live in the agent's standing context so the
  first draft comes out plain. Costs nothing. Unknown: whether standing guidance
  survives a long session — which the replay harness could measure directly.
- **Step in when someone reads, not when an agent writes.** attn owns the
  surface where messages get read. A message that does not land could be
  replaced on demand, with one keystroke, instead of every message being
  pre-chewed. That turns an unsolvable prediction problem into a one-click
  request. The cost is that the user has to ask.
- **Aim at comprehension, not density.** Most complaints quote a passage that
  assumed knowledge the reader did not have. Nobody has tried to measure
  understandability head-on — for example, whether a model given only what the
  reader had seen can answer questions about the message.
- **Let the agent judge its own draft before sending.** Same two-way comparison,
  but run inside the agent's turn, where the whole conversation is available,
  instead of at a CLI boundary that only sees the text.
- **Accept that it is unpredictable and make repair cheap instead of
  automatic.** A complaint costs the user four words and produces a good
  revision 93% of the time. The manual loop may already be close to optimal, and
  the honest answer may be to make it faster rather than remove it.
- **Question whether those 22 complaints are the right target at all.** They are
  the moments someone objected out loud. The messages that were read,
  half-understood, and let go are invisible in every measurement here, and we
  know of no way to observe them.

## Constraints any solution must respect

- **Latency lands on the agent, never on the reader.** Twenty seconds before a
  ticket is written is fine. Twenty seconds before a message appears is not.
- **It must fail open.** Any error, timeout, or missing binary ships the text the
  agent already wrote. A prose check that can block a write is worse than any
  dense paragraph.
- **No shadow mode.** Ruled out — attn has one user, and spending a week logging
  to answer a question is a week of waiting. Measuring offline against the
  existing data and the real ticket table replaces it.
- **Idle systems stay idle.** Nothing here may add recurring work. The pipeline
  runs only when someone actually writes.
- **The data stays out of the repo.** It is the user's private transcripts, and
  this repository is public. It lives in a session scratchpad. Only the numbers
  and the short excerpts above are committed.

## Reproducing any of this

The mining and experiment scripts live in the session scratchpad, not in the
repo. Everything they produced is summarized above. The data files are
`corpus_pure.jsonl` (17 hand-checked rejected/accepted pairs),
`silent.jsonl.handlabel` (1,250 messages, 22 hand-checked complaints), and
`tickets.jsonl` (136 real ticket texts, copied read-only from the production
database).

**No prose code is in the repository, on `main` or anywhere else it would be
found by looking.** Two generations exist and neither shipped:

- **Generation 1** — a rule engine with density, hidden-verb, rhythm and
  structure rules, accept/reject vocabulary files, and golden tests — sits
  unmerged on the branch `feat/prose-check`, as `internal/prose` plus
  `cmd/attn/prose.go`. The measurements above killed its design before it was
  reviewed. Close the branch.
- **Generation 2** — the six threshold checks, the rewrite instruction, the
  one-shot refusal, and a structural counter — was never committed. It lived in
  a session scratchpad and is gone. The only parts worth reviving are quoted in
  full in this document: the instruction text above, and the idea of counting
  code blocks, tables and links before and after a rewrite.

So there is nothing here to delete and nothing to build on. Anyone starting
fresh starts from the measurements, not from the code.
