package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Tickets retired with the garden era. Every `attn ticket` verb that used to
// write is now a signpost: it names where the capability went and exits
// nonzero, so an agent running on stale guidance self-corrects from the error
// instead of silently losing the report it thought it filed.
//
// The signposts stay indefinitely (docs/plans/2026-08-14-garden-era-epic.md).
// Their audience is agents whose memories and skills still say `attn ticket`,
// and a few inert lines of code cost nothing against a lost status report.
//
// `show` and `list` are the deliberate exception and are not routed here: a
// done ticket has no garden equivalent to point at, so it stays readable
// forever rather than answering with a signpost aimed at nothing.

// ticketSignpost is one retired verb and the garden that replaced it.
type ticketSignpost struct {
	// Lead says what the verb used to do, in the caller's terms.
	Lead string
	// Moves are the garden commands that do it now, as label/command pairs.
	Moves [][2]string
	// Note is an optional closing line for a verb whose replacement is not a
	// one-to-one swap.
	Note string
}

// ticketSignposts maps every retired `attn ticket` write verb onto the garden.
// A verb missing from this table would fall through to the router's unknown-command
// path, so the table is the list: `TestEveryTicketWriteVerbSignposts` walks the
// router's own cases against it.
var ticketSignposts = map[string]ticketSignpost{
	"status": {
		Lead: "reporting your work state",
		Moves: [][2]string{
			{"progress", `attn seed note <seed-id> -m "<what happened and what you learned>"`},
			{"blocked", `attn seed note <seed-id> -m "<the decision you need>"`},
			{"finished", `attn seed harvest <seed-id> -m "<what got done>"`},
			{"giving up", `attn seed wither <seed-id> -m "<why nobody should pick this up>"`},
			{"pausing", `attn seed park <seed-id>`},
		},
		Note: "Your seed id is in the brief you launched with; `attn seed ls` lists the garden.",
	},
	"inbox": {
		Lead: "reading unread activity on your work",
		Moves: [][2]string{
			{"the whole log", `attn seed notes <seed-id>`},
			{"the seed itself", `attn seed show <seed-id>`},
		},
		Note: "A seed's log is read, not delivered: there is no cursor to consume.",
	},
	"new": {
		Lead: "creating a backlog item nobody is working on yet",
		Moves: [][2]string{
			{"plant it", `attn seed plant "<title>" -m "<the brief>"`},
			{"a whole plot", `attn seed plot -f <payload.json>`},
		},
	},
	"comment": {
		Lead: "leaving a note on somebody else's work",
		Moves: [][2]string{
			{"note it", `attn seed note <seed-id> -m "<what you want them to know>"`},
		},
	},
	"attach": {
		Lead: "associating a document with your work",
		Moves: [][2]string{
			{"a file", `attn seed attach <seed-id> --path <file> [--repo <repository>]`},
			{"a Notebook document", `attn seed attach <seed-id> --notebook <document-id>`},
			{"anything with a URL", `attn seed attach <seed-id> --url <url>`},
			{"take it back", `attn seed detach <seed-id> --path <file>`},
		},
	},
	"attach-plan": {
		Lead: "handing over a durable plan or design",
		Moves: [][2]string{
			{"a committed plan", `attn seed attach <seed-id> --path <file> --repo <repository>`},
			{"a Notebook document", `attn seed attach <seed-id> --notebook <document-id>`},
		},
		Note: "Where a document lives has not changed; the seed records the association only.",
	},
	"take": {
		Lead: "claiming work as yours",
		Moves: [][2]string{
			{"claim it", `attn seed tend <seed-id>`},
			{"what is free", `attn seed ready`},
		},
		Note: "One tender at a time: tending a seed somebody holds is refused, naming them.",
	},
	"subscribe": {
		Lead: "following somebody else's work",
		Moves: [][2]string{
			{"read the log", `attn seed notes <seed-id>`},
			{"read the seed", `attn seed show <seed-id>`},
		},
		Note: "The garden has no subscription: a seed's log is read when you want it, never pushed.",
	},
	"unsubscribe": {
		Lead: "unfollowing somebody else's work",
		Moves: [][2]string{
			{"read the log", `attn seed notes <seed-id>`},
		},
		Note: "The garden has no subscription to leave; stop reading the log.",
	},
}

// signpostTicketVerb prints the signpost for a retired verb and exits nonzero.
// Exit 2 is the router's own usage-error code: the caller asked for something
// that is not there any more, and the answer says what is.
func signpostTicketVerb(verb string) {
	fprintTicketSignpost(os.Stderr, verb)
	os.Exit(2)
}

// fprintTicketSignpost renders one signpost. It never returns a "not retired"
// answer for an unknown verb — the router only reaches here for verbs the table
// covers, and a missing entry is a defect the test catches, not a runtime case.
func fprintTicketSignpost(w io.Writer, verb string) {
	post, ok := ticketSignposts[verb]
	if !ok {
		fmt.Fprintf(w, "attn ticket %s: tickets retired; work lives in the garden. Run `attn seed --help`.\n", verb)
		return
	}
	fmt.Fprintf(w, "attn ticket %s retired: tickets are gone and %s happens in the garden now.\n\n", verb, post.Lead)
	width := 0
	for _, move := range post.Moves {
		if len(move[0]) > width {
			width = len(move[0])
		}
	}
	for _, move := range post.Moves {
		fmt.Fprintf(w, "  %-*s  %s\n", width, move[0], move[1])
	}
	if post.Note != "" {
		fmt.Fprintf(w, "\n%s\n", post.Note)
	}
	fmt.Fprint(w, "\nDone tickets stay readable: `attn ticket show <id>` and `attn ticket list`.\n")
}

// ticketSignpostVerbs lists the retired verbs in a stable order, for the help
// text that has to name all of them.
func ticketSignpostVerbs() []string {
	verbs := make([]string, 0, len(ticketSignposts))
	for verb := range ticketSignposts {
		verbs = append(verbs, verb)
	}
	sort.Strings(verbs)
	return verbs
}

// ticketSignpostVerbList is the retired verbs as one comma-separated line.
func ticketSignpostVerbList() string {
	return strings.Join(ticketSignpostVerbs(), ", ")
}
