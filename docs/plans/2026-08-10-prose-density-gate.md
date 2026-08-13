# Plan: the prose density gate

## Goal

Agent-written prose drifts dense, and Victor has to say so by hand. Twenty-six
times in four weeks he replied "plain language bro" or "simpler terms please" to
a message he had just been handed. The gate fires that nudge for him, before he
reads the draft.

The original two-layer design in this plan — a Vale-shaped rule engine emitting
findings, plus a model judge — was calibrated against a corpus of those real
complaints and did not survive. What replaced it is smaller: a deterministic
trigger and a nudge string. See [Receipts](#receipts).

**That deterministic trigger is also dead.** Measured against every message
Victor actually replied to rather than against the corpus that built it, it fires
on 85% of prose he accepted without comment, and every one of its six features is
a coin flip — as is every cheaper and smarter trigger tried after it. See
[The trigger does not work](#the-trigger-does-not-work). What replaces it
predicts nothing: every gated write is rewritten under the nudge and a pairwise
verdict decides which version ships. See [Design C](#design-c-rewrite-and-verify).

## The trigger does not work

Second corpus, built to answer a question the first one could not: how often does
the gate fire on ordinary writing? Every assistant message over 120 words that
Victor typed a reply to — 1,217 of them across 1,675 transcripts, one month —
labelled by whether that reply complained about the prose. Only genuinely typed
human turns count (`origin.kind == "human"`, `promptSource == "typed"`); task
notifications, teammate relays, and compaction summaries all arrive as user turns
and all contain words like "concise" that have nothing to do with the message
before them. Without that filter the complaint side is ~70% machine text.

**Which numbers came from which pull.** Counts differ across this document and
its successor because there were two pulls and two label sets, so every figure
is pinned here:

| pull | date | files walked | messages | complaint labels |
| --- | --- | --- | --- | --- |
| A | 2026-08-11 | 1,675 | 1,217 | 49 by regex, 22 after hand-reading |
| B — adds the conversation each message was read in | 2026-08-11, later | 2,066 | 1,250 | pull A's labels, re-joined by file and line |

Tables that apply the 150-word floor sit on a subset of pull A: 995 messages
with the original regex labels (43 complaints, 952 accepted), 1,042 with the
hand-verified ones (22 complaints, 1,020 accepted). Two tokenizers were used
across the scratchpad runs, one in Go and one in Python, and they disagree on
two messages sitting exactly at the floor — the same recomputation reported 21
complaints from one and 22 from the other. It never moves a result.

**Every gated feature is a coin flip** (pull A, regex labels, 43 complaints and
952 silently accepted; AUC 0.5 is chance):

| feature | AUC | mean complaint | mean silent |
| --- | --- | --- | --- |
| word_len_mean | 0.503 | 4.84 | 4.83 |
| em_dash | 0.504 | 2.08 | 2.08 |
| long_words_8 | 0.509 | 15.10 | 14.93 |
| preposition | 0.493 | 6.07 | 6.18 |
| sent_mean_words | 0.529 | 19.96 | 18.57 |
| long_sents_per100 | 0.549 | 0.83 | 0.73 |

**The shipped thresholds fire on 84.6% of messages Victor never complained
about.** Recalibrated to the 90th percentile of accepted prose they fire on 38.4%
of silent messages and 37.2% of complaints — equally on both. There is no
operating point that is quiet and useful at the same time.

Shape is no better: raw words 0.589, characters 0.572, paragraphs 0.568,
headings/lists/tables/bold all at or below chance. Nothing cheap predicts it.

**How the first corpus hid this.** Its negative side was the *revisions* — prose
written after a complaint, on best behaviour — not ordinary writing. Thresholds
set just above that fire on anything less careful than Victor's most careful
prose, which is nearly everything he is sent. The 94% paired win rates were real
and answer a different question: *is a revision plainer than the message it
replaced?* Yes, nearly always. That is not *does density predict a complaint?*
This plan wrote "a paired signal is not a threshold" about one feature and then
built the whole trigger on exactly that mistake.

**The labels were then hand-read and it still does not work.** The regex that
labelled complaints matched replies about design ("a plain rename should be
fine", "isnt that too complex"), speed, and file loading; only 22 of 49 were
genuine writing complaints. Recomputed on the hand-verified labels, density is
still chance (word_len_mean 0.44, long_words_8 0.50). What the clean labels
did surface: the genuine complaints split into style complaints ("plain
language bro", maybe a third) and comprehension failures quoting one specific
passage that assumed unseen knowledge (the majority), and messages with
structure draw *fewer* complaints than prose walls — fenced blocks AUC 0.19,
tables 0.21, headings 0.33, all inverted. The weak residual signals are
message length (raw words 0.60, paragraphs 0.60) and conversation depth
(0.59). With 22 positives every AUC carries roughly ±0.12, so nothing here is
confidently above chance.

**Model judges and reader-context features fail too.** Scoring each message
0-10 for "will he push back on how this is written": bare-message Haiku AUC
0.534; gpt-5.6-luna 0.522, rating 96% of everything ≥5 — the known absolute-
scoring failure of LLM judges. Two experiments aimed at the comprehension-
failure species, both on the hand-verified labels: (a) a Haiku judge shown the
user-visible conversation prefix before the message — AUC 0.568; (b)
deterministic novel-referent density, the fraction of the message's terms
absent from the visible conversation before it — AUC 0.49-0.50, with novel
backtick identifiers *inverted* at 0.39 (new code terms draw fewer
complaints). Nothing cheap — feature, judge, or context-aware — predicts the
complaint. The most likely reading: whether Victor pushes back is not a
property of the text; it depends on what he needed at that moment.

**What survives.** The nudge. In replay it moved word-level density down in 93%
of cases, matched Victor's own revisions, beat "please simplify" by 20-30 points,
and never dropped a diagram or table. The half that talks to the agent works; the
half that decides when to talk does not — and after six features, shape,
novelty, and three judge configurations, the evidence says no cheap trigger
exists to build. What ships instead predicts nothing: see the next section.

## Design C: rewrite and verify

Since no signal predicts the complaint, stop predicting. On every gated write
(`ticket new`, `ticket comment`, `delegate`), the pipeline is:

1. **Rewrite** the text under `DefaultNudge` (the agent's own model; opus in
   every measurement here). p50 12s, max 29s.
2. **Verify pairwise**: sonnet, `--effort low`, judges original vs rewrite in
   *both orders*. The rewrite ships only on a double win; a flip or a loss
   ships the original. p50 6s per pair of calls.
3. **Fact gate**: sonnet/low answers "name one concrete thing the ORIGINAL
   states that the REWRITE dropped, or NONE". A flagged loss ships the
   original — fail closed. Any model error anywhere also ships the original —
   the pipeline can only ever fail open to the text the agent already wrote.
4. **Structure gate**: `Structure.Lost` (fenced blocks, tables, links) on the
   rewrite ships the original. Deterministic and free. 0/200 on chat text;
   1/98 on ticket text (a dropped link), which is why it gates rather than
   logs.

The judge prompt must be **single-axis**. The first prompt asked for plainness
and completeness in one question and the judge resolved the tension by counting
detail: 47-53% accuracy against Victor's own accepted revisions. Asking only
"which reads plainer" (information-loss policed separately by step 3) moved it
to 85-97%. Same lesson tightened the fact prompt: demand "NONE or one quote, no
reasoning" or the checker rambles itself into self-refuting verdicts.

**Effort hurts the judge.** Accuracy on the 17 ground-truth pairs falls
monotonically with reasoning effort, in both model families — the task is a
surface judgment, and thinking room lets the judge re-litigate the
completeness bias the prompt removed:

| judge | ships (of 17) | flips | wrong | per-call acc | p50 |
| --- | --- | --- | --- | --- | --- |
| sonnet/low | 16 | 1 | 0 | 97% | 4.8s |
| sonnet/medium | 15 | 2 | 0 | 94% | 4.4s |
| sonnet/high | 11 | 6 | 0 | 82% | 4.4s |
| terra/low | 14 | 2 | **1** | 88% | 6.2s |
| terra/medium | 12 | 5 | 0 | 85% | 11.2s |
| terra/high | 12 | 4 | **1** | 82% | 8.1s |
| haiku (any prompt) | 3 | 10 | 4 | 47% | — |

Haiku is position-biased garbage (27 of 34 verdicts picked whichever side was
labelled B); terra confidently prefers the rejected original once at two
effort levels, which the both-orders rule cannot defend against. Sonnet's
remaining errors are all flips — uncertainty, which ships the original.

**Receipts, all on hand-verified labels and holdout data:**

- *Validity* (17 pairs Victor himself adjudicated): sonnet/low ships his
  accepted revision 16/17, never the rejected side.
- *Safety* (100 silently-accepted messages, no prompt tuned on them): 98%
  of rewrites win the pairwise vote — and a blind test says that is real, not
  judge style-bias: shown 6 unlabelled pairs with sides randomized, Victor
  picked the rewrite 6/6 (p ≈ 0.016), on messages he had accepted in their
  original form.
- *The nudge eats receipts, and the fix works*: the plain nudge dropped
  numbers, file:line citations, arithmetic receipts, and hedges in 31/98
  rewrites (tight checker). Adding "keep every number, measurement, and file
  or line reference, and keep stated uncertainty stated" cut it to 10/97 on
  the same sample — and the residue is phrase-level wording, not receipts,
  with several of the 10 being checker ramble counted conservatively.

**The ticket register needs the gates.** Ran offline over all 99 real ticket
descriptions and comments ≥100 words from production (copied read-only). The
pairwise vote still ships 97% — but the fact gate fires on 36 of those 96
(37%, vs 10% on chat), and the catches are real: three rewrites dropped the
*entire* ticket body and still won the plainness vote; others dropped session
IDs, commit SHAs, file:line references, and measured receipts, and two changed
meaning ("two 30m windows" → "about an hour"). Effective behavior on tickets:
~60/99 rewrites ship, the rest fall back to the original, no loss reaches the
reader. On fact-dense text the single-axis judge is safe only because the
fact gate is behind it — the gate is load-bearing, not belt-and-braces.

**What this costs**: ~20s of agent time and a few cents per gated write, on
surfaces that see a handful of writes a day. The latency lands on the agent
writing the ticket, never on the reader.

**Rollout** (shadow mode considered and rejected — single user, offline
register evidence above replaces it): ship live behind a `prose.pipeline`
setting, `off | on`, default `off`; every run logs original, rewrite, verdicts
and timings under the data dir, which is also the label corpus growing on its
own. Fail open everywhere: missing binary, timeout (90s tripwire; measured
max 74s total on ticket text), or any error ships the agent's original text.

**Never built.** Nothing routes `ticket new`, `ticket comment` or `delegate`
through any of this; the pipeline was only ever run offline against saved text.
Building it would mean writing it from scratch — there is no gate in the
repository to unwire and no threshold code to delete, only the unmerged
generation-1 branch described in
[What was built](#what-was-built-and-where-it-is-not). The one loose measurement
is the blind test: 6 pairs judged, 14 more available if tighter error bars are
wanted.

## Receipts

Corpus: 26 moments mined from four weeks of Claude Code transcripts where Victor
objected to an agent's prose; 17 are pure "write this plainly" complaints. Each
case carries the message he objected to, his complaint verbatim, and the revision
he accepted. Paired within-case, so topic and length cancel out.

**The rule set this plan originally proposed does not separate dense prose from
accepted prose.** Win rate is how often the dense side scored worse:

| proposed rule | win rate |
| --- | --- |
| nominalizations | 59% |
| passive voice | 57% |
| noun strings (3+) | 50% (n=4) |
| expletive openers | 33% — backwards |

Chance is 50%; significance at n=17 needs 76%. Every rule that would have
required a part-of-speech tagger landed in that table, which closes the tagger
question and the CPIDR idea-density port with it.

**What does separate:**

| feature | dense → accepted | win rate |
| --- | --- | --- |
| words ≥8 chars | 13.6 → 9.8 /100w | 94% |
| parenthetical asides | 0.74 → 0.41 /100w | 94% |
| mean sentence length | 18.2 → 14.6 words | 88% |
| "not just X — Y" tic | 0.28 → 0.11 /100w | 80% |
| em-dashes | 2.00 → 1.55 /100w | 76% |
| prepositions | 6.5 → 5.4 /100w | 76% |

**A paired signal is not a threshold.** Parenthetical asides win 94% paired, yet
the two distributions overlap so completely that a tripwire set past accepted
prose fires on 0% of dense documents. Only word-level and length features work as
absolute gates.

**The gate, leave-one-out** (each threshold set 2% above the max of every other
accepted document, so no document sets the bar it is judged by). Regenerate with
`TestRecalibrate`, which runs the package's own `Measure` — the numbers can never
drift from the shipping code. 15 pairs clear the 150-word floor:

```
long_words_8      > 14.40   fires on 5/15 dense,  1/15 accepted
word_len_mean     >  4.77              5/15       0/15
em_dash           >  2.24              4/15       0/15
long_sents/100w   >  0.96              4/15       1/15
sent_mean_words   > 19.53              4/15       0/15
preposition       >  7.65              1/15       1/15

UNION                                 12/15 (80%) 3/15 (20%)
```

**One gate is enough.** Requiring two before firing costs 20 points of recall
(73% → 53% in-sample) and buys nothing: the false-positive rate is already 0% at
one gate. Three drops recall to 20%.

**The gate fires on long reference documents**, which nothing routes through it —
`AGENTS.md`, `docs/glossary.md`, `docs/profiles.md` and `CHANGELOG.md` all trip,
each marginally (16.25 against 14.40; 20.29 against 19.53) where a genuinely dense
message misses by multiples (67.65 against 14.40). Whole-document averages over
thousands of words mix headings, terse lists and prose, so they are a different
distribution from the messages the thresholds were cut for. This is a limit of
`attn prose check` pointed at a document, not of the gate on the write paths.

**The harness, 15 cases replayed against opus.** Each case rebuilt to the message
Victor objected to, answered in his place, and measured:

```
arm         n   judged   still trips   dropped structure   mean words
real       15     15      0 (  0%)      3 ( 20%)            420
generic    15      9      6 ( 67%)      3 ( 20%)            187
mechanism  15     15      9 ( 60%)      0 (  0%)            359
```

Two of those columns are traps and are recorded here so nobody reads them as
wins. `real` scoring zero trips is **circular** — those fifteen documents are the
accepted side that set the thresholds. And six of the generic arm's rewrites fell
under the word floor and abstained, so its trip rate is over nine documents, not
fifteen: shortening is not the same as clarifying, and a scoreboard that counts
abstentions as passes rewards the wrong thing.

The question the `real` arm *can* answer is paired — against the message it
replaced, did the rewrite move each feature down:

```
feature                 real      generic   mechanism
long_words_8           14/15 93%  10/15 67%  14/15 93%
word_len_mean          12/15 80%   9/15 60%  14/15 93%
em_dash                11/15 73%   8/15 53%  12/15 80%
sent_mean_words        13/15 87%  14/15 93%  14/15 93%
long_sents_per100      11/15 73%  12/15 80%  11/15 73%
preposition            11/15 73%  10/15 67%   9/15 60%
```

**The two nudges do different things.** On the length features every arm ties —
"Please simplify" makes text shorter, which is easy. On the word-level features,
the ones the corpus said the complaints are actually about, the mechanism nudge
matches Victor's own revisions (93% on long words) or beats them (93% against
80% on mean word length), and the generic control trails both by 20-30 points.

**The structure clause earns its place.** The generic nudge dropped tables in two
cases and a fenced block plus tables in a third — 3/15, the same rate as Victor's
own revisions. The mechanism nudge dropped nothing in fifteen rewrites. This is
the concern that motivated the clause, and it is the cleanest non-circular result
in the run.

**One nudge does not reliably produce prose that passes.** 60% of mechanism
rewrites still trip. The one-shot design absorbs this — the rewrite lands
regardless — but nobody should expect a single pass to clear the bar, and a
future design that blocks until it does would be wrong.

**Two hypotheses tested and refuted**, recorded so they are not re-proposed:

- *Glossary discipline.* Canonical `docs/glossary.md` term usage: 53% — a coin
  flip. Dense messages use shared vocabulary at the same rate as accepted ones.
  What looked like a 76% signal on "coined vocabulary" turned out to be counting
  branch names and file paths inside backticks.
- *Findings beat a nudge.* Not refuted, but unsupported and now unnecessary: all
  17 of Victor's bare nudges produced a revision he accepted. The nudge is not
  the weak part, so span-level findings have no measured value to add.

## Architecture Map

```text
Current:
agent writes description/comment/brief
  -> cmd/attn: runTicketNew / runTicketComment / runDelegate
    -> internal/client: CreateTicket / CommentTicket
      -> daemon

Target:
agent writes description/comment/brief
  -> cmd/attn: runTicketNew / runTicketComment / runDelegate
    -> internal/prosegate.Check(text)          # pure Go, no model, sub-ms
       |  a refusal is already on record
       |    -> clear it, warn on any structure the rewrite dropped, pass
       |  trips, nothing on record
       |    -> record it, print nudge to stderr, exit 2   # agent rewrites, re-runs
       |  otherwise
       v
    -> internal/client: CreateTicket / CommentTicket
      -> daemon

Also:
attn prose check <file|->        # same Check, --json for agents
```

The gate lives CLI-side. No protocol change, no daemon change, no
`ProtocolVersion` bump: the CLI already owns every write path we are gating.

## Data Model / Interfaces

```go
// internal/prosegate
type Verdict struct {
    Tripped bool
    Gates   []GateHit  // which tripwire, its value, its threshold — for --json and logs
    Nudge   string     // the text handed to the agent; empty when not tripped
}

type GateHit struct {
    Name      string   // "long_words_8"
    Value     float64
    Threshold float64
}

func Check(text string, cfg Config) Verdict

// Prose only. Fenced blocks (covers ```mermaid), tables, and command lines are
// removed before measurement, so structure never trips a gate on its own.
func proseOnly(markdown string) string

// Structure the rewrite must not lose.
type Structure struct {
    FencedBlocks, Tables, Links, ListItems, Headings int
}
func StructureOf(markdown string) Structure
func (before Structure) Lost(after Structure) []string  // fences, tables, links
func (before Structure) Preserved(after Structure) bool
```

`Lost` watches three of the five counts. Headings and list items are counted but
never reported: reorganising them is what the nudge asks for, and a warning that
fires on every honest rewrite is one nobody reads.

Config, one place, defaulting to the `/bro` wording:

```go
type Config struct {
    Nudge      string             // default: the /bro text + "keep what carries
                                  // information the prose cannot — diagrams,
                                  // tables, code, commands, links. Improve them
                                  // if you can; just don't drop them."
    Thresholds map[string]float64 // the calibrated numbers above
    Enabled    bool
}
```

One-shot state, so the gate can never wedge an agent:

```
<data-dir>/prosegate/<session-id>  ->  {digest, structure} of the refused text
```

One refusal per session, not per text. The moment a refusal is on record the
next write lands, whatever it says, and the record is cleared so the gate arms
again for the following message. On that next write the stored structure is
compared against what arrived: anything dropped is named on stderr, and the
write still goes through.

## Boundaries

- `internal/prosegate` owns measurement, thresholds, and the nudge string. Pure
  functions over a string; no store, no client, no daemon.
- `cmd/attn` owns the decision to refuse and the one-shot record. It is the only
  caller that can block a write.
- The daemon owns nothing here. Deliberate: the gate must not become a protocol
  concern before it has earned trust.
- Nothing rewrites text automatically. The agent rewrites; the gate only checks.

## What was built, and where it is not

**None of this is on `main`.** The measurements above are the only thing worth
keeping from it. Nothing below is a task list; it is a record of what the
numbers were produced with. Unticked boxes would imply work still owed, and
none is.

Two generations of prose code exist, both on unmerged branches:

- **Generation 1**, a rule engine — density, hidden-verb, rhythm and structure
  rules, accept/reject vocabulary files, golden tests, corpus fixtures — lives
  on `feat/prose-check` as `internal/prose` plus `cmd/attn/prose.go`. The
  corpus measurements killed its design before it was reviewed.
- **Generation 2**, described below, is preserved on
  `research/prose-gate-generation-2`, together with every mining and experiment
  script under `research/prose/`. That branch must never merge. It is kept
  because the miners rebuild the transcript corpus from `~/.claude/projects` on
  demand (ticket text has its own miner, run against a copy of the database),
  which is what makes the private data disposable, and because the replay
  harness there outlives whatever gets built next. If any of the gate itself is
  ever worth reviving it is the nudge text and the structural counter, both
  reproduced in full in this document.

- `internal/prosegate`: `proseOnly` (fences, tables, command lines),
      tokenizer, the six gates, `Check`, thresholds as named constants carrying
      their receipts.
- `internal/prosegate`: `StructureOf` / `Lost` / `Preserved`, with the
      structural counts above.
- Corpus fixture + calibration test, gated behind `ATTN_PROSE_CORPUS` (the
      corpus is real conversation text and this repo is public). A threshold edit
      that breaks separation fails the test. Bounds are recall ≥70% and false
      positive ≤30%; in-sample reads below the leave-one-out estimate of 80%/20%
      because every accepted document helped set the bar it is judged against.
      `TestRecalibrate` regenerates the numbers through the package's own
      `Measure`, so they can never drift from the shipping code.
- `attn prose check <file|->`, `--json`.
- Wire `ticket new`, `ticket comment`, `delegate` through the gate with the
      one-shot refusal and the dropped-structure warning.
- Harness (`TestNudgeHarness`): replays every case that clears the word floor.
      It rebuilds the conversation up to the message Victor objected to, answers
      in his place, and measures what comes back — three arms, `real` (the
      revision he accepted, free), `generic` ("Please simplify.", the control the
      mechanism has to beat), and `mechanism` (the shipped nudge). Reconstruction
      is bounded, not a full resume: the last few turns of plain text, widened
      until two of them are the user's, so the question being answered is inside
      the window. It costs money and minutes, so it runs only when asked:

      ```
      ATTN_PROSE_CORPUS=…/corpus_pure.jsonl ATTN_PROSE_HARNESS=…/out \
        go test ./internal/prosegate -run TestNudgeHarness -v -timeout 40m
      ```
- A changelog fragment and a `docs/glossary.md` entry for the prose gate were
  drafted alongside the code and went the same way. `docs/glossary.md` on `main`
  has no prose entry, and should not gain one until something ships.

## Decisions

- **Trigger, not findings** (2026-08-11): the corpus shows Victor's bare nudge
  worked 17/17, so the missing piece is firing it, not enriching it. Kills the
  rule engine, the judge, span-anchored output, and the vocabulary injection.
- **No part-of-speech tagger** (2026-08-11): every rule that needed one measured
  at chance. Also kills the CPIDR idea-density port and the `jdkato/prose`
  dependency (+6MB, gonum, unmaintained since 2020).
- **Surprisal stays cut** (2026-08-11), but for a better reason than the original
  plan gave: it is the best-evidenced predictor of reading difficulty in the
  literature, and it still has no repair operator — "this word was unexpected"
  tells an agent nothing to do. Runtime cost was never the real objection.
- **The retry always lands** (2026-08-11): keying the one-shot on the text
  digest looked safe and was not — an agent whose rewrite still trips produces a
  different digest, gets refused again, and trades versions with the gate
  forever. Keying it on the record itself guarantees the write after a refusal
  goes through. Note what it does not guarantee: the gate re-arms after that
  write, so a later message can be refused again. One refusal per message, not
  one per session.
- **Structure is checked, not requested** (Victor, 2026-08-11): a nudge that says
  "be concise" will eventually delete a mermaid diagram. Counting structural
  elements before and after makes preservation a property rather than a hope.
- **Loss is reported, not rejected** (Victor, 2026-08-11): forbidding any edit to
  a diagram or table is too rigid — an agent should be free to improve one. The
  rule is about losing information, not about touching markup, and only the
  author can tell whether a dropped table was deliberate. So the nudge asks, the
  check watches, and the warning names what went missing without blocking.
- **Gate lives in the CLI** (2026-08-11): the write paths are already there, so
  this needs no protocol version bump and no daemon surface.

## Open Questions

- 20% of accepted prose trips the union gate, and 60% of nudged rewrites still
  trip. Both are tolerable while the payload is a nudge that costs one rewrite;
  neither would be tolerable in a design that blocks until the text passes.
- Chat replies inside a session never pass through attn's hands, so the gate
  cannot fire there — that surface stays opt-in via `attn prose check`, which is
  the guidance channel #818 measured drifting. Accepted for now.
- The corpus holds real conversation text and this repo is public. It lives
  outside the tree behind `ATTN_PROSE_CORPUS`, and both the calibration test and
  the harness skip without it — so CI never proves anything about the
  thresholds. A redacted or synthetic fixture would fix that; nothing does today.
- The gate reads a whole reference document as one message and trips on the
  margins (see the receipt above). Nothing routes documents through it, but
  `attn prose check <doc>` will nag. Either the command grows a document mode or
  its help says plainly what it is for.

## Follow-ups

- Promotion from advisory-refusal to anything stricter, per surface, with
  evidence from the harness.
- Garden write verbs, once crown bodies exist — the same wiring, unchanged.
