package main

import (
	"fmt"
	"io"
	"os"
)

// `attn seed guide` is where the craft lives. The hook-injected garden block
// carries the rules an agent follows without asking; everything that takes
// judgment rather than obedience is here, on demand, so the always-on block
// stays the weight of a rule sheet and there is one place to keep the craft
// current.

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

  A body is the brief a delegate is launched with and the cold-start spec
  somebody reads now or months from now. Write it for a reader with no warm
  context.

  Outcome first. Say what "done" looks like. "Migrate the store to X" is a
  title; "X is the only backend the daemon talks to, the old path is deleted,
  tests green" is a stop condition.

  Just-enough context: the paths, the one non-obvious constraint, the why.

  A verification contract: how completion is known, and what evidence lands as
  an attachment. This is what makes "ready for review" mean something.

  Scope and autonomy bounds: what is deferred or out of scope, and where the
  tender's authority ends — when to stop and ask versus when to make the call
  and keep going. A body that says nothing here defaults to checking in.

  A body may say "ship till done": that is an instruction to work end to end
  on your own, following the stop conditions and requirements until they are
  met — not to stop and check in along the way.

  When you plant something you discovered, say what it fell out of — the work
  you were doing and how you hit it. A discovered seed with no origin reads as
  noise to whoever finds it next.

  The body is the stable contract; the log is the live thread. Don't script the
  whole job in the body — that belongs in notes and steering.

DELIVERABLE TYPES BEND THE SHAPE

  How much to prescribe, what "done" is, and who reviews all move with the kind
  of work.

  feature / code    done: behavior exists, tests green, PR up. Attach the plan
                    while it is active. Prescribe outcome and constraints, not
                    the implementation.
  bug fix           done: root cause found, then fixed, with a regression test.
                    Give symptom and repro only — prescribing the fix invites
                    symptom-patching.
  research          done: a sourced answer feeding a decision. Frame the
                    question, not a task. The answer is the deliverable.
  docs / prose      done: the durable point made, the old superseded. Say the
                    audience, what it replaces, and the one idea.
  refactor          done: transform complete, behavior preserved. Here you do
                    prescribe — list the behaviors that must survive.
  prototype         done: a decision or a feel. Name the question being
                    de-risked; it is throwaway and tests are optional.

  Evidence decides when to harvest, not the type. Harvest when the requested
  outcome has strong terminal evidence and no review or decision remains. When
  implementation is finished but acceptance is still pending, say so in a note
  and leave the seed open.

WHERE A SEED BELONGS

  Under the plot you are working in, when it is part of that plan and someone
  reading the crown would expect to see it: plant it with ` + "`--part-of <crown>`" + `.

  Freestanding, when it is real work that simply is not this plan. Say what it
  fell out of and leave it in the garden.

  Not a seed at all, when it is one fact about work already planted — that goes
  on the log of the seed it belongs to.

EDIT, REPLANT, OR PLANT AGAIN

  Edit the body when the same work got sharper: the outcome moved, a constraint
  surfaced, scope narrowed. ` + "`attn seed edit`" + ` leaves the state and the claim
  alone.

  Plant a new seed when the work is different work. A body rewritten into
  something its log no longer describes loses the thread.

  Replant when a closed seed turns out unfinished. ` + "`attn seed replant`" + ` reopens
  it, and a closed seed reopens before it moves again.

A SEED WHOSE TENDER IS GONE

  A claim held by a session that is no longer running does not hold the seed:
  it comes back as ready and tending it succeeds. Read the log and the freshest
  handoff before you touch the work — the previous tender may have left the
  branch, the PR, or the failure half-done.

PICKING UP FURTHER WORK

  Dispatched at a plot: the plot is the assignment. When your seed closes, run
  ` + "`attn seed ready`" + ` and take the next one, until nothing in the plot is ready.

  Dispatched at a single seed: that seed is the assignment. Report on it and
  stop. Work you found along the way is planted, not picked up.

ARTIFACTS

  A document is associated with a seed, never moved into it:

    attn seed attach <id> --path <file.md> [--repo <repository>]
    attn seed attach <id> --notebook <document-id>
    attn seed attach <id> --url <url>
    attn seed detach <id> --path <file.md>

  Where the document lives does not change. A committed plan stays canonical in
  Git; an untracked staging file belongs in the Notebook. Edit only the
  canonical source, and note a meaningful edit, rename, or deletion on the seed
  so whoever reads it next knows to re-read it.

HANDOFFS AND STEERING

  ` + "`attn seed note <id> -m \"…\" --handoff`" + ` addresses a note to your successor on
  this seed: ` + "`show`" + ` renders the freshest one first and ` + "`tend`" + ` prints it on the
  claim, so it is read before any work. Leave one whenever you park a seed or
  stop mid-thread.

  To steer whoever is tending a seed right now, ` + "`attn agent msg <seed-id> -m \"…\"`" + `
  delivers to its tender; an untended seed refuses by name and points at the
  log.

TICKETS RETIRED

  Every ` + "`attn ticket`" + ` write verb names the garden command that replaced it and
  exits nonzero. ` + "`attn ticket show`" + ` and ` + "`attn ticket list`" + ` still read the
  archived board.
`
