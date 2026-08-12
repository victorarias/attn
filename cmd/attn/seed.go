package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

// `attn seed` is the garden's agent surface. It is deliberately the shortest
// path in the CLI: planting must cost one line and return the id, because
// anything worth handing off, parking, or attributing gets planted, and a
// capture that costs ceremony does not happen.
//
// Scope comes from the daemon, not from flags: the session id in the
// environment is enough for it to stamp the workspace and to scope a listing.

func runSeed() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeSeedHelp(os.Stdout)
		return
	}
	args := os.Args[3:]
	switch os.Args[2] {
	case "plant":
		runSeedPlant(args)
	case "ls":
		runSeedList(args)
	case "show":
		runSeedShow(args)
	case "export":
		runSeedExport(args)
	case "tend", "park", "harvest", "wither", "replant":
		runSeedTransition(os.Args[2], args)
	case "note":
		runSeedNote(args)
	case "notes":
		runSeedNotes(args)
	default:
		fmt.Fprintf(os.Stderr, "seed: unknown command %q\n", os.Args[2])
		writeSeedHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeSeedHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn seed <command>

A seed is the unit of work: one document, one short id, planted in the garden.
Anything worth handing off, parking, or attributing is a seed; in-session
scratch is not.

The garden lives at the home daemon. On an outpost every command here refuses,
naming the home to run it on.

commands:
  plant "<title>" [-m <body>] [flags]
        plant a seed and print its id. -m takes markdown, or - to read stdin —
        on a crown that body is the plan itself. The seed is stamped with the
        workspace of the session you are in; --workspace overrides it.

  ls [--all | --workspace <id>] [--json]
        the seeds of the workspace you are in, newest first. --all is the whole
        garden, including seeds planted outside any workspace.

  show <id> [--json]
        one seed: its state, who tends it, its body, and the newest notes on
        its trail.

  tend <id> [--member <name>]
        claim the seed and start growing it. One tender at a time: tending a
        seed somebody else holds is refused, naming them.

  park <id>
        pause the seed deliberately — it goes dormant and lets go of its
        tender. Tending it again picks it back up.

  harvest <id> -m "<what got done>"
        close the seed as done. The reason is the point of the record.

  wither <id> [-m "<why>"]
        close the seed as abandoned. Nobody is picking this up.

  replant <id>
        reopen a harvested or withered seed. A closed seed reopens before it
        moves again.

  note <id> -m "<what happened>"
        append to the seed's trail — what happened and what you learned, for
        whoever tends it next. - reads stdin.

  notes <id> [--limit <n>] [--json]
        the whole trail, newest first. show renders the newest few and says
        how many more are here.

  export <id> [--out <path>] [--json]
        write the seed's body to markdown, stamped as generated from the seed —
        the file to open, read and annotate. --out - writes to stdout; the
        default is <id>.md in the current directory. The seed stays the source:
        edit the seed and export again, never the file.

flags:
  --workspace <id>   the workspace to stamp (plant) or to list (ls)
  --member <name>    the crew member asking, recorded as planter, tender or
                     note author
  --session <id>     the session asking (defaults to ATTN_SESSION_ID)
  --limit <n>        how many trail entries to read (notes)
  --json             print the result as JSON
`)
}

func seedClient() *client.Client {
	return client.New(config.SocketPath())
}

func seedFail(verb string, err error) {
	fmt.Fprintf(os.Stderr, "seed %s: %v\n", verb, err)
	os.Exit(1)
}

// seedFlags are what every seed command may take. The session is resolved
// best-effort: a headless caller with no session is a real case, and only the
// commands that need a scope refuse it.
type seedFlags struct {
	fs        *flag.FlagSet
	session   *string
	workspace *string
	member    *string
	json      *bool
	all       *bool
	message   *string
	out       *string
	limit     *int
}

func newSeedFlags(verb string) *seedFlags {
	fs := flag.NewFlagSet("seed "+verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return &seedFlags{
		fs:        fs,
		session:   fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)"),
		workspace: fs.String("workspace", "", "workspace id"),
		member:    fs.String("member", "", "crew member planting this seed"),
		json:      fs.Bool("json", false, "print the result as JSON"),
		all:       fs.Bool("all", false, "the whole garden, not one workspace"),
		message:   fs.String("m", "", "the seed's body, as markdown (- reads stdin)"),
		out:       fs.String("out", "", "file to write (- for stdout)"),
		limit:     fs.Int("limit", 0, "how many trail entries to read"),
	}
}

// text reads -m, taking stdin when it is "-", so a long body or note can be
// piped instead of quoted into a shell.
func (f *seedFlags) text(verb string) string {
	if *f.message != "-" {
		return *f.message
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		seedFail(verb, fmt.Errorf("reading stdin: %w", err))
	}
	return string(raw)
}

// parse reads flags interleaved with positionals, the way the rest of the CLI
// does: Go's parser stops at the first positional, so a --json written after the
// title would otherwise be swallowed.
func (f *seedFlags) parse(verb string, args []string) []string {
	var positionals []string
	rest := args
	for {
		if err := f.fs.Parse(rest); err != nil {
			fmt.Fprintf(os.Stderr, "seed %s: %v\n", verb, err)
			writeSeedHelp(os.Stderr)
			os.Exit(2)
		}
		rest = f.fs.Args()
		if len(rest) == 0 {
			return positionals
		}
		positionals = append(positionals, rest[0])
		rest = rest[1:]
	}
}

func (f *seedFlags) sessionID() string {
	if id := strings.TrimSpace(*f.session); id != "" {
		return id
	}
	return strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
}

// workspaceOverride is nil unless --workspace was actually written, so the
// daemon can tell "use my session's workspace" from "use this one".
func (f *seedFlags) workspaceOverride() *string {
	if !flagWasSet(f.fs, "workspace") {
		return nil
	}
	value := strings.TrimSpace(*f.workspace)
	return &value
}

func runSeedPlant(args []string) {
	f := newSeedFlags("plant")
	positionals := f.parse("plant", args)
	if len(positionals) != 1 {
		seedFail("plant", fmt.Errorf(`needs exactly one title, got %d: attn seed plant "what this is" [-m "the detail"]`, len(positionals)))
	}
	result, err := seedClient().SeedPlant(f.sessionID(), positionals[0], f.text("plant"), f.workspaceOverride(), strings.TrimSpace(*f.member))
	if err != nil {
		seedFail("plant", err)
	}
	if *f.json {
		writeJSON(result.Seed)
		return
	}
	// The id alone on stdout is the point: an agent plants and then uses it.
	fmt.Println(result.Seed.ID)
}

func runSeedList(args []string) {
	f := newSeedFlags("ls")
	if positionals := f.parse("ls", args); len(positionals) != 0 {
		seedFail("ls", fmt.Errorf("takes no arguments, got %q; to read one seed use `attn seed show <id>`", positionals[0]))
	}
	result, err := seedClient().SeedList(f.sessionID(), f.workspaceOverride(), *f.all)
	if err != nil {
		seedFail("ls", err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	if len(result.Seeds) == 0 {
		if result.All {
			fmt.Println("the garden is empty — `attn seed plant \"what this is\"` puts something in it")
			return
		}
		fmt.Println("no seeds in this workspace — `attn seed ls --all` reads the whole garden")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tTENDER\tPLANTED\tTITLE")
	for _, seed := range result.Seeds {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			seed.ID, seed.Status, orDash(firstNonEmpty(seed.TenderMember, seed.TenderSession)),
			shortStamp(seed.CreatedAt), seed.Title)
	}
	w.Flush()
	if result.Total > len(result.Seeds) {
		fmt.Printf("\nshowing the newest %d of %d seeds — one read is capped at %d. The %d not shown are the oldest; `attn seed ls --workspace <id>` reaches them in a narrower scope.\n",
			len(result.Seeds), result.Total, len(result.Seeds), result.Total-len(result.Seeds))
	}
}

// shortStamp renders a wire timestamp for a terminal column; an unparseable one
// is printed as it arrived rather than swallowed.
func shortStamp(stamp string) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return stamp
	}
	return t.Local().Format("2006-01-02 15:04")
}

func runSeedShow(args []string) {
	f := newSeedFlags("show")
	positionals := f.parse("show", args)
	if len(positionals) != 1 {
		seedFail("show", fmt.Errorf("needs exactly one seed id, got %d: attn seed show s-7k3f9m", len(positionals)))
	}
	result, err := seedClient().SeedShow(positionals[0])
	if err != nil {
		seedFail("show", err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	printSeed(result.Seed)
	if len(result.Notes) > 0 {
		fmt.Println()
		printNotes(result.Notes, result.NotesTotal)
	}
}

func printSeed(seed protocol.Seed) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "%s\t%s\n", seed.ID, seed.Title)
	fmt.Fprintf(w, "status\t%s\n", seed.Status)
	fmt.Fprintf(w, "step\t%s\n", seed.StepSlug)
	fmt.Fprintf(w, "workspace\t%s\n", orDash(seed.WorkspaceID))
	fmt.Fprintf(w, "planted\t%s by %s\n", shortStamp(seed.CreatedAt), orDash(firstNonEmpty(seed.PlanterMember, seed.PlanterSession)))
	fmt.Fprintf(w, "tender\t%s\n", orDash(firstNonEmpty(seed.TenderMember, seed.TenderSession)))
	if seed.Template {
		fmt.Fprintf(w, "packet\tyes\n")
	}
	if seed.Gate {
		fmt.Fprintf(w, "gate\tyes\n")
	}
	if seed.Reason != nil && *seed.Reason != "" {
		fmt.Fprintf(w, "reason\t%s\n", *seed.Reason)
	}
	for _, edge := range seed.Edges {
		fmt.Fprintf(w, "edge\t%s %s\n", edge.Kind, edge.To)
	}
	w.Flush()
	if body := strings.TrimRight(seed.Body, "\n"); body != "" {
		fmt.Printf("\n%s\n", body)
	}
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// runSeedTransition drives the five lifecycle verbs. They share everything but
// their name: the daemon holds the state machine, so the CLI's job is to say
// which move and hand over who is asking.
func runSeedTransition(verb string, args []string) {
	f := newSeedFlags(verb)
	positionals := f.parse(verb, args)
	if len(positionals) != 1 {
		seedFail(verb, fmt.Errorf("needs exactly one seed id, got %d: attn seed %s s-7k3f9m", len(positionals), verb))
	}
	result, err := seedClient().SeedTransition(
		f.sessionID(), positionals[0], verb, f.text(verb), strings.TrimSpace(*f.member))
	if err != nil {
		seedFail(verb, err)
	}
	if *f.json {
		writeJSON(result.Seed)
		return
	}
	fmt.Println(transitionLine(result.Seed))
}

// transitionLine is the one line a move prints: what the seed is now, and who
// holds it if anybody does. An agent reads it to confirm the claim landed.
func transitionLine(seed protocol.Seed) string {
	line := fmt.Sprintf("%s is %s", seed.ID, seed.Status)
	if tender := firstNonEmpty(seed.TenderMember, seed.TenderSession); tender != "" {
		line += fmt.Sprintf(", tended by %s", tender)
	}
	if seed.Reason != nil && *seed.Reason != "" {
		line += fmt.Sprintf(" — %s", *seed.Reason)
	}
	return line
}

func runSeedNote(args []string) {
	f := newSeedFlags("note")
	positionals := f.parse("note", args)
	if len(positionals) != 1 {
		seedFail("note", fmt.Errorf(`needs exactly one seed id, got %d: attn seed note s-7k3f9m -m "what happened"`, len(positionals)))
	}
	result, err := seedClient().SeedNote(f.sessionID(), positionals[0], f.text("note"), strings.TrimSpace(*f.member))
	if err != nil {
		seedFail("note", err)
	}
	if *f.json {
		writeJSON(result.Note)
		return
	}
	fmt.Printf("noted on %s\n", result.Note.SeedID)
}

func runSeedNotes(args []string) {
	f := newSeedFlags("notes")
	positionals := f.parse("notes", args)
	if len(positionals) != 1 {
		seedFail("notes", fmt.Errorf("needs exactly one seed id, got %d: attn seed notes s-7k3f9m", len(positionals)))
	}
	result, err := seedClient().SeedNotes(positionals[0], *f.limit)
	if err != nil {
		seedFail("notes", err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	if len(result.Notes) == 0 {
		fmt.Printf("nothing on this seed's trail yet — `attn seed note %s -m \"what happened\"` starts it\n", positionals[0])
		return
	}
	printNotes(result.Notes, result.Total)
}

// printNotes renders a trail newest first, and says what it did not print. A
// silently short trail reads as a complete one.
func printNotes(notes []protocol.SeedNote, total int) {
	for i, note := range notes {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("%s  %s\n", shortStamp(note.CreatedAt), orDash(firstNonEmpty(note.AuthorMember, note.AuthorSession)))
		fmt.Printf("%s\n", strings.TrimRight(note.Body, "\n"))
	}
	if withheld := total - len(notes); withheld > 0 && len(notes) > 0 {
		fmt.Printf("\n%d more — `attn seed notes %s`\n", withheld, notes[0].SeedID)
	}
}

// runSeedExport is the review bridge: it renders a seed to a markdown file so
// the annotation surface — which addresses files by workspace path — works on a
// seed today. Slice 6 removes the file by rendering the seed itself.
func runSeedExport(args []string) {
	f := newSeedFlags("export")
	positionals := f.parse("export", args)
	if len(positionals) != 1 {
		seedFail("export", fmt.Errorf("needs exactly one seed id, got %d: attn seed export s-7k3f9m", len(positionals)))
	}
	result, err := seedClient().SeedShow(positionals[0])
	if err != nil {
		seedFail("export", err)
	}
	rendered := garden.Export(gardenSeedFromWire(result.Seed))

	out := strings.TrimSpace(*f.out)
	if out == "-" {
		fmt.Print(rendered)
		return
	}
	if out == "" {
		out = result.Seed.ID + ".md"
	}
	if err := os.WriteFile(out, []byte(rendered), 0644); err != nil {
		seedFail("export", fmt.Errorf("writing %s: %w", out, err))
	}
	absolute, err := filepath.Abs(out)
	if err != nil {
		absolute = out
	}
	if *f.json {
		writeJSON(struct {
			SeedID string `json:"seed_id"`
			Path   string `json:"path"`
			Bytes  int    `json:"bytes"`
		}{result.Seed.ID, absolute, len(rendered)})
		return
	}
	fmt.Printf("wrote %s from %s — edit the seed, not the file\n", absolute, result.Seed.ID)
}

// gardenSeedFromWire is the export's only need for the domain type: rendering
// belongs to the garden package, so the CLI and a future in-app renderer cannot
// disagree about what a crown looks like.
func gardenSeedFromWire(seed protocol.Seed) garden.Seed {
	return garden.Seed{ID: seed.ID, Title: seed.Title, Body: seed.Body}
}
