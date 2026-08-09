// Command activity-bench is the experiment loop for session activity lines.
//
// Prompt quality is the whole product for this feature and it cannot be
// unit-tested. This harness freezes real transcript windows as a corpus, runs a
// matrix of prompt x agent x model x effort against it, and reports cost,
// latency, and what each variant actually wrote.
//
// It runs the real code path — internal/transcript for the window,
// internal/activity for the rendering and checks, internal/agent's
// HeadlessTaskProvider for the run — so what is benchmarked is what ships.
//
//	activity-bench corpus    # freeze windows from live sessions
//	activity-bench run       # matrix over prompts x models x efforts
//	activity-bench report    # comparison table + side-by-side lines
//
// Design: docs/plans/2026-08-07-session-activity.md
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "corpus":
		err = runCorpus(os.Args[2:])
	case "run":
		err = runMatrix(os.Args[2:])
	case "report":
		err = runReport(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "activity-bench: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `activity-bench — experiment loop for session activity lines

  corpus   sample windows from live sessions into the corpus (append + dedupe)
  run      run prompts x models x efforts over the corpus
  report   compare runs

Corpus entries are captured live, one pass per invocation, so rare states
(pending_approval, recoverable) accumulate by running it over time rather than
by fabricating them.

Data lives under `+"`--dir`"+` (default `+"`.activity-bench/`"+`, gitignored): real
windows carry source code and conversations and are not committed.
`)
}

// defaultDir is where corpus and results live. Gitignored on purpose — these are
// real transcript excerpts.
const defaultDir = ".activity-bench"

func ensureDir(dir string) error {
	return os.MkdirAll(filepath.Clean(dir), 0o700)
}
