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
	case "link", "unlink":
		runSeedLink(os.Args[2] == "unlink", args)
	case "ready":
		runSeedReady(args)
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

  ls [--all | --workspace <id>] [--tree] [--json]
        the seeds of the workspace you are in, newest first. --all is the whole
        garden, including seeds planted outside any workspace. --tree nests each
        seed under the crown it is part of.

  ready [--plot <crown> | --workspace <id> | --all] [--json]
        what you can tend right now, oldest first: nothing open blocks it,
        nobody is holding it, and it is not a crown — a plot's work is its
        children. With no flags the scope is the workspace you are in.

  link <a> blocks <b> | link <a> part-of <b>
        relate two seeds. "a blocks b" keeps b out of ready until a closes;
        "a part-of b" puts a in b's plot, and b is then the crown. A cycle is
        refused, naming both seeds and the edge to remove.

  unlink <a> blocks <b>
        remove the edge. Every link has one.

  show <id> [--json]
        one seed: the freshest handoff left on it, its state, who tends it,
        every edge that touches it in both directions, its body, and the newest
        notes on its trail.

  tend <id> [--member <name>]
        claim the seed and start growing it. One tender at a time: tending a
        seed somebody else holds is refused, naming them. The freshest handoff
        prints on the claim, so picking a seed up primes you.

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

  note <id> -m "<what happened>" [--handoff]
        append to the seed's trail — what happened and what you learned, for
        whoever tends it next. - reads stdin. --handoff addresses it to your
        successor on this seed: show renders the freshest one first and tend
        prints it on the claim, so it is read before any work.

  notes <id> [--limit <n>] [--json]
        the whole trail, newest first. show renders the newest few and says
        how many more are here.

  export <id> [--out <path>] [--json]
        write the seed's body to markdown, stamped as generated from the seed —
        the file to open, read and annotate. --out - writes to stdout; the
        default is <id>.md in the current directory. The seed stays the source:
        edit the seed and export again, never the file.

flags:
  --plot <crown>     scope a ready answer to one plot
  --tree             nest a listing under its crowns
  --workspace <id>   the workspace to stamp (plant) or to list (ls, ready)
  --handoff          write a note to whoever tends the seed next (note)
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
	tree      *bool
	plot      *string
	message   *string
	out       *string
	limit     *int
	handoff   *bool
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
		tree:      fs.Bool("tree", false, "nest seeds under the crown they are part of"),
		plot:      fs.String("plot", "", "scope to one plot, by its crown"),
		message:   fs.String("m", "", "the seed's body, as markdown (- reads stdin)"),
		out:       fs.String("out", "", "file to write (- for stdout)"),
		limit:     fs.Int("limit", 0, "how many trail entries to read"),
		handoff:   fs.Bool("handoff", false, "write this note to whoever tends the seed next"),
	}
}

// noteKind reads --handoff. The plain trail entry is the default, so a note
// written the way it always was stays one.
func (f *seedFlags) noteKind() string {
	if *f.handoff {
		return garden.NoteKindHandoff
	}
	return ""
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
	for _, row := range seedRows(result.Seeds, *f.tree) {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s%s\n",
			row.seed.ID, row.seed.Status, orDash(firstNonEmpty(row.seed.TenderMember, row.seed.TenderSession)),
			shortStamp(row.seed.CreatedAt), strings.Repeat("  ", row.depth), row.seed.Title)
	}
	w.Flush()
	if result.Total > len(result.Seeds) {
		fmt.Printf("\nshowing the newest %d of %d seeds — one read is capped at %d. The %d not shown are the oldest; `attn seed ls --workspace <id>` reaches them in a narrower scope.\n",
			len(result.Seeds), result.Total, len(result.Seeds), result.Total-len(result.Seeds))
	}
}

// seedRow is one printed line: the seed and how deep it sits under its crown.
type seedRow struct {
	seed  protocol.Seed
	depth int
}

// seedRows orders a listing. Flat keeps the daemon's order; --tree hands the
// ordering to the garden package, so the CLI and a future in-app tree cannot
// disagree about what nests under what.
func seedRows(seeds []protocol.Seed, tree bool) []seedRow {
	rows := make([]seedRow, 0, len(seeds))
	if !tree {
		for _, seed := range seeds {
			rows = append(rows, seedRow{seed: seed})
		}
		return rows
	}
	byID := make(map[string]protocol.Seed, len(seeds))
	domain := make([]garden.Seed, 0, len(seeds))
	for _, seed := range seeds {
		byID[seed.ID] = seed
		domain = append(domain, gardenSeedFromWire(seed))
	}
	for _, row := range garden.Tree(domain) {
		rows = append(rows, seedRow{seed: byID[row.Seed.ID], depth: row.Depth})
	}
	return rows
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
	fprintSeedShow(os.Stdout, result)
}

// fprintSeedShow renders one seed for a reader. The handoff comes before the
// seed, not after it: it was written to whoever is reading this, and a
// continuity note under the body is a note nobody reads.
func fprintSeedShow(w io.Writer, result *protocol.SeedShowResult) {
	fprintHandoff(w, result.Handoff)
	fprintSeed(w, result.Seed)
	if len(result.Relations) > 0 {
		fmt.Fprintln(w)
		fprintRelations(w, result.Relations)
	}
	// The handoff is already above; repeating it in the trail would print the
	// same paragraph twice on one screen. What was withheld is counted against
	// the window the daemon read, not against what is printed here, so dropping
	// it does not turn one shown note into one hidden note.
	trail := withoutNote(result.Notes, result.Handoff)
	if len(trail) > 0 {
		fmt.Fprintln(w)
		fprintNotes(w, trail, result.Seed.ID, result.NotesTotal-len(result.Notes))
	}
}

// fprintHandoff renders the freshest handoff as its own block. Nothing prints
// when there is none — a seed nobody handed over says nothing about handoffs.
func fprintHandoff(w io.Writer, handoff *protocol.SeedNote) {
	if handoff == nil {
		return
	}
	fmt.Fprintf(w, "handoff — %s, %s\n",
		orDash(firstNonEmpty(handoff.AuthorMember, handoff.AuthorSession)), shortStamp(handoff.CreatedAt))
	for _, line := range strings.Split(strings.TrimRight(handoff.Body, "\n"), "\n") {
		fmt.Fprintf(w, "  %s\n", line)
	}
	fmt.Fprintln(w)
}

// withoutNote drops one note from a trail by id, so a note rendered elsewhere on
// the same screen is not printed twice.
func withoutNote(notes []protocol.SeedNote, drop *protocol.SeedNote) []protocol.SeedNote {
	if drop == nil {
		return notes
	}
	out := make([]protocol.SeedNote, 0, len(notes))
	for _, note := range notes {
		if note.ID != drop.ID {
			out = append(out, note)
		}
	}
	return out
}

// fprintRelations renders both directions of a seed's edges. The other seed's
// state is on the line because "blocked-by s-7k3f9m" is only actionable when the
// reader can see whether that blocker is still open.
func fprintRelations(out io.Writer, relations []protocol.SeedRelation) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, relation := range relations {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", relation.Label, relation.SeedID, relation.Status, relation.Title)
	}
	w.Flush()
}

// runSeedLink relates two seeds, reading `link <a> blocks <b>` as it is said out
// loud. The daemon owns what may be linked, so a refusal here is only about the
// shape of the command.
func runSeedLink(unlink bool, args []string) {
	verb := "link"
	if unlink {
		verb = "unlink"
	}
	f := newSeedFlags(verb)
	positionals := f.parse(verb, args)
	if len(positionals) != 3 {
		seedFail(verb, fmt.Errorf("reads as a sentence: `attn seed %s s-7k3f9m %s s-2p4qxv`, where the kind is %s",
			verb, garden.EdgeBlocks, strings.Join(garden.LinkableKinds, " or ")))
	}
	result, err := seedClient().SeedLink(positionals[0], positionals[1], positionals[2], unlink)
	if err != nil {
		seedFail(verb, err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	switch {
	case unlink:
		fmt.Printf("%s no longer %s %s\n", positionals[0], positionals[1], positionals[2])
	case !result.Changed:
		fmt.Printf("%s already %s %s\n", positionals[0], positionals[1], positionals[2])
	default:
		fmt.Printf("%s %s %s\n", positionals[0], positionals[1], positionals[2])
	}
}

// runSeedReady is the flag-free question: what can I tend now. Scope comes from
// the daemon, which knows the session, so an agent asks without naming its own
// context.
func runSeedReady(args []string) {
	f := newSeedFlags("ready")
	if positionals := f.parse("ready", args); len(positionals) != 0 {
		seedFail("ready", fmt.Errorf("takes no arguments, got %q; scope it with --plot <crown>, --workspace <id> or --all", positionals[0]))
	}
	result, err := seedClient().SeedReady(f.sessionID(), strings.TrimSpace(*f.plot), f.workspaceOverride(), *f.all)
	if err != nil {
		seedFail("ready", err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	if len(result.Seeds) == 0 {
		fmt.Printf("nothing is ready %s — `attn seed ls` shows what is planted and what holds it\n", readyScopeName(result))
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tPLANTED\tTITLE")
	for _, seed := range result.Seeds {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", seed.ID, seed.Status, shortStamp(seed.CreatedAt), seed.Title)
	}
	w.Flush()
	fmt.Printf("\n%d ready %s — `attn seed tend <id>` claims one\n", len(result.Seeds), readyScopeName(result))
}

// readyScopeName says what the answer covered, so an empty answer is never read
// as an empty garden.
func readyScopeName(result *protocol.SeedReadyResult) string {
	switch result.Scope {
	case "plot":
		return fmt.Sprintf("in the plot under %s", result.ScopeID)
	case "workspace":
		if result.ScopeID == "" {
			return "outside any workspace"
		}
		return "in this workspace"
	default:
		return "in the garden"
	}
}

func fprintSeed(out io.Writer, seed protocol.Seed) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
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
	if seed.Ready {
		fmt.Fprintf(w, "ready\tyes\n")
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
		// The whole result, not just the seed: a tend carries the handoff, and a
		// --json caller that only ever saw the seed would never be primed.
		writeJSON(result)
		return
	}
	fprintTransition(os.Stdout, result)
}

// fprintTransition renders what a move did, and then the handoff when the move
// was the pickup. The claim is confirmed first — the tender needs to know it
// landed — and the note it primes with follows on the same screen.
func fprintTransition(w io.Writer, result *protocol.SeedTransitionResult) {
	fmt.Fprintln(w, transitionLine(result.Seed))
	if result.Handoff != nil {
		fmt.Fprintln(w)
		fprintHandoff(w, result.Handoff)
	}
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
	result, err := seedClient().SeedNote(
		f.sessionID(), positionals[0], f.text("note"), strings.TrimSpace(*f.member), f.noteKind())
	if err != nil {
		seedFail("note", err)
	}
	if *f.json {
		writeJSON(result.Note)
		return
	}
	if result.Note.Kind == garden.NoteKindHandoff {
		fmt.Printf("handoff left on %s — whoever tends it next reads this first\n", result.Note.SeedID)
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
	fprintNotes(os.Stdout, result.Notes, positionals[0], result.Total-len(result.Notes))
}

// fprintNotes renders a trail newest first, and says what it did not print. A
// silently short trail reads as a complete one.
func fprintNotes(w io.Writer, notes []protocol.SeedNote, seedID string, withheld int) {
	for i, note := range notes {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s  %s%s\n", shortStamp(note.CreatedAt),
			orDash(firstNonEmpty(note.AuthorMember, note.AuthorSession)), noteKindSuffix(note.Kind))
		fmt.Fprintf(w, "%s\n", strings.TrimRight(note.Body, "\n"))
	}
	if withheld > 0 {
		fmt.Fprintf(w, "\n%d more — `attn seed notes %s`\n", withheld, seedID)
	}
}

// noteKindSuffix labels a trail entry that is not a plain note, so a handoff
// read in the trail is recognisable as one written to a successor.
func noteKindSuffix(kind string) string {
	if kind == "" || kind == garden.NoteKindNote {
		return ""
	}
	return "  " + kind
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

// gardenSeedFromWire carries a wire seed back into the domain type, which is
// where rendering and hierarchy live — so the CLI and a future in-app renderer
// cannot disagree about what a crown looks like or what nests under it.
func gardenSeedFromWire(seed protocol.Seed) garden.Seed {
	out := garden.Seed{
		ID: seed.ID, Title: seed.Title, Body: seed.Body, Status: seed.Status,
		Edges: make([]garden.Edge, 0, len(seed.Edges)),
	}
	for _, edge := range seed.Edges {
		out.Edges = append(out.Edges, garden.Edge{Kind: edge.Kind, To: edge.To})
	}
	return out
}
