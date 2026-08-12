# Prose gate, generation 2 — preserved, not proposed

**This branch must never merge.** It is a snapshot of work whose design was
measured and rejected. It exists so the measurements can be repeated and the
tooling reused, not so anyone can pick the code back up.

Read the findings first. They are on `main` and they are the point:

- [`docs/plans/2026-08-12-plain-agent-prose-first-principles.md`](../../docs/plans/2026-08-12-plain-agent-prose-first-principles.md)
  — the problem, what bad and good look like, every dead end
- [`docs/plans/2026-08-10-prose-density-gate.md`](../../docs/plans/2026-08-10-prose-density-gate.md)
  — the full receipts

Short version: nothing cheap predicts when a reader will ask for plainer words.
Six surface measurements, message shape, three model judges, and a
context-aware novelty measure all landed at chance. Rewriting and then
comparing the two versions does work.

## Why this branch exists at all

The corpus cannot live in a public repository — it is private transcripts. But
**the miners can, and that is enough**: point them at `~/.claude/projects` and
the corpus rebuilds in a couple of minutes. Keeping the tools makes the data
disposable.

The one thing here that is worth reusing regardless of what happens to the gate
is the **replay harness** (`internal/prosegate/harness_test.go`). It rebuilds
the conversation up to a message the user objected to, answers in their place
with whatever intervention is being tested, and measures what the agent sends
back. Every real answer in those plan docs came out of it, including the one
that killed the design. The plan doc's untried framings — "does standing
guidance survive a long session?" — are questions it can answer directly.

## What is here

### Go, generation 2 (dead design)

| path | what it is |
| --- | --- |
| `internal/prosegate/prosegate.go` | the rewrite instruction, the tokenizer, and six threshold checks. **The thresholds are dead** — they fire on 84.6% of prose nobody complained about. The instruction text is the part that works. |
| `internal/prosegate/structure.go` | counts code blocks, tables and links before and after a rewrite. Still a good idea; caught a dropped link the model judges missed. |
| `internal/prosegate/harness_test.go` | the replay harness described above. Env-gated: `ATTN_PROSE_CORPUS`, `ATTN_PROSE_HARNESS`. |
| `internal/prosegate/calibration_test.go`, `recalibrate_test.go` | threshold calibration against the corpus. Preserved to show what was tried; the thing they calibrate does not work. |
| `internal/prosegate/zz_scratch*_test.go` | the measurements themselves — false-positive rates, AUC separation per feature, message-shape features. `zz_scratch2_test.go` carries the rank-sum AUC used everywhere. |
| `internal/prosegate/zz_judge_test.go` | the model-judge experiment (Haiku and gpt-5.6-luna scoring messages 0-10). |
| `internal/prosegate/zz_structcheck_test.go` | filter mode: reads pairs on stdin, applies the real `Structure.Lost`. The Python experiments call this so they score with shipping code rather than a reimplementation. |
| `cmd/attn/prose.go`, `prose_gate.go` | the CLI surface and the one-shot refusal, wired into `ticket new`, `ticket comment` and `delegate` in `main.go`. Never reviewed, never merged. |

Generation 1 — a rule engine with vocabulary files and golden tests — is a
different unmerged branch, `feat/prose-check` (`internal/prose` plus its own
`cmd/attn/prose.go`). It was killed by the same measurements. Both branches
should be closed once nobody wants to re-read them.

### Miners (Go, `miners/`)

Each reads Claude Code transcripts and writes JSONL. Run with the transcript
root as the only argument: `go run ./miners/silent ~/.claude/projects`.

| miner | output |
| --- | --- |
| `extract` | the paired corpus: a message the user objected to, the objection, and the revision they accepted |
| `silent` | every message over 120 words that got a typed human reply, labelled complaint or accepted |
| `silentctx` | the same, plus the conversation the reader had seen before each message |
| `features`, `glossary` | one-off feature dumps kept for provenance |

**The complaint labels these produce are regex labels and they are wrong more
than half the time** — 49 flagged, 22 genuine. Words like "simple" get used
constantly for things that are not writing. Every published number came from
hand-read labels. Do not skip that step.

Only replies a human actually typed are counted (`origin.kind == "human"`,
`promptSource == "typed"`). Without that filter roughly 70% of the complaint
side is task notifications and relayed machine text.

### Experiments (Python)

Plain scripts, no dependencies beyond the standard library; they shell out to
the `claude` and `codex` CLIs. Run from a directory holding the mined JSONL.

| script | question it answered |
| --- | --- |
| `expa.py` | does a judge that sees what the reader saw predict complaints? (AUC 0.568 — no) |
| `expb.py` | does leaning on terms the reader never saw predict them? (AUC 0.49 — no) |
| `expc.py`, `expc2.py`, `expc_matrix.py` | can a pairwise judge pick the accepted revision? (yes: 16/17, and effort *hurts*) |
| `exp2.py`, `exp3.py` | does rewriting damage messages that were already fine? (no — but it dropped receipts until the instruction was fixed) |
| `exp4.py` | the same against real ticket text (37% lose a receipt — the open defect) |
| `exp5.py`, `exp5_report.py` | which model should rewrite? (unfinished: four of six rows died on a CLI failure during scoring) |
| `blindtest_gen.py` | builds the unlabelled A/B page for a human verdict |
| `refact.py` | re-scores an old run with a newer checker prompt |

Two traps worth knowing before rerunning any of them. A judge asked to weigh
plainness *and* completeness in one question scores 47-53%; ask only about
plainness and check information loss separately and it scores 85-97%. And more
reasoning effort makes the judge steadily worse, in both model families — set
it low.
