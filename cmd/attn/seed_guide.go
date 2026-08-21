package main

import (
	"fmt"
	"io"
	"os"
)

// `attn seed guide` is where the craft lives. The hook-injected garden block
// carries the rules an agent follows without asking; everything that takes
// judgment rather than obedience is here, on demand, so the always-on block
// stays light and there is one place to keep the craft current. The text is
// transplanted from the attn skill's garden and delegated-agent references,
// which now point here.

func runSeedGuide(args []string) {
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "seed guide: takes no arguments\n")
		os.Exit(2)
	}
	writeSeedGuide(os.Stdout)
}

func writeSeedGuide(w io.Writer) {
	fmt.Fprint(w, seedGuideText)
}

const seedGuideText = `The garden holds the work. The syntax is in ` + "`attn seed --help`" + `.

WRITING A BODY

A seed's body is the brief — the literal prompt a delegate receives when it is
dispatched at the seed, and the cold-start spec when somebody picks it up
months later. Write it for a reader with zero warm context.

  Outcome first. State what "done" looks like — the stop condition — not a
  procedure. A title ("Migrate the store to X") is not a stop condition; "X is
  the only backend the daemon talks to, the old path is deleted, tests green"
  is.

  Just-enough context. The paths, the one non-obvious constraint, the why.
  Not a dump.

  A verification contract. How completion is known, and what evidence lands
  as an attachment. This is what makes "ready for review" mean something.

  Scope + autonomy bounds. What is explicitly deferred, and what is a real
  blocker versus a call the tender can make.

  Easy to read, nothing lost. Plain words, short paragraphs, and a sketch
  wherever it says more than the sentences it replaces: see showing.md in the
  attn skill's references.

The body is the stable contract — still true when a different agent tends the
seed tomorrow. The log is the live thread. Don't over-stuff the body trying
to script the whole job; that belongs in notes and steering.

A planted seed nobody is tending is colder than a delegation brief: there is no
live session to ask. Its body has to be *more* self-sufficient, not less.

DELIVERABLE TYPES BEND THE SHAPE

How much to prescribe, what "done" is, and who reviews all change with the kind
of work:

| deliverable | what "done" is | attach | how much to prescribe | who reviews |
|---|---|---|---|---|
| feature / code | behavior exists, tests green, PR up | the plan while it remains active | outcome + constraints, not the implementation | the user (engineering) |
| bug fix | root cause found *then* fixed, regression test | durable diagnosis when needed | symptom + repro only — prescribing the fix invites symptom-patching | the user |
| research | a sourced answer feeding a decision | the findings | frame the *question*, not a task | the answer is the deliverable |
| docs / prose | the durable point made, the old superseded | the doc | audience + what it replaces + the one idea | the chief may review on the merits |
| refactor / migration | transform complete, behavior preserved | before/after and invariants | here you *do* prescribe; list the behaviors that must survive | the user, lighter |
| prototype | a decision or a feel; throwaway | the thing and the learning when durable | the question being de-risked; tests optional | informal |

Evidence decides when to harvest, not the deliverable type. Harvest when the
requested outcome has strong terminal evidence and no review or decision
remains — the user accepted it, the requested PR merged. When implementation is
finished but acceptance or another decision is still pending, say so in a note
and leave the seed open. A separate confirmation ritual is unnecessary when
that evidence already exists.

ARTIFACTS

A document is associated with a seed, never moved into it:

    attn seed attach <id> --path <file.md> [--repo <repository>]
    attn seed attach <id> --notebook <document-id>
    attn seed attach <id> --url <url>
    attn seed detach <id> --path <file.md>

Where the document lives does not change. A committed plan stays canonical in
Git; an untracked staging file belongs in the Notebook. The seed's current
artifacts are every attach that has not been detached, and ` + "`attn seed show`" + `
renders the set.

Edit only the canonical source, and note a meaningful edit, rename, or deletion
on the seed so whoever reads it next knows to re-read the document.

HANDOFFS AND STEERING

` + "`attn seed note <id> -m \"…\" --handoff`" + ` addresses a note to your successor on
this seed: ` + "`show`" + ` renders the freshest one first and ` + "`tend`" + ` prints it on the
claim, so it is read before any work. Leave one whenever you park a seed or
stop mid-thread.

Keep a note concrete: outcome, evidence, and next action. A note is a small
payload — put large durable reasoning in an artifact and attach it rather than
inlining it.

Noting does not stop or transfer your session. Continue working unless the task
is blocked or complete.

To steer whoever is tending a seed right now, message them by seed id:
` + "`attn agent msg <seed-id> -m \"…\"`" + ` delivers to its tender, and an untended seed
refuses by name and points at the log.
`
