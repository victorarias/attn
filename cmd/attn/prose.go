package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/prose"
)

// Exit codes. A caller has to tell "nothing to say" from "here is what to
// change" from "I could not read the file", and only the middle one is about
// the prose.
const (
	proseExitClean    = 0
	proseExitFindings = 1
	proseExitError    = 2
)

func runProse() {
	if len(os.Args) < 3 {
		writeProseHelp(os.Stderr)
		os.Exit(proseExitError)
	}
	switch os.Args[2] {
	case "check":
		runProseCheck(os.Args[3:])
	case "rules":
		for _, name := range prose.RuleNames() {
			fmt.Println(name)
		}
	case "-h", "--help", "help":
		writeProseHelp(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "prose: unknown subcommand %q\n\n", os.Args[2])
		writeProseHelp(os.Stderr)
		os.Exit(proseExitError)
	}
}

func writeProseHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn prose check <file|-> [file...] [flags]
       attn prose rules

Read Markdown prose and report what to simplify: a span, the rule, and the
objection. Never a score. Code fences, mermaid, front matter, tables, and link
targets are not prose and are never read.

flags:
  --json                 one JSON object with every finding, for an agent
  --deterministic-only   accepted and inert; the model judge does not exist
                         yet, so every run is already deterministic-only
  --vocab <dir>          vocabulary directory holding reject.txt / accept.txt
                         (default: the nearest docs/prose/vocabulary above the
                         file; pass "" to use none)
  --rule <name>          run only this rule; repeatable. "attn prose rules"
                         lists them

exit codes:
  0  clean
  1  findings
  2  could not read a file, or bad usage
`)
}

// proseJSONOutput is the --json shape. Findings carry which layer raised them,
// so the model judge's findings join this array without changing it.
type proseJSONOutput struct {
	Files    []string        `json:"files"`
	Findings []prose.Finding `json:"findings"`
	Counts   map[string]int  `json:"counts"`
}

type ruleList []string

func (r *ruleList) String() string     { return strings.Join(*r, ",") }
func (r *ruleList) Set(v string) error { *r = append(*r, v); return nil }

func runProseCheck(args []string) {
	fs := flag.NewFlagSet("prose check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "print one JSON object instead of lines")
	fs.Bool("deterministic-only", false, "accepted and inert until the model judge exists")
	vocabDir := fs.String("vocab", "", "vocabulary directory")
	vocabSet := false
	var only ruleList
	fs.Var(&only, "rule", "run only this rule; repeatable")

	paths, err := parseInterspersedFlagArgs(fs, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prose check: %v\n", err)
		os.Exit(proseExitError)
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "vocab" {
			vocabSet = true
		}
	})
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "prose check: name a file, or - for stdin")
		os.Exit(proseExitError)
	}
	if err := validateProseRules(only); err != nil {
		fmt.Fprintf(os.Stderr, "prose check: %v\n", err)
		os.Exit(proseExitError)
	}

	out := proseJSONOutput{Findings: []prose.Finding{}, Counts: map[string]int{}}
	for _, path := range paths {
		source, err := readProseSource(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "prose check: %v\n", err)
			os.Exit(proseExitError)
		}
		vocab, err := loadProseVocabulary(path, *vocabDir, vocabSet)
		if err != nil {
			fmt.Fprintf(os.Stderr, "prose check: %v\n", err)
			os.Exit(proseExitError)
		}
		findings, err := prose.Check(path, source, prose.Options{Vocabulary: vocab, Only: only})
		if err != nil {
			fmt.Fprintf(os.Stderr, "prose check: %s: %v\n", path, err)
			os.Exit(proseExitError)
		}
		out.Files = append(out.Files, path)
		out.Findings = append(out.Findings, findings...)
		for _, f := range findings {
			out.Counts[f.Rule]++
		}
	}

	if *jsonOut {
		encoded, err := json.Marshal(out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "prose check: encode json: %v\n", err)
			os.Exit(proseExitError)
		}
		fmt.Println(string(encoded))
	} else {
		writeProseFindings(os.Stdout, out.Findings)
		fmt.Fprintln(os.Stderr, proseSummary(len(out.Findings), len(out.Files)))
	}
	if len(out.Findings) > 0 {
		os.Exit(proseExitFindings)
	}
}

// writeProseFindings prints one finding per line, in the file:line:column form
// an editor and an agent both already know how to jump to.
func writeProseFindings(w io.Writer, findings []prose.Finding) {
	for _, f := range findings {
		fmt.Fprintf(w, "%s:%d:%d: %s: %s\n", f.File, f.Line, f.Column, f.Rule, f.Objection)
		if f.Suggestion != "" {
			fmt.Fprintf(w, "    use %q\n", f.Suggestion)
		}
	}
}

func proseSummary(findings, files int) string {
	if findings == 0 {
		return fmt.Sprintf("prose check: clean (%s)", plural(files, "file"))
	}
	return fmt.Sprintf("prose check: %s in %s", plural(findings, "finding"), plural(files, "file"))
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func readProseSource(path string) ([]byte, error) {
	if path == "-" {
		source, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		return source, nil
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return source, nil
}

// loadProseVocabulary resolves which word list applies. An explicit --vocab
// wins, including an explicit empty one; otherwise the file's own repository
// is searched, and stdin has no repository to search.
func loadProseVocabulary(path, flagValue string, flagSet bool) (*prose.Vocabulary, error) {
	dir := flagValue
	if !flagSet {
		if path == "-" {
			dir = prose.FindVocabulary(".")
		} else {
			dir = prose.FindVocabulary(path)
		}
	}
	return prose.LoadVocabulary(dir)
}

func validateProseRules(names []string) error {
	known := map[string]bool{}
	for _, name := range prose.RuleNames() {
		known[name] = true
	}
	var unknown []string
	for _, name := range names {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("unknown rule %s; `attn prose rules` lists them", strings.Join(unknown, ", "))
}
