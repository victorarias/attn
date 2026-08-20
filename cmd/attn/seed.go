package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/protocol"
)

// `attn seed` is the garden's agent surface. It is deliberately the shortest
// path in the CLI: planting must cost one line and return the id, because
// anything worth handing off, parking, or attributing gets planted, and a
// capture that costs ceremony does not happen.
//
// Scope comes from the daemon, not from flags: the garden is one space, and the
// session id in the environment is enough for it to infer the plot a dispatched
// session was aimed at.

func runSeed() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeSeedHelp(os.Stdout)
		return
	}
	args := os.Args[3:]
	switch os.Args[2] {
	case "plant":
		runSeedPlant(args)
	case "plot":
		runSeedPlot(args)
	case "ls":
		runSeedList(args)
	case "show":
		runSeedShow(args)
	case "edit":
		runSeedEdit(args)
	case "set-resume":
		runSeedSetResume(args)
	case "export":
		runSeedExport(args)
	case "tend", "park", "harvest", "wither", "replant":
		runSeedTransition(os.Args[2], args)
	case "note":
		runSeedNote(args)
	case "attach", "detach":
		runSeedArtifact(os.Args[2], args)
	case "notes":
		runSeedNotes(args)
	case "link", "unlink":
		runSeedLink(os.Args[2] == "unlink", args)
	case "ready":
		runSeedReady(args)
	case "guide":
		runSeedGuide(args)
	default:
		fmt.Fprintf(os.Stderr, "seed: unknown command %q\n", os.Args[2])
		writeSeedHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeSeedHelp(w io.Writer) {
	fmt.Fprintf(w, `usage: attn seed <command>

A seed is the unit of work: one document, one short id, planted in the garden.
Anything worth handing off, parking, or attributing is a seed; in-session
scratch is not.

The garden lives at the home daemon. On an outpost every command here refuses,
naming the home to run it on.

commands:
  plant "<title>" [-m <body>] [--part-of <crown>] [resume flags] [flags]
        plant a seed and print its id. -m takes markdown, or - to read stdin —
        on a crown that body is the plan itself. --part-of plants it under a
        crown, born part of that plot. --resume-session-id, --cwd and --agent
        together make a dead conversation resumable without a dispatch record.

  plot [-f <path>] [--json]
        plant a whole plot in one move from a JSON payload (-f, or stdin):
        {"title": …, "body": …, "children": [{"title": …, "body": …,
        "blocks": ["<sibling step slug>"]}]}. Children are parallel by
        default; blocks names the siblings a child holds back. Prints the
        crown's id and each child's id and step slug.

  ls [--stale [--window <duration>]] [--tree] [--json]
        the garden, newest first. --tree nests each seed under the crown it is
        part of. --stale narrows to the open seeds whose log has not moved
        for the window (default %s) — a query for your judgment, never a
        reaper.

  ready [--plot <crown> | --all] [--json]
        what you can tend right now, oldest first: nothing open blocks it,
        nobody is holding it, and it is not a crown — a plot's work is its
        children. With no flags the scope is the whole garden — unless this
        session was dispatched at a crown, and then its plot; --all steps back
        out to the garden.

  link <a> blocks <b> | link <a> part-of <b>
        relate two seeds. "a blocks b" keeps b out of ready until a closes;
        "a part-of b" puts a in b's plot, and b is then the crown. A cycle is
        refused, naming both seeds and the edge to remove.

  unlink <a> blocks <b>
        remove the edge. Every link has one.

  show <id> [--json]
        one seed: the freshest handoff left on it, its state, who tends it,
        every edge that touches it in both directions, its body, and the newest
        notes on its log.

  edit <id> -m <body>
        replace the seed's markdown body without moving its state or claim.
        - reads stdin; an explicit empty -m clears the body.

  set-resume <id> (--resume-session-id <id> --cwd <path> --agent <name> | --clear)
        set or clear the seed-owned fallback used when attn has no dispatch
        record for the conversation. The three identity fields move together.

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
        append to the seed's log — what happened and what you learned, for
        whoever tends it next. - reads stdin. --handoff addresses it to your
        successor on this seed: show renders the freshest one first and tend
        prints it on the claim, so it is read before any work.

  attach <id> (--path <file> [--repo <repo>] | --notebook <id> | --url <u>) [-m "<why>"]
        associate a document with the seed. Where the document lives does not
        change — the seed records the association, and the seed's current
        artifacts are every attach that has not been detached.

  detach <id> (--path <file> [--repo <repo>] | --notebook <id> | --url <u>) [-m "<why>"]
        take an association back. Name the same document the attach named.

  notes <id> [--limit <n>] [--json]
        the whole log, newest first. show renders the newest few and says
        how many more are here.

  guide
        print the craft behind the rules: writing a body, what "done" is per
        deliverable type, where a seed belongs, edit versus replant, a seed
        whose tender is gone, and how to pick up further work.

  export <id> [--out <path>] [--json]
        write the seed's body to markdown, stamped as generated from the seed —
        the file to open, read and annotate. --out - writes to stdout; the
        default is <id>.md in the current directory. The seed stays the source:
        edit the seed and export again, never the file.

flags:
  --part-of <crown>  plant under a crown (plant)
  --resume-session-id <id>  agent-native conversation id (plant, set-resume)
  --cwd <path>        directory to reopen in (plant, set-resume)
  --agent <name>      agent driver to reopen with (plant, set-resume)
  --clear             remove the fallback identity (set-resume)
  --plot <crown>     scope a ready answer to one plot
  --tree             nest a listing under its crowns
  --stale            only open seeds whose log has not moved (ls)
  --window <d>       the stale window, like 72h or 14d (ls --stale)
  -f <path>          the plot payload to read (plot; default stdin)
  --handoff          write a note to whoever tends the seed next (note)
  --path <file>      a markdown document at this path (attach, detach)
  --repo <name>      the repository that path lives in (attach, detach)
  --notebook <id>    a Notebook document (attach, detach)
  --url <url>        anything reachable by URL (attach, detach)
  --member <name>    the crew member asking, recorded as planter, tender or
                     note author
  --session <id>     the session asking (defaults to ATTN_SESSION_ID)
  --limit <n>        how many log entries to read (notes)
  --json             print the result as JSON
`, formatWindow(garden.DefaultStaleWindow))
}

func seedClient() *client.Client {
	return client.New(config.SocketPath())
}

// gardenPrimeFromReady carries a flag-free ready answer into the launch primer.
// The daemon already inferred the scope — plot when the session was dispatched
// at a crown, the whole garden otherwise — so this is rendering, not policy.
func gardenPrimeFromReady(ready *protocol.SeedReadyResult) *hooks.GardenPrime {
	prime := &hooks.GardenPrime{Ready: len(ready.Seeds)}
	if ready.Crown == nil {
		return prime
	}
	crown := &hooks.CrownPrime{ID: ready.Crown.ID, Title: ready.Crown.Title, Body: ready.Crown.Body}
	handoffs := make(map[string]protocol.SeedNote, len(ready.Handoffs))
	for _, handoff := range ready.Handoffs {
		if _, seen := handoffs[handoff.SeedID]; !seen {
			handoffs[handoff.SeedID] = handoff
		}
	}
	for _, seed := range ready.Seeds {
		line := hooks.SeedPrime{ID: seed.ID, Title: seed.Title}
		if handoff, ok := handoffs[seed.ID]; ok {
			line.Handoff = handoff.Body
			line.HandoffAuthor = crew.HolderName(handoff.AuthorMember, handoff.AuthorSession)
		}
		crown.ReadySeeds = append(crown.ReadySeeds, line)
	}
	prime.Crown = crown
	return prime
}

func seedFail(verb string, err error) {
	fmt.Fprintf(os.Stderr, "seed %s: %v\n", verb, err)
	os.Exit(1)
}

// seedFlags are what every seed command may take. The session is resolved
// best-effort: a headless caller with no session is a real case, and only the
// commands that need a scope refuse it.
type seedFlags struct {
	fs       *flag.FlagSet
	session  *string
	member   *string
	json     *bool
	all      *bool
	tree     *bool
	stale    *bool
	window   *string
	plot     *string
	partOf   *string
	file     *string
	message  *string
	out      *string
	limit    *int
	handoff  *bool
	path     *string
	repo     *string
	notebook *string
	url      *string
	resumeID *string
	cwd      *string
	agent    *string
	clear    *bool
}

func newSeedFlags(verb string) *seedFlags {
	fs := flag.NewFlagSet("seed "+verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return &seedFlags{
		fs:       fs,
		session:  fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)"),
		member:   fs.String("member", "", "crew member planting this seed"),
		json:     fs.Bool("json", false, "print the result as JSON"),
		all:      fs.Bool("all", false, "the whole garden, overriding a dispatched session's plot"),
		tree:     fs.Bool("tree", false, "nest seeds under the crown they are part of"),
		stale:    fs.Bool("stale", false, "only open seeds whose log has not moved for the window"),
		window:   fs.String("window", "", "the stale window, like 72h or 14d"),
		plot:     fs.String("plot", "", "scope to one plot, by its crown"),
		partOf:   fs.String("part-of", "", "plant under this crown"),
		file:     fs.String("f", "", "the plot payload to read (- or empty reads stdin)"),
		message:  fs.String("m", "", "the seed's body, as markdown (- reads stdin)"),
		out:      fs.String("out", "", "file to write (- for stdout)"),
		limit:    fs.Int("limit", 0, "how many log entries to read"),
		handoff:  fs.Bool("handoff", false, "write this note to whoever tends the seed next"),
		path:     fs.String("path", "", "a markdown document at this path"),
		repo:     fs.String("repo", "", "the repository the path lives in"),
		notebook: fs.String("notebook", "", "a Notebook document, by its id"),
		url:      fs.String("url", "", "anything reachable by URL"),
		resumeID: fs.String("resume-session-id", "", "agent-native conversation id"),
		cwd:      fs.String("cwd", "", "directory to reopen in"),
		agent:    fs.String("agent", "", "agent driver to reopen with"),
		clear:    fs.Bool("clear", false, "remove the seed-owned resume identity"),
	}
}

// noteKind reads --handoff. The plain log entry is the default, so a note
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

func (f *seedFlags) wasSet(name string) bool {
	set := false
	f.fs.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			set = true
		}
	})
	return set
}

// staleWindowSeconds reads --window, refusing a value that is not a duration.
// 0 means "the daemon's default", which the result echoes back.
func (f *seedFlags) staleWindowSeconds() int {
	raw := strings.TrimSpace(*f.window)
	if raw == "" {
		return 0
	}
	window, err := parseWindow(raw)
	if err != nil {
		seedFail("ls", err)
	}
	return int(window / time.Second)
}

// parseWindow reads a stale window. Go's ParseDuration tops out at hours, and
// a stale window is naturally said in days, so `d` is accepted as 24h.
func parseWindow(raw string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(raw, "d"); ok {
		if n, err := time.ParseDuration(days + "h"); err == nil {
			return n * 24, nil
		}
	}
	window, err := time.ParseDuration(raw)
	if err != nil || window <= 0 {
		return 0, fmt.Errorf("%q is not a window; say it like 72h or 14d", raw)
	}
	return window, nil
}

// formatWindow renders a window the way it is said: whole days as Nd,
// anything else as Go's own duration string.
func formatWindow(window time.Duration) string {
	if window >= 24*time.Hour && window%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", window/(24*time.Hour))
	}
	return window.String()
}

func runSeedPlant(args []string) {
	f := newSeedFlags("plant")
	positionals := f.parse("plant", args)
	if len(positionals) != 1 {
		seedFail("plant", fmt.Errorf(`needs exactly one title, got %d: attn seed plant "what this is" [-m "the detail"]`, len(positionals)))
	}
	result, err := seedClient().SeedPlant(
		f.sessionID(), positionals[0], f.text("plant"), strings.TrimSpace(*f.partOf), strings.TrimSpace(*f.member),
		strings.TrimSpace(*f.resumeID), strings.TrimSpace(*f.cwd), strings.TrimSpace(*f.agent),
	)
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
	if !*f.stale && flagWasSet(f.fs, "window") {
		seedFail("ls", fmt.Errorf("--window is the stale window; it only means something with --stale"))
	}
	result, err := seedClient().SeedList(f.sessionID(), *f.stale, f.staleWindowSeconds())
	if err != nil {
		seedFail("ls", err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	if *f.stale {
		// The rule is half the answer: a stale seed is a question for your
		// judgment, and the reader has to know what was asked to judge it.
		fmt.Printf("open seeds whose log has not moved for %s — no note, no move, no edge. This is a query, not a reaper: tend, note or park what still matters.\n\n",
			staleWindowLabel(result.StaleWindowSeconds))
	}
	if len(result.Seeds) == 0 {
		if *f.stale {
			fmt.Println("none — every open seed has moved inside the window")
			return
		}
		fmt.Println("the garden is empty — `attn seed plant \"what this is\"` puts something in it")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tTENDER\tPLANTED\tTITLE")
	for _, row := range seedRows(result.Seeds, *f.tree) {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s%s%s\n",
			row.seed.ID, row.seed.Status, orDash(crew.HolderName(row.seed.TenderMember, row.seed.TenderSession)),
			shortStamp(row.seed.CreatedAt), strings.Repeat("  ", row.depth), row.seed.Title, plotProgressSuffix(row.seed))
	}
	w.Flush()
	if result.Total > len(result.Seeds) {
		fmt.Printf("\nshowing the newest %d of %d seeds — one read is capped at %d. The %d not shown are the oldest; `attn seed show <id>` still reaches any of them.\n",
			len(result.Seeds), result.Total, len(result.Seeds), result.Total-len(result.Seeds))
	}
}

// staleWindowLabel says the window the daemon actually applied, which is the
// default unless --window moved it.
func staleWindowLabel(seconds *int) string {
	if seconds == nil || *seconds <= 0 {
		return formatWindow(garden.DefaultStaleWindow)
	}
	return formatWindow(time.Duration(*seconds) * time.Second)
}

// plotProgressSuffix is how a crown wears its plot in a listing: the counts
// that say whether the plot is draining, and where it is stuck.
func plotProgressSuffix(seed protocol.Seed) string {
	if seed.PlotProgress == nil {
		return ""
	}
	p := *seed.PlotProgress
	return fmt.Sprintf("  [%d/%d done · %d growing · %d ready · %d blocked]",
		p.Done, p.Total, p.Growing, p.Ready, p.Blocked)
}

func runSeedPlot(args []string) {
	f := newSeedFlags("plot")
	if positionals := f.parse("plot", args); len(positionals) != 0 {
		seedFail("plot", fmt.Errorf("takes no arguments, got %q; the plot is JSON on stdin or at -f <path>", positionals[0]))
	}
	spec, err := garden.ParsePlotSpec(readPlotPayload(strings.TrimSpace(*f.file)))
	if err != nil {
		seedFail("plot", err)
	}
	msg := protocol.SeedPlotMessage{Title: spec.Title}
	if spec.Body != "" {
		msg.Body = protocol.Ptr(spec.Body)
	}
	for _, child := range spec.Children {
		wire := protocol.SeedPlotChild{Title: child.Title, Blocks: child.Blocks}
		if child.Body != "" {
			wire.Body = protocol.Ptr(child.Body)
		}
		msg.Children = append(msg.Children, wire)
	}
	result, err := seedClient().SeedPlot(f.sessionID(), strings.TrimSpace(*f.member), msg)
	if err != nil {
		seedFail("plot", err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	fmt.Println(result.Crown.ID)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, child := range result.Children {
		fmt.Fprintf(w, "  %s\t%s\t%s\n", child.ID, child.StepSlug, child.Title)
	}
	w.Flush()
}

// readPlotPayload takes the payload from a file, or from stdin when there is no
// path — the shape an agent writes a plot in.
func readPlotPayload(path string) []byte {
	if path == "" || path == "-" {
		payload, err := io.ReadAll(os.Stdin)
		if err != nil {
			seedFail("plot", fmt.Errorf("read the plot from stdin: %w", err))
		}
		return payload
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		seedFail("plot", err)
	}
	return payload
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

func runSeedEdit(args []string) {
	f := newSeedFlags("edit")
	positionals := f.parse("edit", args)
	if len(positionals) != 1 {
		seedFail("edit", fmt.Errorf("needs exactly one seed id, got %d: attn seed edit s-7k3f9m -m -", len(positionals)))
	}
	if !f.wasSet("m") {
		seedFail("edit", fmt.Errorf("needs -m <body>; use -m - to read markdown from stdin, or -m '' to clear it"))
	}
	result, err := seedClient().SeedEdit(positionals[0], f.text("edit"))
	if err != nil {
		seedFail("edit", err)
	}
	if *f.json {
		writeJSON(result.Seed)
		return
	}
	fmt.Printf("updated %s at revision %d\n", result.Seed.ID, result.Seed.Rev)
}

func runSeedSetResume(args []string) {
	f := newSeedFlags("set-resume")
	positionals := f.parse("set-resume", args)
	if len(positionals) != 1 {
		seedFail("set-resume", fmt.Errorf("needs exactly one seed id, got %d", len(positionals)))
	}
	result, err := seedClient().SeedSetResume(
		positionals[0], strings.TrimSpace(*f.resumeID), strings.TrimSpace(*f.cwd), strings.TrimSpace(*f.agent), *f.clear,
	)
	if err != nil {
		seedFail("set-resume", err)
	}
	if *f.json {
		writeJSON(result.Seed)
		return
	}
	fprintSeed(os.Stdout, result.Seed)
}

// fprintArtifacts renders the current set as a small block above the log, not
// as entries in it: what a seed points at now is a state, and reading it out of
// a timeline of attaches and detaches is work nobody should have to do.
func fprintArtifacts(w io.Writer, artifacts []protocol.SeedArtifactReference) {
	fmt.Fprintln(w, "artifacts:")
	for _, artifact := range artifacts {
		line := protocol.Deref(artifact.Path)
		if line == "" {
			line = protocol.Deref(artifact.NotebookDocumentID)
		}
		if line == "" {
			line = protocol.Deref(artifact.URL)
		}
		if line == "" {
			line = protocol.Deref(artifact.Repository)
		}
		if repo := protocol.Deref(artifact.Repository); repo != "" && repo != line {
			line += " (" + repo + ")"
		}
		fmt.Fprintf(w, "  %s  %s\n", artifact.Kind, line)
	}
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
	if len(result.Artifacts) > 0 {
		fmt.Fprintln(w)
		fprintArtifacts(w, result.Artifacts)
	}
	// The handoff is already above; repeating it in the log would print the
	// same paragraph twice on one screen. What was withheld is counted against
	// the window the daemon read, not against what is printed here, so dropping
	// it does not turn one shown note into one hidden note.
	entries := withoutNote(result.Notes, result.Handoff)
	if len(entries) > 0 {
		fmt.Fprintln(w)
		fprintNotes(w, entries, result.Seed.ID, result.NotesTotal-len(result.Notes))
	}
}

// fprintHandoff renders the freshest handoff as its own block. Nothing prints
// when there is none — a seed nobody handed over says nothing about handoffs.
func fprintHandoff(w io.Writer, handoff *protocol.SeedNote) {
	if handoff == nil {
		return
	}
	fmt.Fprintf(w, "handoff — %s, %s\n",
		orDash(crew.HolderName(handoff.AuthorMember, handoff.AuthorSession)), shortStamp(handoff.CreatedAt))
	for _, line := range strings.Split(strings.TrimRight(handoff.Body, "\n"), "\n") {
		fmt.Fprintf(w, "  %s\n", line)
	}
	fmt.Fprintln(w)
}

// withoutNote drops one note from a log by id, so a note rendered elsewhere on
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
		seedFail("ready", fmt.Errorf("takes no arguments, got %q; scope it with --plot <crown> or --all", positionals[0]))
	}
	result, err := seedClient().SeedReady(f.sessionID(), strings.TrimSpace(*f.plot), *f.all)
	if err != nil {
		seedFail("ready", err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	if result.Crown != nil {
		fmt.Printf("%s  %s%s\n\n", result.Crown.ID, result.Crown.Title, plotProgressSuffix(*result.Crown))
	}
	if len(result.Seeds) == 0 {
		fmt.Printf("nothing is ready %s — `attn seed ls` shows what is planted and what holds it\n", readyScopeName(result))
		return
	}
	handoffs := freshestHandoffs(result.Handoffs)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tPLANTED\tTITLE")
	for _, seed := range result.Seeds {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", seed.ID, seed.Status, shortStamp(seed.CreatedAt), seed.Title)
		if handoff, ok := handoffs[seed.ID]; ok {
			fmt.Fprintf(w, "\t\t\t↳ %s: %s\n",
				orDash(crew.HolderName(handoff.AuthorMember, handoff.AuthorSession)), firstLine(handoff.Body))
		}
	}
	w.Flush()
	fmt.Printf("\n%d ready %s — `attn seed tend <id>` claims one\n", len(result.Seeds), readyScopeName(result))
}

// freshestHandoffs keeps the first handoff per seed; the daemon sends them
// newest first, so the first one is the one to read before any work.
func freshestHandoffs(notes []protocol.SeedNote) map[string]protocol.SeedNote {
	freshest := make(map[string]protocol.SeedNote, len(notes))
	for _, note := range notes {
		if _, seen := freshest[note.SeedID]; !seen {
			freshest[note.SeedID] = note
		}
	}
	return freshest
}

// firstLine keeps a handoff to one row of a listing; show renders the whole one.
func firstLine(body string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	return line
}

// readyScopeName says what the answer covered, so an empty answer is never read
// as an empty garden. The plot scope is inferred, never a fence: --all steps
// back out to the garden from anywhere.
func readyScopeName(result *protocol.SeedReadyResult) string {
	if result.Scope == "plot" {
		return fmt.Sprintf("in the plot under %s", result.ScopeID)
	}
	return "in the garden"
}

func fprintSeed(out io.Writer, seed protocol.Seed) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "%s\t%s\n", seed.ID, seed.Title)
	fmt.Fprintf(w, "status\t%s\n", seed.Status)
	fmt.Fprintf(w, "step\t%s\n", seed.StepSlug)
	fmt.Fprintf(w, "planted\t%s by %s\n", shortStamp(seed.CreatedAt), orDash(crew.HolderName(seed.PlanterMember, seed.PlanterSession)))
	fmt.Fprintf(w, "tender\t%s\n", orDash(crew.HolderName(seed.TenderMember, seed.TenderSession)))
	if p := seed.PlotProgress; p != nil {
		fmt.Fprintf(w, "plot\t%d of %d done — %d growing, %d ready, %d blocked, %d dormant, %d withered\n",
			p.Done, p.Total, p.Growing, p.Ready, p.Blocked, p.Dormant, p.Withered)
	}
	if seed.Template {
		fmt.Fprintf(w, "packet\tyes\n")
	}
	if seed.Gate {
		fmt.Fprintf(w, "gate\tyes\n")
	}
	if seed.Reason != nil && *seed.Reason != "" {
		fmt.Fprintf(w, "reason\t%s\n", *seed.Reason)
	}
	if resumeID := protocol.Deref(seed.ResumeSessionID); resumeID != "" {
		fmt.Fprintf(w, "resume\t%s in %s on %s\n", resumeID, protocol.Deref(seed.ResumeCwd), protocol.Deref(seed.ResumeAgent))
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
	if open := openPlotSeeds(result.Seed); open > 0 && closedSeedStatus(string(result.Seed.Status)) {
		fmt.Fprintf(w, "its plot still holds %d open seed(s) — a closed crown over open work reads as done; close them too, or replant this one\n", open)
	}
	if result.Handoff != nil {
		fmt.Fprintln(w)
		fprintHandoff(w, result.Handoff)
	}
}

// closedSeedStatus is the two statuses that stop holding anything back.
func closedSeedStatus(status string) bool {
	return status == "harvested" || status == "withered"
}

// openPlotSeeds is how many of a crown's children are still open; zero for a
// seed with no plot.
func openPlotSeeds(seed protocol.Seed) int {
	p := seed.PlotProgress
	if p == nil {
		return 0
	}
	open := p.Total - p.Done - p.Withered
	if open < 0 {
		return 0
	}
	return open
}

// transitionLine is the one line a move prints: what the seed is now, and who
// holds it if anybody does. An agent reads it to confirm the claim landed.
func transitionLine(seed protocol.Seed) string {
	line := fmt.Sprintf("%s is %s", seed.ID, seed.Status)
	if tender := crew.HolderName(seed.TenderMember, seed.TenderSession); tender != "" {
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
		f.sessionID(), positionals[0], f.text("note"), strings.TrimSpace(*f.member), f.noteKind(), nil)
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

// runSeedArtifact writes one attach or detach. The reference is assembled from
// the flag the caller used, so the kind is chosen by what they pointed at
// rather than typed twice — and the daemon is handed a typed shape, never a
// string to read meaning out of.
func runSeedArtifact(verb string, args []string) {
	f := newSeedFlags(verb)
	positionals := f.parse(verb, args)
	if len(positionals) != 1 {
		seedFail(verb, fmt.Errorf("needs exactly one seed id, got %d: attn seed %s s-7k3f9m --path docs/plans/thing.md", len(positionals), verb))
	}
	artifact, err := f.artifact()
	if err != nil {
		seedFail(verb, err)
	}
	kind := garden.NoteKindAttach
	if verb == "detach" {
		kind = garden.NoteKindDetach
	}
	// The body is optional here alone: the daemon renders one from the reference
	// when the caller had nothing to add, so the log reads as prose either way.
	result, err := seedClient().SeedNote(
		f.sessionID(), positionals[0], f.text(verb), strings.TrimSpace(*f.member), kind, artifact)
	if err != nil {
		seedFail(verb, err)
	}
	if *f.json {
		writeJSON(result.Note)
		return
	}
	// The answer names the document that moved: with a -m the body is the
	// caller's own words, which on their own never say which document they were
	// about. Without one the daemon already rendered that same sentence as the
	// body, so it is printed once either way.
	moved := garden.DefaultNoteBody(kind, garden.ArtifactReference{
		Kind:               artifact.Kind,
		NotebookDocumentID: protocol.Deref(artifact.NotebookDocumentID),
		Repository:         protocol.Deref(artifact.Repository),
		Path:               protocol.Deref(artifact.Path),
		URL:                protocol.Deref(artifact.URL),
	})
	fmt.Printf("%s %s\n", positionals[0], moved)
	if body := strings.TrimSpace(result.Note.Body); body != "" && body != moved {
		fmt.Printf("%s\n", body)
	}
}

// artifact reads the one reference flag the caller passed. Exactly one, because
// a call naming two documents does not say which one it means.
func (f *seedFlags) artifact() (*protocol.SeedArtifactReference, error) {
	path := strings.TrimSpace(*f.path)
	repo := strings.TrimSpace(*f.repo)
	notebook := strings.TrimSpace(*f.notebook)
	url := strings.TrimSpace(*f.url)
	named := []string{}
	for flag, value := range map[string]string{"--path": path, "--notebook": notebook, "--url": url} {
		if value != "" {
			named = append(named, flag)
		}
	}
	slices.Sort(named)
	switch len(named) {
	case 0:
		return nil, fmt.Errorf("name the document: --path <file> [--repo <repository>], --notebook <document-id>, or --url <url>")
	case 1:
	default:
		return nil, fmt.Errorf("%s were all given and a reference names one document; run it once per document", strings.Join(named, " and "))
	}
	switch {
	case path != "":
		// A repository beside a path is what tells the same relative path in two
		// worktrees apart; without one the path stands alone.
		ref := &protocol.SeedArtifactReference{Kind: garden.ArtifactMarkdownFile, Path: protocol.Ptr(path)}
		if repo != "" {
			ref.Repository = protocol.Ptr(repo)
		}
		return ref, nil
	case notebook != "":
		return &protocol.SeedArtifactReference{Kind: garden.ArtifactNotebook, NotebookDocumentID: protocol.Ptr(notebook)}, nil
	default:
		return &protocol.SeedArtifactReference{Kind: garden.ArtifactURL, URL: protocol.Ptr(url)}, nil
	}
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
		fmt.Printf("nothing on this seed's log yet — `attn seed note %s -m \"what happened\"` starts it\n", positionals[0])
		return
	}
	fprintNotes(os.Stdout, result.Notes, positionals[0], result.Total-len(result.Notes))
}

// fprintNotes renders a log newest first, and says what it did not print. A
// silently short log reads as a complete one.
func fprintNotes(w io.Writer, notes []protocol.SeedNote, seedID string, withheld int) {
	for i, note := range notes {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s  %s%s\n", shortStamp(note.CreatedAt),
			orDash(crew.HolderName(note.AuthorMember, note.AuthorSession)), noteKindSuffix(note.Kind))
		fmt.Fprintf(w, "%s\n", strings.TrimRight(note.Body, "\n"))
	}
	if withheld > 0 {
		fmt.Fprintf(w, "\n%d more — `attn seed notes %s`\n", withheld, seedID)
	}
}

// noteKindSuffix labels a log entry that is not a plain note, so a handoff
// read in the log is recognisable as one written to a successor.
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
