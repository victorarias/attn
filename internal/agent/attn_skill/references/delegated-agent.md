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
so write to it when you reach a meaningful milestone, need input or are blocked,
and when you finish the requested work.

    attn seed note <seed-id> -m \
      "Implemented the parser and tests pass. Next: review the error wording."

Keep a note concrete: outcome, evidence, and next action. A note is a small
payload — put large durable reasoning in an artifact and attach it rather than
inlining it. Leave your successor a `--handoff` note whenever you park or stop
mid-thread. Noting does not stop or transfer your session: continue working
unless the task is blocked or complete. Untracked delegation — no seed in your
prompt — has nowhere to report and needs none of this.

Close it yourself when the outcome is settled:

    attn seed harvest <seed-id> -m "The requested PR merged and no follow-up remains"
    attn seed wither <seed-id> -m "The required API was removed; nobody should pick this up"

## When Your Seed Closes

Dispatched at a plot, the plot is the assignment: run `attn seed ready` and take
the next seed until nothing in the plot is ready. Dispatched at a single seed,
that seed is the assignment: report on it and stop.

`attn seed guide` has the rest of the craft — when the evidence is strong enough
to harvest, where a discovered seed belongs, edit versus replant, and how to
attach the documents your work produced. Run it rather than guessing; it is the
single source of truth for that judgment.

`attn ticket` retired: every write verb prints the garden command that replaced
it and exits nonzero. Only `attn ticket show` and `attn ticket list` still read.
See [garden.md](garden.md).
