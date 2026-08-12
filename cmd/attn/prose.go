package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/prosegate"
)

func writeProseHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn prose check <file|-> [--json]

Reports whether a piece of prose should be rewritten plainly before anyone
reads it. Exits 0 when the prose passes or is too short to judge, 1 when it
should be rewritten.

Judges one message. Pointed at a long reference document it averages headings,
lists and prose together and will trip on the margins — that says nothing about
the document.

  --json   print the verdict, the gates that fired, and the nudge as JSON
`)
}

func runProse() {
	if len(os.Args) < 3 {
		writeProseHelp(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[2] {
	case "check":
		if hasHelpFlag(os.Args[3:]) {
			writeProseHelp(os.Stdout)
			return
		}
		runProseCheck(os.Args[3:])
	default:
		fmt.Fprintf(os.Stderr, "prose: unknown command %q\n", os.Args[2])
		writeProseHelp(os.Stderr)
		os.Exit(2)
	}
}

func runProseCheck(args []string) {
	fs := flag.NewFlagSet("prose check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "print the verdict as JSON")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "prose check: %v\n", err)
		writeProseHelp(os.Stderr)
		os.Exit(2)
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "prose check: expected exactly one file argument, or - for stdin")
		writeProseHelp(os.Stderr)
		os.Exit(2)
	}

	var (
		text []byte
		err  error
	)
	if rest[0] == "-" {
		text, err = io.ReadAll(os.Stdin)
	} else {
		text, err = os.ReadFile(rest[0])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "prose check: %v\n", err)
		os.Exit(2)
	}

	verdict := prosegate.Check(string(text), prosegate.Default())
	if *jsonOutput {
		printJSON(verdict)
	} else {
		fmt.Print(formatVerdict(verdict))
	}
	if verdict.Tripped {
		os.Exit(1)
	}
}

// formatVerdict names the limit, its value, and the ask, so an agent reading
// stderr can tell whether the gate fired and why.
func formatVerdict(v prosegate.Verdict) string {
	if v.Abstained {
		return fmt.Sprintf("too short to judge (%d words, floor %d)\n", v.Words, prosegate.MinWords)
	}
	if !v.Tripped {
		return fmt.Sprintf("reads plainly (%d words)\n", v.Words)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", v.Nudge)
	fmt.Fprintf(&b, "(%d words; ", v.Words)
	parts := make([]string, 0, len(v.Gates))
	for _, g := range v.Gates {
		parts = append(parts, fmt.Sprintf("%s %.2f over %.2f", g.Name, g.Value, g.Threshold))
	}
	fmt.Fprintf(&b, "%s)\n", strings.Join(parts, ", "))
	return b.String()
}
