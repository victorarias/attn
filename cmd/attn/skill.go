package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/agent"
)

// runSkill prints the bundled agent skill so any agent or human can read the
// release-matched copy without locating an installed skill directory.
func runSkill() {
	os.Exit(writeSkill(os.Stdout, os.Stderr, os.Args[2:]))
}

func writeSkill(stdout, stderr io.Writer, args []string) int {
	reference := ""
	list := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			printSkillHelp(stdout)
			return 0
		case "--list":
			list = true
		case "--reference":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				fmt.Fprintln(stderr, "skill: --reference requires a name; run `attn skill --list` for the bundled names")
				return 2
			}
			reference = strings.TrimSpace(args[i])
		default:
			fmt.Fprintf(stderr, "skill: unknown argument %q\n", args[i])
			printSkillHelp(stderr)
			return 2
		}
	}

	if list {
		for _, name := range agent.SkillReferenceNames() {
			fmt.Fprintln(stdout, name)
		}
		return 0
	}

	relative := "SKILL.md"
	if reference != "" {
		relative = "references/" + strings.TrimSuffix(reference, ".md") + ".md"
	}
	content, err := agent.SkillFile(relative)
	if err != nil {
		fmt.Fprintf(stderr, "skill: no bundled reference %q; bundled references: %s\n",
			reference, strings.Join(agent.SkillReferenceNames(), ", "))
		return 1
	}
	if _, err := stdout.Write(content); err != nil {
		fmt.Fprintf(stderr, "skill: write output: %v\n", err)
		return 1
	}
	return 0
}

func printSkillHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn skill [--reference <name>] [--list]

print the agent skill bundled with this binary — the instructions attn
installs for supported agents, release-matched to the running version.

  (no flags)            print SKILL.md
  --reference <name>    print one bundled reference, e.g. tickets
  --list                list bundled reference names
`)
}
