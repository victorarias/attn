# Delegated-Agent Guidance

Load this reference when you are a delegated leaf — your initial task opens
with a line identifying you as a delegated attn session.

## You Are A Leaf, Not A Coordinator

Do the assigned work in this session. A subagent is always a native runtime
subagent, including when the user says to delegate or dispatch subagents. An
explicit request from the user steering this session selects attn delegation;
otherwise, use native subagents.

An attn delegation creates a visible agent session the user can inspect,
converse with, and steer directly. Native subagents report to you.

## Report On Your Seed

Your work is a **seed** in the garden — the brief you launched with is its body,
and you are its tender. Its id is in your launch prompt. The log is the only
channel back to the session that delegated you and to whoever else is watching,
so write to it when you:

- reach a meaningful milestone
- need input or are blocked
- finish the requested work

    attn seed note <seed-id> -m \
      "Implemented the parser and tests pass. Next: review the error wording."

    attn seed note <seed-id> -m \
      "Core implementation is ready locally; which event contract should be used?"

Close it yourself when the outcome is settled:

    attn seed harvest <seed-id> -m "The requested PR merged and no follow-up remains"
    attn seed wither <seed-id> -m "The required API was removed; nobody should pick this up"

Harvest when strong terminal evidence shows the requested outcome is done and no
review or decision remains — Victor accepted the work, the requested PR merged,
or an equivalent objective completion signal is clear. A separate confirmation
ritual is unnecessary when that evidence already exists. If you merely finished
implementing and acceptance, review, or another decision is still pending, say
that in a note and leave the seed open.

Keep a note concrete: outcome, evidence, and next action. A note is a small
payload — put large durable reasoning in an artifact and attach it rather than
inlining it. Leave your successor a `--handoff` note whenever you park or stop
mid-thread.

Noting does not stop or transfer your session. Continue working unless the task
is blocked or complete. Untracked delegation — no seed in your prompt — has
nowhere to report and needs none of this.

`attn ticket` retired: every write verb prints the garden command that replaced
it and exits nonzero. Only `attn ticket show` and `attn ticket list` still read.
See [garden.md](garden.md).

## Hand Over Durable Artifacts

When your work produces a Markdown plan or design that must outlive this
session, associate it with your seed:

    attn seed attach <seed-id> --path docs/plans/design.md --repo attn
    attn seed attach <seed-id> --notebook <document-id>
    attn seed attach <seed-id> --url <url>

Where the document lives does not change — the seed records the association
only, and `attn seed detach` takes it back.

- If the scope keeps plans or designs in Git, commit the plan first and attach
  it by `--path` with the `--repo` it lives in. The repository file stays
  canonical.
- Otherwise write it into the Notebook (see [notebook.md](notebook.md)) and
  attach it by `--notebook <document-id>`.

Edit only the canonical source afterward. When you make a meaningful edit,
rename, or deletion, note it on the seed so whoever reads it next knows to
re-read the document.
