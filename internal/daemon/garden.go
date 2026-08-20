package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The garden's daemon half: the collections it lives in, the commands agents
// plant, move and read seeds with, and the snapshot the app's panel renders.
//
// The daemon is where a lifecycle move is decided. It reads the seed, asks
// internal/garden whether the move is legal from the state the seed is actually
// in, and writes the answer against the revision it read — so two sessions
// racing for the same claim produce one tender and one named refusal, not two
// winners.
//
// Every command passes the enrollment fence first. The garden has exactly one
// owner — the home daemon — and an outpost holds no part of it, not even a
// cache; the refusal names the home and the plan that tracks the uplink.
//
// Storage is the document store under `core/garden`. A planting commits the
// document with the docstore's own change fact, so live queries on the
// collection wake exactly as they do for any other write, and then publishes
// `garden.planted` — the domain fact whose projection re-pushes the panel and
// which a future sync engine consumes with a cursor.

// gardenSnapshotLimit bounds one push of the garden. Measured 2026-08-12
// against production ~/.attn: the largest list attn pushes whole is 59 tickets,
// and this plan plants seven seeds on its first day. A thousand is the
// document store's own MaxLimit and more than an order of magnitude past any
// real garden — a tripwire, not a budget. Past it the push carries the total
// beside the truncated list, so the panel says what it is not showing rather
// than quietly ending at a thousand.
const gardenSnapshotLimit = docstore.MaxLimit

// ensureGardenCollections declares the garden's collections at startup.
//
// It runs on every daemon, home or outpost, because a declaration is schema and
// not state: an outpost's garden tables stay empty (the fence refuses every
// write), and declaring them anyway means `attn enrollment leave` makes a daemon
// a working home immediately rather than at its next restart. Declaring is
// idempotent — an unchanged declaration is a no-op in the store.
func (d *Daemon) ensureGardenCollections() {
	if d.store == nil {
		return
	}
	for _, schema := range []docstore.CollectionSchema{garden.SeedsSchema(), garden.NotesSchema(), garden.DispatchesSchema()} {
		redeclared, err := d.store.DefineDocumentCollection(schema, time.Now())
		if err != nil {
			d.logf("garden: declaring %s/%s: %v", schema.Namespace, schema.Collection, err)
			continue
		}
		if redeclared {
			d.publishCollectionRedeclared(schema.Namespace, schema.Collection)
		}
	}
	d.dispatchSeedsMu.Lock()
	d.dispatchSeeds, d.dispatchFromChief, d.dispatchSeedsLoaded = nil, nil, false
	d.dispatchSeedsMu.Unlock()
}

// seedsCollection reads the seeds declaration, which carries the minted table
// name every query compiles against.
func (d *Daemon) seedsCollection() (*docstore.CollectionSchema, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	return d.collectionFor(garden.Namespace, garden.CollectionSeeds)
}

// plantSeed writes one seed and publishes the fact that it exists. It is create-
// only: the id was just minted, so an existing document at that id is a
// collision, and the caller mints again rather than overwriting somebody's seed.
func (d *Daemon) plantSeed(schema docstore.CollectionSchema, seed garden.Seed) (docstore.Document, error) {
	body, err := seed.Encode()
	if err != nil {
		return docstore.Document{}, err
	}
	expected := docstore.ExpectAbsent
	fact := documentChangedFact(garden.Namespace, garden.CollectionSeeds, seed.ID, false)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: schema, ID: seed.ID, Body: body, Expected: &expected,
	}, fact, time.Now())
	if err != nil {
		return docstore.Document{}, err
	}
	d.announceCommittedWrite(fact, written.Seq)
	// The domain fact rides after the write it describes: a consumer that reads
	// it always finds the document. Its projection is the panel push.
	d.publishFact(FactGardenPlanted, seed.ID, nil)

	doc, found, err := d.store.GetDocument(schema, seed.ID)
	if err != nil || !found {
		// The write committed; only the read-back failed. Answer from what was
		// written rather than failing a planting that happened.
		return docstore.Document{ID: seed.ID, Body: body, Rev: written.Rev}, nil
	}
	return *doc, nil
}

// writeSeed stores a seed the caller already read and changed, refusing the
// write if the document moved underneath it. Every lifecycle move goes through
// here, so the domain fact and the docstore's own change fact are published
// together, in that order, exactly once.
func (d *Daemon) writeSeed(schema docstore.CollectionSchema, seed garden.Seed, expected int64, fact string) (docstore.Document, error) {
	body, err := seed.Encode()
	if err != nil {
		return docstore.Document{}, err
	}
	changed := documentChangedFact(garden.Namespace, garden.CollectionSeeds, seed.ID, false)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: schema, ID: seed.ID, Body: body, Expected: &expected,
	}, changed, time.Now())
	if err != nil {
		return docstore.Document{}, err
	}
	d.announceCommittedWrite(changed, written.Seq)
	d.publishFact(fact, seed.ID, nil)

	doc, found, err := d.store.GetDocument(schema, seed.ID)
	if err != nil || !found {
		return docstore.Document{ID: seed.ID, Body: body, Rev: written.Rev}, nil
	}
	return *doc, nil
}

// readSeed reads one seed by id, refusing a malformed id before it reaches the
// store so the caller is told what a seed id looks like.
func (d *Daemon) readSeed(id string) (garden.Seed, docstore.Document, error) {
	id = strings.TrimSpace(id)
	if err := garden.ValidateID(id); err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	doc, found, err := d.store.GetDocument(*schema, id)
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	if !found {
		return garden.Seed{}, docstore.Document{}, fmt.Errorf("no seed %s is planted here; `attn seed ls` lists the garden", id)
	}
	seed, err := garden.Decode(doc.Body)
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	return seed, *doc, nil
}

// gardenRead is the whole garden as one read: the seeds newest first, the
// document each came from, and which of them are ready.
//
// Every surface starts here rather than querying its own scope, because an edge
// and a readiness answer are properties of the whole graph — the seed blocking
// this workspace's work may live in another one. Scoping happens after the read.
type gardenRead struct {
	seeds []garden.Seed
	docs  map[string]docstore.Document
	ready map[string]bool
}

// readGarden reads every seed, bounded by the same limit one push carries, and
// decides readiness over the set. Past that bound a scoped list can be short of
// its own count — which is the count each surface prints beside it.
func (d *Daemon) readGarden() (gardenRead, error) {
	read, _, err := d.runDocQuery(docstore.Query{
		Namespace:  garden.Namespace,
		Collection: garden.CollectionSeeds,
		Sort:       &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
		Limit:      gardenSnapshotLimit,
	})
	if err != nil {
		return gardenRead{}, err
	}
	out := gardenRead{
		seeds: make([]garden.Seed, 0, len(read.Documents)),
		docs:  make(map[string]docstore.Document, len(read.Documents)),
		ready: map[string]bool{},
	}
	for _, doc := range read.Documents {
		seed, err := garden.Decode(doc.Body)
		if err != nil {
			// One unreadable body must not blank the panel; name it and go on.
			d.logf("garden: seed %s has an unreadable body: %v", doc.ID, err)
			continue
		}
		out.seeds = append(out.seeds, seed)
		out.docs[seed.ID] = doc
	}
	for _, seed := range garden.Ready(out.seeds, d.sessionExists) {
		out.ready[seed.ID] = true
	}
	return out, nil
}

// wire renders seeds this read holds. A seed the read did not cover — one
// written since — still renders, without a stamp it cannot know. A crown
// carries its plot's progress, computed here over the whole read for the same
// reason readiness is: one rule, rendered everywhere.
func (g gardenRead) wire(seeds []garden.Seed) []protocol.Seed {
	out := make([]protocol.Seed, 0, len(seeds))
	for _, seed := range seeds {
		wire := seedToProtocol(seed, g.docs[seed.ID], g.ready[seed.ID])
		if progress, ok := g.progress(seed.ID); ok {
			wire.PlotProgress = progress
		}
		out = append(out, wire)
	}
	return out
}

// progress is one crown's plot progress, and false for a seed nothing is part
// of — a childless seed has no plot to report on.
func (g gardenRead) progress(id string) (*protocol.SeedPlotProgress, bool) {
	p := garden.PlotProgress(g.seeds, id, g.ready)
	if p.Total == 0 {
		return nil, false
	}
	return &protocol.SeedPlotProgress{
		Total:    p.Total,
		Done:     p.Done,
		Withered: p.Withered,
		Growing:  p.Growing,
		Dormant:  p.Dormant,
		Ready:    p.Ready,
		Blocked:  p.Blocked,
	}, true
}

// gardenReady answers which seeds are tendable now, for a caller that has
// already written and needs only to render one seed. A failed read leaves the
// answer empty rather than failing a move that committed.
func (d *Daemon) gardenReady() map[string]bool {
	read, err := d.readGarden()
	if err != nil {
		d.logf("garden: reading the garden to decide readiness: %v", err)
		return map[string]bool{}
	}
	return read.ready
}

// countSeeds is what makes a truncated read honest: every surface shows a
// bounded list beside the count of what the whole garden holds.
func (d *Daemon) countSeeds() int {
	if d.store == nil {
		return 0
	}
	read, found, err := d.store.CountQuery(docstore.Query{
		Namespace: garden.Namespace, Collection: garden.CollectionSeeds,
	})
	if err != nil || !found {
		return 0
	}
	return read.Count
}

func seedToProtocol(seed garden.Seed, doc docstore.Document, ready bool) protocol.Seed {
	out := protocol.Seed{
		ID:             seed.ID,
		Title:          seed.Title,
		Body:           seed.Body,
		Status:         seed.Status,
		StepSlug:       seed.StepSlug,
		PlanterSession: seed.PlanterSession,
		PlanterMember:  seed.PlanterMember,
		TenderSession:  seed.TenderSession,
		TenderMember:   seed.TenderMember,
		Edges:          make([]protocol.SeedEdge, 0, len(seed.Edges)),
		Template:       seed.Template,
		Gate:           seed.Gate,
		Ready:          ready,
		Vars:           make([]protocol.SeedVar, 0, len(seed.Vars)),
		Rev:            int(doc.Rev),
		CreatedAt:      doc.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:      doc.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if seed.Reason != "" {
		out.Reason = protocol.Ptr(seed.Reason)
	}
	for _, e := range seed.Edges {
		out.Edges = append(out.Edges, protocol.SeedEdge{Kind: e.Kind, To: e.To})
	}
	for _, v := range seed.Vars {
		wire := protocol.SeedVar{Name: v.Name}
		if v.Description != "" {
			wire.Description = protocol.Ptr(v.Description)
		}
		if v.Required {
			wire.Required = protocol.Ptr(true)
		}
		if v.Default != "" {
			wire.Default = protocol.Ptr(v.Default)
		}
		if v.Pattern != "" {
			wire.Pattern = protocol.Ptr(v.Pattern)
		}
		if len(v.Enum) > 0 {
			wire.Enum = v.Enum
		}
		out.Vars = append(out.Vars, wire)
	}
	return out
}

// seedsForBroadcast is the payload of both initial_state and
// garden_seeds_updated: the whole garden, bounded, newest first. Clients filter
// by workspace themselves — switching workspaces must not cost a round trip.
func (d *Daemon) seedsForBroadcast() []protocol.Seed {
	if d.store == nil {
		return nil
	}
	// An outpost has no garden to push. Not an error here: initial_state is not
	// a garden command, and a refusal in a snapshot would be noise on every
	// connect.
	if err := d.requireHome(garden.Surface); err != nil {
		return nil
	}
	read, err := d.readGarden()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("garden: reading seeds for broadcast: %v", err)
		}
		return nil
	}
	return read.wire(read.seeds)
}

// countSeedsForBroadcast is seedsForBroadcast's count, behind the same fence:
// an outpost pushes no seeds and must report no total either, or the panel
// would say it is hiding a garden that is not there.
func (d *Daemon) countSeedsForBroadcast() int {
	if d.store == nil {
		return 0
	}
	if err := d.requireHome(garden.Surface); err != nil {
		return 0
	}
	return d.countSeeds()
}

// projectGardenSeeds re-pushes the garden to every client. Like every other
// whole-list projection it goes through projectSnapshot, so planting a plot puts
// one garden on the wire instead of one per seed.
func (d *Daemon) projectGardenSeeds() {
	if d.store == nil {
		return
	}
	d.projectSnapshot(snapshotGarden, func() {
		seeds := d.seedsForBroadcast()
		total := d.countSeedsForBroadcast()
		if total > len(seeds) {
			d.logf("garden: %d seeds, pushing the newest %d (limit %d); the panel says so",
				total, len(seeds), gardenSnapshotLimit)
		}
		// GardenSeedsUpdatedMessage is its own top-level event, so the wsHub's
		// WebSocketEvent-only broadcastListener cannot see it; tests use this hook.
		if d.gardenBroadcastHook != nil {
			d.gardenBroadcastHook(seeds, total)
		}
		if d.wsHub == nil {
			return
		}
		d.broadcastMessage(&protocol.GardenSeedsUpdatedMessage{
			Event:           protocol.EventGardenSeedsUpdated,
			Seeds:           seeds,
			Total:           total,
			StrandedTickets: d.strandedTicketsField(),
		})
	})
}

// IPC handlers.

// sendGardenError is the one refusal path. It goes through sendError so the
// fence's multi-line message reaches the caller verbatim: an agent that hits it
// has to read the home's id and what to do next.
func (d *Daemon) sendGardenError(conn net.Conn, verb string, err error) {
	d.sendError(conn, fmt.Sprintf("seed %s: %v", verb, err))
}

func (d *Daemon) handleSeedPlant(conn net.Conn, msg *protocol.SeedPlantMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "plant", err)
		return
	}
	title := strings.TrimSpace(msg.Title)
	body := protocol.Deref(msg.Body)
	if err := garden.ValidatePlant(title, body); err != nil {
		d.sendGardenError(conn, "plant", err)
		return
	}
	schema, err := d.seedsCollection()
	if err != nil {
		d.sendGardenError(conn, "plant", err)
		return
	}

	sessionID := strings.TrimSpace(protocol.Deref(msg.SourceSessionID))
	seed := garden.Seed{
		Title:          title,
		Body:           body,
		Status:         garden.StatusPlanted,
		StepSlug:       garden.StepSlug(title),
		PlanterSession: sessionID,
		PlanterMember:  d.resolveTenderMember(protocol.Deref(msg.Member), sessionID),
		Edges:          []garden.Edge{},
		Vars:           []garden.Var{},
	}
	// Planting under a crown: the seed is born part of that plot. The crown must
	// be planted — an edge to nothing is a plot nobody can find — but its state
	// does not matter: planting into a closed plot is the planter's call.
	if crown := strings.TrimSpace(protocol.Deref(msg.PartOf)); crown != "" {
		if _, _, err := d.readSeed(crown); err != nil {
			d.sendGardenError(conn, "plant", err)
			return
		}
		seed.Edges = append(seed.Edges, garden.Edge{Kind: garden.EdgePartOf, To: crown})
	}
	seed, doc, err := d.mintAndPlant(*schema, seed)
	if err != nil {
		d.sendGardenError(conn, "plant", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{
		Ok:              true,
		SeedPlantResult: &protocol.SeedPlantResult{Seed: seedToProtocol(seed, doc, d.gardenReady()[seed.ID])},
	})
}

// handleSeedPlot plants a whole plot in one move: the crown, then each child
// part of it, with the payload's blocks edges between siblings. Everything that
// can be refused is refused before the first write; a write that fails midway
// names what was already planted, because half a plot in the garden must not
// read as no plot.
func (d *Daemon) handleSeedPlot(conn net.Conn, msg *protocol.SeedPlotMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "plot", err)
		return
	}
	spec := garden.PlotSpec{Title: strings.TrimSpace(msg.Title), Body: protocol.Deref(msg.Body)}
	for _, child := range msg.Children {
		spec.Children = append(spec.Children, garden.PlotChildSpec{
			Title:  strings.TrimSpace(child.Title),
			Body:   protocol.Deref(child.Body),
			Blocks: child.Blocks,
		})
	}
	if err := garden.ValidatePlotSpec(spec); err != nil {
		d.sendGardenError(conn, "plot", err)
		return
	}
	schema, err := d.seedsCollection()
	if err != nil {
		d.sendGardenError(conn, "plot", err)
		return
	}
	sessionID := strings.TrimSpace(protocol.Deref(msg.SourceSessionID))
	member := strings.TrimSpace(protocol.Deref(msg.Member))

	var result protocol.SeedPlotResult
	planted := []string{}

	// One garden push for the whole plot, not one per seed.
	var plotErr error
	d.coalesceSnapshots(func() {
		crown, crownDoc, err := d.mintAndPlant(*schema, garden.Seed{
			Title: spec.Title, Body: spec.Body, Status: garden.StatusPlanted,
			StepSlug: garden.StepSlug(spec.Title), PlanterSession: sessionID, PlanterMember: member,
			Edges: []garden.Edge{}, Vars: []garden.Var{},
		})
		if err != nil {
			plotErr = err
			return
		}
		planted = append(planted, crown.ID)
		// Children are minted after the crown so their edges can name real ids;
		// blocks edges point at siblings, so ids are minted for all children
		// before any child is written.
		ids := make(map[string]string, len(spec.Children))
		childSeeds := make([]garden.Seed, 0, len(spec.Children))
		for _, child := range spec.Children {
			id, err := d.mintUnplantedSeedID(*schema)
			if err != nil {
				plotErr = err
				return
			}
			ids[garden.StepSlug(child.Title)] = id
			childSeeds = append(childSeeds, garden.Seed{ID: id, Title: child.Title, Body: child.Body})
		}
		docs := make([]docstore.Document, 0, len(spec.Children))
		for i, child := range spec.Children {
			seed := childSeeds[i]
			seed.Edges = []garden.Edge{{Kind: garden.EdgePartOf, To: crown.ID}}
			for _, target := range child.Blocks {
				seed.Edges = append(seed.Edges, garden.Edge{Kind: garden.EdgeBlocks, To: ids[garden.StepSlug(strings.TrimSpace(target))]})
			}
			seed.Status = garden.StatusPlanted
			seed.StepSlug = garden.StepSlug(seed.Title)
			seed.PlanterSession = sessionID
			seed.PlanterMember = member
			seed.Vars = []garden.Var{}
			doc, err := d.plantSeed(*schema, seed)
			if err != nil {
				plotErr = fmt.Errorf(
					"planting %q failed after %s were planted: %w — the plot is partial, `attn seed ls --tree` shows what landed",
					seed.Title, strings.Join(planted, ", "), err)
				return
			}
			planted = append(planted, seed.ID)
			childSeeds[i] = seed
			docs = append(docs, doc)
		}
		ready := d.gardenReady()
		result.Crown = seedToProtocol(crown, crownDoc, ready[crown.ID])
		for i, seed := range childSeeds {
			result.Children = append(result.Children, seedToProtocol(seed, docs[i], ready[seed.ID]))
		}
	})
	if plotErr != nil {
		d.sendGardenError(conn, "plot", plotErr)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedPlotResult: &result})
}

// mintAndPlant gives the seed an id and writes it, minting again when that id
// is already taken. Ids come from crypto/rand rather than a counter, so a
// collision is possible and its own retry: the write is create-only, and the
// alternative — telling the planter its seed was refused because of a coin
// flip — is a refusal nobody can act on. Three tries is a tripwire, not a
// budget: at a garden of ten thousand seeds a single mint collides with
// probability ~1e-5, so three in a row is a broken random source, and the
// refusal says so.
func (d *Daemon) mintAndPlant(schema docstore.CollectionSchema, seed garden.Seed) (garden.Seed, docstore.Document, error) {
	const mintAttempts = 3
	var lastErr error
	for range mintAttempts {
		id, err := d.mintSeedID()
		if err != nil {
			return seed, docstore.Document{}, err
		}
		seed.ID = id
		doc, err := d.plantSeed(schema, seed)
		if err == nil {
			return seed, doc, nil
		}
		if !docstore.IsConflict(err) {
			return seed, docstore.Document{}, err
		}
		lastErr = err
	}
	return seed, docstore.Document{}, fmt.Errorf(
		"minted %d seed ids and every one was already planted, which a working random source does not do: %w",
		mintAttempts, lastErr)
}

func (d *Daemon) mintSeedID() (string, error) {
	if d.gardenMintID != nil {
		return d.gardenMintID()
	}
	return garden.NewID()
}

// mintUnplantedSeedID mints an id no planted seed holds. Plot planting names
// sibling ids in edges before the siblings are written, so a collision has to
// be caught before the id is woven into the plot — the write's create-only
// guard still stands behind this check, and a mid-write race just means the
// planting fails the way any collision does.
func (d *Daemon) mintUnplantedSeedID(schema docstore.CollectionSchema) (string, error) {
	const mintAttempts = 3
	for range mintAttempts {
		id, err := d.mintSeedID()
		if err != nil {
			return "", err
		}
		_, found, err := d.store.GetDocument(schema, id)
		if err != nil {
			return "", err
		}
		if !found {
			return id, nil
		}
	}
	return "", fmt.Errorf("minted %d seed ids and every one was already planted, which a working random source does not do", mintAttempts)
}

func (d *Daemon) handleSeedList(conn net.Conn, msg *protocol.SeedListMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "ls", err)
		return
	}
	read, err := d.readGarden()
	if err != nil {
		d.sendGardenError(conn, "ls", err)
		return
	}
	result := &protocol.SeedListResult{Total: d.countSeeds()}
	seeds := read.seeds
	if protocol.Deref(msg.Stale) {
		window := garden.DefaultStaleWindow
		if s := protocol.Deref(msg.StaleWindowSeconds); s > 0 {
			window = time.Duration(s) * time.Second
		}
		seeds, err = d.staleSeeds(read, window)
		if err != nil {
			d.sendGardenError(conn, "ls", err)
			return
		}
		// Under --stale the honest total is the stale count itself: the garden's
		// size says nothing about how many seeds went quiet.
		result.Total = len(seeds)
		result.StaleWindowSeconds = protocol.Ptr(int(window / time.Second))
	}
	result.Seeds = read.wire(seeds)
	result.StrandedTickets = d.strandedTicketsField()
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedListResult: result})
}

// staleSeeds is the stale query over one read: open seeds whose log — the
// document's own updated stamp or its newest note, whichever is later — has
// not moved within window. The note read runs only for seeds already quiet by
// their document stamp: a note never makes a fresh seed stale.
func (d *Daemon) staleSeeds(read gardenRead, window time.Duration) ([]garden.Seed, error) {
	now := time.Now()
	lastMoved := make(map[string]time.Time, len(read.seeds))
	for _, seed := range read.seeds {
		if garden.Closed(seed.Status) {
			continue
		}
		moved := read.docs[seed.ID].UpdatedAt
		if now.Sub(moved) >= window {
			note, err := d.newestNoteAt(seed.ID)
			if err != nil {
				return nil, err
			}
			if note.After(moved) {
				moved = note
			}
		}
		lastMoved[seed.ID] = moved
	}
	return garden.Stale(read.seeds, lastMoved, window, now), nil
}

// newestNoteAt is when a seed's log last spoke; zero when it never has.
func (d *Daemon) newestNoteAt(seedID string) (time.Time, error) {
	readNotes, _, err := d.runDocQuery(docstore.Query{
		Namespace:  garden.Namespace,
		Collection: garden.CollectionNotes,
		Filters:    []docstore.Filter{{Field: "seed", Op: docstore.OpEq, Value: seedID}},
		Sort:       &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
		Limit:      1,
	})
	if err != nil {
		return time.Time{}, err
	}
	if len(readNotes.Documents) == 0 {
		return time.Time{}, nil
	}
	return readNotes.Documents[0].CreatedAt, nil
}

func (d *Daemon) handleSeedShow(conn net.Conn, msg *protocol.SeedShowMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "show", err)
		return
	}
	seed, doc, err := d.readSeed(msg.SeedID)
	if err != nil {
		d.sendGardenError(conn, "show", err)
		return
	}
	// The log rides the read the tender already runs. A failure to read it must
	// not fail the show — the seed is the answer, the notes are the context.
	notes, total, err := d.readNotes(seed.ID, garden.ShowNotes)
	if err != nil {
		d.logf("garden: reading the log of %s: %v", seed.ID, err)
	}
	read, err := d.readGarden()
	if err != nil {
		d.logf("garden: reading the garden around %s: %v", seed.ID, err)
	}
	wire := seedToProtocol(seed, doc, read.ready[seed.ID])
	if progress, ok := read.progress(seed.ID); ok {
		wire.PlotProgress = progress
	}
	d.sendGardenResponse(conn, protocol.Response{
		Ok: true,
		SeedShowResult: &protocol.SeedShowResult{
			Seed:       wire,
			Notes:      notes,
			NotesTotal: total,
			Relations:  gardenRelations(read, seed.ID),
			Handoff:    d.gardenHandoff(seed.ID),
			Artifacts:  d.seedArtifacts(seed.ID),
		},
	})
}

// seedArtifacts projects a seed's current set from its whole log — attach minus
// detach — for every surface that renders it. It reads the log again rather
// than reusing a bounded one a caller already has: the attach that matters is
// often older than the newest few entries, and a set that quietly shrinks as a
// log gets busy is worse than none.
//
// A read failure is an empty set and a log line, never a refusal: the caller is
// reading a seed, and losing the artifact list is not worth failing that.
func (d *Daemon) seedArtifacts(seedID string) []protocol.SeedArtifactReference {
	notes, err := d.readNotesDomain(seedID)
	if err != nil {
		d.logf("garden: reading the artifacts of %s: %v", seedID, err)
		return []protocol.SeedArtifactReference{}
	}
	current := garden.CurrentArtifacts(notes)
	out := make([]protocol.SeedArtifactReference, 0, len(current))
	for _, artifact := range current {
		out = append(out, *artifactToProtocol(artifact))
	}
	return out
}

func (d *Daemon) handleSeedEdit(conn net.Conn, msg *protocol.SeedEditMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "edit", err)
		return
	}
	if err := garden.ValidateBody(msg.Body); err != nil {
		d.sendGardenError(conn, "edit", err)
		return
	}
	seed, doc, err := d.applySeedBodyEdit(msg.SeedID, msg.Body)
	if err != nil {
		d.sendGardenError(conn, "edit", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{
		Ok:             true,
		SeedEditResult: &protocol.SeedEditResult{Seed: seedToProtocol(seed, doc, d.gardenReady()[seed.ID])},
	})
}

// applySeedBodyEdit changes only the living markdown body. Re-reading on a
// conflict preserves lifecycle, tender and edge changes that landed while the
// editor was writing.
func (d *Daemon) applySeedBodyEdit(id, body string) (garden.Seed, docstore.Document, error) {
	schema, err := d.seedsCollection()
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(id)
		if err != nil {
			return garden.Seed{}, docstore.Document{}, err
		}
		seed.Body = body
		written, err := d.writeSeed(*schema, seed, doc.Rev, FactGardenBodyEdited)
		if err == nil {
			return seed, written, nil
		}
		if !docstore.IsConflict(err) {
			return garden.Seed{}, docstore.Document{}, err
		}
	}
	return garden.Seed{}, docstore.Document{}, fmt.Errorf(
		"%s was rewritten under all %d attempts to edit it; read it again with `attn seed show %s` and retry",
		id, attempts, id)
}

// handleSeedDocumentGet returns the complete reading surface for one seed.
// Garden snapshots carry the live seed itself; clients use this detail read
// for the ledger and as a fallback when a bounded snapshot omits the open seed.
func (d *Daemon) handleSeedDocumentGet(client *wsClient, msg *protocol.SeedDocumentGetMessage) {
	result := protocol.SeedDocumentGetResultMessage{
		Event:     protocol.EventSeedDocumentGetResult,
		RequestID: msg.RequestID,
	}
	fail := func(err error) {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
	}
	if err := d.requireHome(garden.Surface); err != nil {
		fail(err)
		return
	}
	seed, doc, err := d.readSeed(msg.SeedID)
	if err != nil {
		fail(err)
		return
	}
	read, err := d.readGarden()
	if err != nil {
		fail(err)
		return
	}
	children := make([]garden.Seed, 0)
	for _, candidate := range read.seeds {
		for _, edge := range candidate.Edges {
			if edge.Kind == garden.EdgePartOf && edge.To == seed.ID {
				children = append(children, candidate)
				break
			}
		}
	}
	notes, notesTotal, err := d.readNotes(seed.ID, docstore.MaxLimit)
	if err != nil {
		fail(err)
		return
	}
	wireSeed := seedToProtocol(seed, doc, read.ready[seed.ID])
	if progress, ok := read.progress(seed.ID); ok {
		wireSeed.PlotProgress = progress
	}
	result.Document = &protocol.SeedDocument{
		Seed:        wireSeed,
		TenderHolds: seed.Tender().Holds(d.sessionExists),
		Children:    read.wire(children),
		Notes:       notes,
		NotesTotal:  notesTotal,
		Artifacts:   d.seedArtifacts(seed.ID),
	}
	result.Success = true
	d.sendToClient(client, result)
}

// gardenRelations renders both directions of a seed's edges, each with the other
// seed's title and state: "blocked-by s-7k3f9m" is only actionable when the
// reader can see whether that blocker is still open.
func gardenRelations(read gardenRead, id string) []protocol.SeedRelation {
	index := make(map[string]garden.Seed, len(read.seeds))
	for _, seed := range read.seeds {
		index[seed.ID] = seed
	}
	relations := garden.Relations(read.seeds, id)
	out := make([]protocol.SeedRelation, 0, len(relations))
	for _, relation := range relations {
		other := index[relation.Seed]
		out = append(out, protocol.SeedRelation{
			Label:  relation.Label,
			SeedID: relation.Seed,
			Title:  other.Title,
			Status: other.Status,
		})
	}
	return out
}

// handleSeedLink adds or removes one edge. The decision is made against the
// whole garden — a cycle is a property of the graph — and written against the
// revision that decision was read from, so two agents linking at once produce
// one edge and one honest refusal rather than a lost write.
func (d *Daemon) handleSeedLink(conn net.Conn, msg *protocol.SeedLinkMessage) {
	verb := "link"
	if protocol.Deref(msg.Unlink) {
		verb = "unlink"
	}
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, verb, err)
		return
	}
	kind, err := garden.ParseEdgeKind(msg.Kind)
	if err != nil {
		d.sendGardenError(conn, verb, err)
		return
	}
	from := strings.TrimSpace(msg.SeedID)
	to := strings.TrimSpace(msg.ToSeedID)
	for _, id := range []string{from, to} {
		if err := garden.ValidateID(id); err != nil {
			d.sendGardenError(conn, verb, err)
			return
		}
	}
	schema, err := d.seedsCollection()
	if err != nil {
		d.sendGardenError(conn, verb, err)
		return
	}

	const attempts = 3
	for range attempts {
		read, err := d.readGarden()
		if err != nil {
			d.sendGardenError(conn, verb, err)
			return
		}
		var next garden.Seed
		changed := true
		if verb == "unlink" {
			next, err = garden.Unlink(read.seeds, from, kind, to)
		} else {
			next, changed, err = garden.Link(read.seeds, from, kind, to)
		}
		if err != nil {
			d.sendGardenError(conn, verb, err)
			return
		}
		if !changed {
			d.sendGardenResponse(conn, protocol.Response{
				Ok: true,
				SeedLinkResult: &protocol.SeedLinkResult{
					Seed: seedToProtocol(next, read.docs[next.ID], read.ready[next.ID]), Changed: false,
				},
			})
			return
		}
		fact := FactGardenLinked
		if verb == "unlink" {
			fact = FactGardenUnlinked
		}
		doc, err := d.writeSeed(*schema, next, read.docs[next.ID].Rev, fact)
		if err != nil {
			if docstore.IsConflict(err) {
				continue
			}
			d.sendGardenError(conn, verb, err)
			return
		}
		d.sendGardenResponse(conn, protocol.Response{
			Ok: true,
			SeedLinkResult: &protocol.SeedLinkResult{
				Seed: seedToProtocol(next, doc, d.gardenReady()[next.ID]), Changed: true,
			},
		})
		return
	}
	d.sendGardenError(conn, verb, fmt.Errorf(
		"%s was rewritten under all %d attempts to %s it; read it again with `attn seed show %s` and decide from what it says now",
		from, attempts, verb, from))
}

// handleSeedReady answers what can be tended now. The scope is inferred from
// the caller — the daemon owns the session, so the flag-free form is the common
// one: the whole garden, unless the calling session was dispatched at a crown,
// and then that plot. The inference is scope, not a fence: --all steps a
// dispatched session out to the garden, --plot into any other plot.
func (d *Daemon) handleSeedReady(conn net.Conn, msg *protocol.SeedReadyMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "ready", err)
		return
	}
	crown := strings.TrimSpace(protocol.Deref(msg.Plot))
	if crown != "" {
		if err := garden.ValidateID(crown); err != nil {
			d.sendGardenError(conn, "ready", err)
			return
		}
		if _, _, err := d.readSeed(crown); err != nil {
			d.sendGardenError(conn, "ready", err)
			return
		}
	}
	if crown == "" && !protocol.Deref(msg.All) {
		// The dispatch inference. A crown that has since left the garden infers
		// nothing — the answer falls back to the whole garden rather than refusing
		// a caller who asked with no flags at all.
		if at, ok := d.gardenDispatchCrown(strings.TrimSpace(protocol.Deref(msg.SourceSessionID))); ok {
			if _, _, err := d.readSeed(at); err == nil {
				crown = at
			}
		}
	}
	read, err := d.readGarden()
	if err != nil {
		d.sendGardenError(conn, "ready", err)
		return
	}
	ready := make([]garden.Seed, 0, len(read.ready))
	for _, seed := range read.seeds {
		if read.ready[seed.ID] {
			ready = append(ready, seed)
		}
	}

	result := &protocol.SeedReadyResult{Scope: "garden"}
	if crown != "" {
		// The plot is walked over the whole garden, not over the ready seeds: a
		// crown is never ready itself, so walking the ready set would lose every
		// child whose parent it holds.
		inPlot := map[string]bool{}
		for _, seed := range garden.InPlot(read.seeds, crown) {
			inPlot[seed.ID] = true
		}
		scoped := make([]garden.Seed, 0, len(ready))
		for _, seed := range ready {
			if inPlot[seed.ID] {
				scoped = append(scoped, seed)
			}
		}
		ready, result.Scope, result.ScopeID = scoped, "plot", crown
		if crownSeed, crownDoc, err := d.readSeed(crown); err == nil {
			wire := seedToProtocol(crownSeed, crownDoc, read.ready[crown])
			if progress, ok := read.progress(crown); ok {
				wire.PlotProgress = progress
			}
			result.Crown = &wire
		}
	}
	// Oldest first, against the newest-first order every other read uses: this is
	// a work queue, and the seed that has waited longest is the one to hand over.
	slices.Reverse(ready)
	result.Seeds = read.wire(ready)
	// The freshest handoff on each ready seed, in the seeds' own order: what a
	// launching delegate reads before any work. Carried on the plot scope alone —
	// a garden-wide answer is a listing, not a pickup.
	if result.Scope == "plot" {
		for _, seed := range ready {
			if handoff := d.gardenHandoff(seed.ID); handoff != nil {
				result.Handoffs = append(result.Handoffs, *handoff)
			}
		}
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedReadyResult: result})
}

// The dispatch record: a session dispatched at a crown, written by delegation
// before the runtime spawns so the session's first flag-free `ready` already
// answers from its plot. Scope inference, nothing more — no surface renders it
// and nothing enforces it.

func (d *Daemon) dispatchesCollection() (*docstore.CollectionSchema, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	return d.collectionFor(garden.Namespace, garden.CollectionDispatches)
}

// recordGardenDispatch stamps a session as dispatched at a seed, with the
// directory and agent a resume would relaunch it from. Last write wins on
// purpose: a session reports to one seed, and re-dispatching a recovered
// session at a new crown is a re-aim, not a conflict.
func (d *Daemon) recordGardenDispatch(sessionID, crown, cwd, agent string, fromChief bool) error {
	schema, err := d.dispatchesCollection()
	if err != nil {
		return err
	}
	body, err := garden.Dispatch{
		SessionID: sessionID, Crown: crown, Cwd: cwd, Agent: agent, FromChief: fromChief,
	}.Encode()
	if err != nil {
		return err
	}
	fact := documentChangedFact(garden.Namespace, garden.CollectionDispatches, sessionID, false)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: *schema, ID: sessionID, Body: body,
	}, fact, time.Now())
	if err != nil {
		return err
	}
	d.announceCommittedWrite(fact, written.Seq)
	d.rememberDispatchSeed(sessionID, crown)
	d.rememberDispatchFromChief(sessionID, fromChief)
	return nil
}

// rememberDispatchResume records the agent-native conversation id on the
// dispatch record, so reopening a seed's tender after its session row is gone
// resumes the conversation rather than opening the agent's picker. The ticket
// row used to hold this copy; a delegation binds no ticket any more.
//
// It is best-effort and silent when the session has no dispatch record: not
// every session reports to a seed, and a resume id is not worth failing the
// transition that produced it.
func (d *Daemon) rememberDispatchResume(sessionID, resumeSessionID string) {
	sessionID, resumeSessionID = strings.TrimSpace(sessionID), strings.TrimSpace(resumeSessionID)
	if sessionID == "" || resumeSessionID == "" {
		return
	}
	dispatch, ok := d.gardenDispatch(sessionID)
	if !ok || dispatch.Resume == resumeSessionID {
		return
	}
	schema, err := d.dispatchesCollection()
	if err != nil {
		return
	}
	dispatch.Resume = resumeSessionID
	body, err := dispatch.Encode()
	if err != nil {
		d.logf("garden: encoding the dispatch record for %s: %v", sessionID, err)
		return
	}
	fact := documentChangedFact(garden.Namespace, garden.CollectionDispatches, sessionID, false)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: *schema, ID: sessionID, Body: body,
	}, fact, time.Now())
	if err != nil {
		d.logf("garden: recording the resume id for session %s: %v", sessionID, err)
		return
	}
	d.announceCommittedWrite(fact, written.Seq)
}

// gardenDispatchResume is the conversation id to reopen a seed's tender on.
func (d *Daemon) gardenDispatchResume(sessionID string) string {
	dispatch, ok := d.gardenDispatch(sessionID)
	if !ok {
		return ""
	}
	return strings.TrimSpace(dispatch.Resume)
}

// validateDispatchCrown refuses a dispatch aimed at a seed that is not here.
// It is deliberately not a check that the seed already has children: a crown
// planted for a plot that is about to be filled is the normal way to start one.
func (d *Daemon) validateDispatchCrown(crown string) error {
	if crown == "" {
		return nil
	}
	if err := d.requireHome(garden.Surface); err != nil {
		return fmt.Errorf("dispatch at %s: %w", crown, err)
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return err
	}
	if _, found, err := d.store.GetDocument(*schema, crown); err != nil {
		return err
	} else if !found {
		return fmt.Errorf("no seed %s to dispatch at", crown)
	}
	return nil
}

// gardenDispatch is a session's whole dispatch record, if it has one.
func (d *Daemon) gardenDispatch(sessionID string) (garden.Dispatch, bool) {
	if sessionID == "" || d.store == nil {
		return garden.Dispatch{}, false
	}
	schema, err := d.dispatchesCollection()
	if err != nil {
		return garden.Dispatch{}, false
	}
	doc, found, err := d.store.GetDocument(*schema, sessionID)
	if err != nil || !found {
		return garden.Dispatch{}, false
	}
	dispatch, err := garden.DecodeDispatch(doc.Body)
	if err != nil {
		d.logf("garden: dispatch record for %s has an unreadable body: %v", sessionID, err)
		return garden.Dispatch{}, false
	}
	return dispatch, true
}

// gardenDispatchCrown is the seed a session reports to, if any.
func (d *Daemon) gardenDispatchCrown(sessionID string) (string, bool) {
	dispatch, ok := d.gardenDispatch(sessionID)
	if !ok {
		return "", false
	}
	return dispatch.Crown, dispatch.Crown != ""
}

// gardenDispatchSeedsBySession maps every session that reports to a seed. A
// session list is broadcast on every state change, so this must cost nothing:
// the map is read from the collection once and then written through on each
// dispatch, rather than scanning a collection that grows with every delegation
// attn has ever made.
//
// Empty — never an error — when the collection is absent or unreadable:
// decoration must not fail a broadcast.
func (d *Daemon) gardenDispatchSeedsBySession() map[string]string {
	if d.store == nil {
		return nil
	}
	d.dispatchSeedsMu.Lock()
	defer d.dispatchSeedsMu.Unlock()
	if !d.dispatchSeedsLoaded {
		read, _, err := d.runDocQuery(docstore.Query{
			Namespace:  garden.Namespace,
			Collection: garden.CollectionDispatches,
		})
		if err != nil {
			if !docstore.IsUndeclaredCollection(err) {
				d.logf("garden: reading dispatch records for broadcast: %v", err)
				return nil
			}
			// No garden here: remember that, so a daemon nobody plants in stops
			// asking. Declaring the collections clears this.
			d.dispatchSeeds, d.dispatchFromChief, d.dispatchSeedsLoaded = nil, nil, true
			return nil
		}
		loaded := make(map[string]string, len(read.Documents))
		fromChief := map[string]bool{}
		for _, doc := range read.Documents {
			dispatch, err := garden.DecodeDispatch(doc.Body)
			if err != nil || dispatch.Crown == "" {
				continue
			}
			loaded[doc.ID] = dispatch.Crown
			if dispatch.FromChief {
				fromChief[doc.ID] = true
			}
		}
		d.dispatchSeeds, d.dispatchFromChief = loaded, fromChief
		d.dispatchSeedsLoaded = true
	}
	return d.dispatchSeeds
}

// rememberDispatchSeed writes one dispatch through to the broadcast map. Called
// after the record is committed, so a reader never sees a binding the database
// does not hold.
func (d *Daemon) rememberDispatchSeed(sessionID, crown string) {
	d.dispatchSeedsMu.Lock()
	defer d.dispatchSeedsMu.Unlock()
	if !d.dispatchSeedsLoaded {
		return // Not loaded yet; the first read builds it from the collection.
	}
	if crown == "" {
		delete(d.dispatchSeeds, sessionID)
		return
	}
	// Copied on write: a broadcast holds the map it was handed while it renders.
	next := make(map[string]string, len(d.dispatchSeeds)+1)
	for id, seed := range d.dispatchSeeds {
		next[id] = seed
	}
	next[sessionID] = crown
	d.dispatchSeeds = next
}

// rememberDispatchFromChief writes the chief-dispatched bit through to the same
// broadcast map, on the same copy-on-write rule.
func (d *Daemon) rememberDispatchFromChief(sessionID string, fromChief bool) {
	d.dispatchSeedsMu.Lock()
	defer d.dispatchSeedsMu.Unlock()
	if !d.dispatchSeedsLoaded {
		return
	}
	next := make(map[string]bool, len(d.dispatchFromChief)+1)
	for id := range d.dispatchFromChief {
		next[id] = true
	}
	if fromChief {
		next[sessionID] = true
	} else {
		delete(next, sessionID)
	}
	d.dispatchFromChief = next
}

// gardenDispatchesFromChief is the set of sessions the chief of staff
// dispatched. It shares the dispatch cache's load, so asking costs nothing on a
// broadcast.
func (d *Daemon) gardenDispatchesFromChief() map[string]bool {
	d.gardenDispatchSeedsBySession()
	d.dispatchSeedsMu.Lock()
	defer d.dispatchSeedsMu.Unlock()
	return d.dispatchFromChief
}

// decorateSessionSeed names the seed a session reports to. Mirrors
// decorateCrewMember: set only when the session has a dispatch record, cleared
// otherwise so it round-trips as an omitted field.
func (d *Daemon) decorateSessionSeed(session *protocol.Session, seedBySession map[string]string) {
	if session == nil {
		return
	}
	if seed := seedBySession[session.ID]; seed != "" {
		session.SeedID = protocol.Ptr(seed)
		return
	}
	session.SeedID = nil
}

// gardenPrime is what a launching session is primed with: the same answer its
// own flag-free `attn seed ready` gives — the whole garden's count, or its
// plot when the session was dispatched at a crown — so guidance and the CLI
// cannot disagree.
func (d *Daemon) gardenPrime(sessionID string) (*hooks.GardenPrime, error) {
	if err := d.requireHome(garden.Surface); err != nil {
		return nil, err
	}
	read, err := d.readGarden()
	if err != nil {
		return nil, err
	}
	prime := &hooks.GardenPrime{}
	for _, seed := range read.seeds {
		if read.ready[seed.ID] {
			prime.Ready++
		}
	}
	crown, ok := d.gardenDispatchCrown(sessionID)
	if !ok {
		return prime, nil
	}
	crownSeed, _, err := d.readSeed(crown)
	if err != nil {
		// A dispatched-at crown that has since left the garden primes the garden,
		// exactly as flag-free `ready` would answer.
		return prime, nil
	}
	plot := &hooks.CrownPrime{ID: crownSeed.ID, Title: crownSeed.Title, Body: crownSeed.Body}
	inPlot := map[string]bool{}
	for _, seed := range garden.InPlot(read.seeds, crown) {
		inPlot[seed.ID] = true
	}
	// Oldest first, like `ready`: the seed that has waited longest leads.
	for i := len(read.seeds) - 1; i >= 0; i-- {
		seed := read.seeds[i]
		if !read.ready[seed.ID] || !inPlot[seed.ID] {
			continue
		}
		line := hooks.SeedPrime{ID: seed.ID, Title: seed.Title}
		if handoff := d.gardenHandoff(seed.ID); handoff != nil {
			line.Handoff = handoff.Body
			line.HandoffAuthor = crew.HolderName(handoff.AuthorMember, handoff.AuthorSession)
		}
		plot.ReadySeeds = append(plot.ReadySeeds, line)
	}
	prime.Ready = len(plot.ReadySeeds)
	prime.Crown = plot
	return prime, nil
}

// gardenFacts is the verb-to-fact table. One fact per transition, each naming
// its seed, so a future sync engine is a durable consumer with a cursor and
// nothing else.
var gardenFacts = map[garden.Verb]string{
	garden.VerbTend:    FactGardenTended,
	garden.VerbPark:    FactGardenParked,
	garden.VerbHarvest: FactGardenHarvested,
	garden.VerbWither:  FactGardenWithered,
	garden.VerbReplant: FactGardenReplanted,
}

func (d *Daemon) handleSeedTransition(conn net.Conn, msg *protocol.SeedTransitionMessage) {
	verb, err := garden.ParseVerb(msg.Verb)
	if err != nil {
		d.sendGardenError(conn, strings.TrimSpace(msg.Verb), err)
		return
	}
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, string(verb), err)
		return
	}
	sessionID := strings.TrimSpace(protocol.Deref(msg.SourceSessionID))
	actor := garden.Tender{
		Session: sessionID,
		// A registered member's free-string name becomes its registry id, so
		// Tender.Is compares real addresses; an unregistered name passes through
		// and keeps tending exactly as before.
		Member: d.resolveTenderMember(protocol.Deref(msg.Member), sessionID),
	}
	seed, doc, err := d.applySeedTransition(msg.SeedID, verb, actor, protocol.Deref(msg.Reason))
	if err != nil {
		d.sendGardenError(conn, string(verb), err)
		return
	}
	result := &protocol.SeedTransitionResult{Seed: seedToProtocol(seed, doc, false)}
	// One read answers both readiness and plot progress. Progress rides on the
	// moved seed so the caller can see what its move left behind — the CLI warns
	// on a close that strands open children — and a failed read leaves both
	// empty rather than failing a move that committed.
	if read, err := d.readGarden(); err == nil {
		result.Seed.Ready = read.ready[seed.ID]
		if progress, ok := read.progress(seed.ID); ok {
			result.Seed.PlotProgress = progress
		}
	}
	// Tending is the pickup, so it is the move that primes: whoever just claimed
	// the seed reads what the last tender wrote to them before doing any work.
	if verb == garden.VerbTend {
		result.Handoff = d.gardenHandoff(seed.ID)
	}
	// Mirrored before the response: the move is not fully recorded until every
	// place that still tracks this work has it, and a caller that harvests and
	// then reads the board must not see the ticket mid-flight.
	d.mirrorSeedMoveOntoTicket(sessionID, seed.ID, verb, protocol.Deref(msg.Reason))
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedTransitionResult: result})
}

// applySeedTransition is the atomic claim, and the same read-decide-write for
// every other move: read the seed, ask the garden whether the move is legal,
// and write against the revision that was read.
//
// A conflict is not a refusal — it means the seed moved between the read and
// the write, so the decision was made against a version that no longer exists.
// Re-reading is what turns a lost race into the honest answer: the second
// session to tend finds a tender there and gets told whose it is. Three
// attempts is a tripwire — two agents contending is one retry, and a seed
// rewritten three times inside one call is something else entirely.
func (d *Daemon) applySeedTransition(id string, verb garden.Verb, actor garden.Tender, reason string) (garden.Seed, docstore.Document, error) {
	schema, err := d.seedsCollection()
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	fact, ok := gardenFacts[verb]
	if !ok {
		return garden.Seed{}, docstore.Document{}, fmt.Errorf("no bus fact is declared for %q", verb)
	}
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(id)
		if err != nil {
			return garden.Seed{}, docstore.Document{}, err
		}
		next, err := garden.Transition(seed, verb, actor, reason, d.sessionExists)
		if err != nil {
			return garden.Seed{}, docstore.Document{}, err
		}
		written, err := d.writeSeed(*schema, next, doc.Rev, fact)
		if err == nil {
			return next, written, nil
		}
		if !docstore.IsConflict(err) {
			return garden.Seed{}, docstore.Document{}, err
		}
	}
	return garden.Seed{}, docstore.Document{}, fmt.Errorf(
		"%s was rewritten under all %d attempts to %s it; read it again with `attn seed show %s` and decide from what it says now",
		id, attempts, verb, id)
}

// Notes. The log is its own collection keyed by seed, so a long-tended seed
// never bloats its own document, and a note is append-only: nothing edits or
// deletes one, because the log is what happened.

func (d *Daemon) notesCollection() (*docstore.CollectionSchema, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	return d.collectionFor(garden.Namespace, garden.CollectionNotes)
}

func (d *Daemon) handleSeedNote(conn net.Conn, msg *protocol.SeedNoteMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "note", err)
		return
	}
	authorSession := strings.TrimSpace(protocol.Deref(msg.SourceSessionID))
	note, err := d.appendSeedNote(
		msg.SeedID,
		msg.Body,
		authorSession,
		protocol.Deref(msg.Member),
		protocol.Deref(msg.Kind),
		artifactFromProtocol(msg.Artifact),
	)
	if err != nil {
		d.sendGardenError(conn, "note", err)
		return
	}
	// Mirrored before the response, for the reason handleSeedTransition states.
	d.mirrorSeedNoteOntoTicket(authorSession, msg.SeedID, note.Body)
	d.sendGardenResponse(conn, protocol.Response{
		Ok:             true,
		SeedNoteResult: &protocol.SeedNoteResult{Note: note},
	})
}

// resolveNoteArtifact pairs a kind with its reference and hands back both as
// they should be stored. The two attach kinds require one and every other kind
// refuses one: a reference on a plain note is invisible to the projection, and
// an attach without one associates nothing — both would store something no
// surface can act on.
//
// An attach or detach with no words of its own gets a body rendered from the
// typed reference, so the log reads as prose either way.
func resolveNoteArtifact(kind string, artifact *garden.ArtifactReference, body string) (*garden.ArtifactReference, string, error) {
	if !garden.CarriesArtifact(kind) {
		if artifact != nil {
			return nil, "", fmt.Errorf(
				"a %s note carries no artifact; `attn seed attach` and `attn seed detach` are what associate a document", kind)
		}
		return nil, body, nil
	}
	if artifact == nil {
		return nil, "", fmt.Errorf("a %s note needs the artifact it %ses; the kinds are %s",
			kind, kind, strings.Join(garden.ArtifactKinds, ", "))
	}
	validated, err := garden.ValidateArtifact(*artifact)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(body) == "" {
		body = garden.DefaultNoteBody(kind, validated)
	}
	return &validated, body, nil
}

// appendSeedNote is the one log-write path shared by the CLI and the seed
// annotation destination. It validates the typed seed before minting so no
// note can land in a log nobody can read.
func (d *Daemon) appendSeedNote(seedID, body, authorSession, member, kindName string, artifact *garden.ArtifactReference) (protocol.SeedNote, error) {
	kind, err := garden.ParseNoteKind(kindName)
	if err != nil {
		return protocol.SeedNote{}, err
	}
	artifact, body, err = resolveNoteArtifact(kind, artifact, body)
	if err != nil {
		return protocol.SeedNote{}, err
	}
	if err := garden.ValidateNote(body); err != nil {
		return protocol.SeedNote{}, err
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return protocol.SeedNote{}, err
	}
	schema, err := d.notesCollection()
	if err != nil {
		return protocol.SeedNote{}, err
	}
	authorSession = strings.TrimSpace(authorSession)
	note := garden.Note{
		Seed:          seed.ID,
		Kind:          kind,
		Body:          body,
		AuthorSession: authorSession,
		AuthorMember:  d.resolveTenderMember(member, authorSession),
		Artifact:      artifact,
	}
	written, doc, err := d.mintAndWriteNote(*schema, note)
	if err != nil {
		return protocol.SeedNote{}, err
	}
	return noteToProtocol(written, doc), nil
}

// mintAndWriteNote writes one log entry, minting again on a taken id for the
// same reason planting does: the author did nothing wrong and has nothing to fix.
func (d *Daemon) mintAndWriteNote(schema docstore.CollectionSchema, note garden.Note) (garden.Note, docstore.Document, error) {
	const mintAttempts = 3
	var lastErr error
	for range mintAttempts {
		id, err := d.mintNoteID()
		if err != nil {
			return note, docstore.Document{}, err
		}
		note.ID = id
		body, err := note.Encode()
		if err != nil {
			return note, docstore.Document{}, err
		}
		expected := docstore.ExpectAbsent
		fact := documentChangedFact(garden.Namespace, garden.CollectionNotes, note.ID, false)
		written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
			Schema: schema, ID: note.ID, Body: body, Expected: &expected,
		}, fact, time.Now())
		if err != nil {
			if !docstore.IsConflict(err) {
				return note, docstore.Document{}, err
			}
			lastErr = err
			continue
		}
		d.announceCommittedWrite(fact, written.Seq)
		// Subject is the seed, not the note: every garden fact names the seed it
		// is about, which is what a change feed reads.
		d.publishFact(FactGardenNoted, note.Seed, nil)

		doc, found, err := d.store.GetDocument(schema, note.ID)
		if err != nil || !found {
			return note, docstore.Document{ID: note.ID, Body: body, Rev: written.Rev}, nil
		}
		return note, *doc, nil
	}
	return note, docstore.Document{}, fmt.Errorf(
		"minted %d note ids and every one was taken, which a working random source does not do: %w", mintAttempts, lastErr)
}

func (d *Daemon) mintNoteID() (string, error) {
	if d.gardenMintNoteID != nil {
		return d.gardenMintNoteID()
	}
	return garden.NewNoteID()
}

func (d *Daemon) handleSeedNotes(conn net.Conn, msg *protocol.SeedNotesMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "notes", err)
		return
	}
	seed, _, err := d.readSeed(msg.SeedID)
	if err != nil {
		d.sendGardenError(conn, "notes", err)
		return
	}
	limit := gardenSnapshotLimit
	if msg.Limit != nil && *msg.Limit > 0 {
		limit = *msg.Limit
	}
	notes, total, err := d.readNotes(seed.ID, limit)
	if err != nil {
		d.sendGardenError(conn, "notes", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{
		Ok:              true,
		SeedNotesResult: &protocol.SeedNotesResult{Notes: notes, Total: total},
	})
}

// readNotes reads a seed's log newest first, bounded, and returns how many it
// holds beside it. The total is what keeps a bounded list from reading as the
// whole log.
func (d *Daemon) readNotes(seedID string, limit int) ([]protocol.SeedNote, int, error) {
	if d.store == nil {
		return nil, 0, errors.New("no database")
	}
	filter := docstore.Filter{Field: "seed", Op: docstore.OpEq, Value: seedID}
	read, _, err := d.runDocQuery(docstore.Query{
		Namespace:  garden.Namespace,
		Collection: garden.CollectionNotes,
		Filters:    []docstore.Filter{filter},
		Sort:       &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
		Limit:      limit,
	})
	if err != nil {
		return nil, 0, err
	}
	notes := make([]protocol.SeedNote, 0, len(read.Documents))
	for _, doc := range read.Documents {
		note, err := garden.DecodeNote(doc.Body)
		if err != nil {
			d.logf("garden: note %s has an unreadable body: %v", doc.ID, err)
			continue
		}
		notes = append(notes, noteToProtocol(note, doc))
	}
	total := len(notes)
	counted, found, err := d.store.CountQuery(docstore.Query{
		Namespace: garden.Namespace, Collection: garden.CollectionNotes,
		Filters: []docstore.Filter{filter},
	})
	if err == nil && found {
		total = counted.Count
	}
	return notes, total, nil
}

// readNotesDomain reads a seed's whole log as domain notes. The artifact
// projection needs the typed reference, which the wire shape carries as
// pointers; decoding once here keeps that conversion out of it.
//
// Whole means whole: the read pages to the end of the log rather than stopping
// at one query's limit. A set that quietly shrinks as a log gets busy is worse
// than none, and a seed is the durable record of a delegation that may run for
// months.
func (d *Daemon) readNotesDomain(seedID string) ([]garden.Note, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	page := d.gardenNotePageSize
	if page <= 0 {
		page = docstore.MaxLimit
	}
	notes := []garden.Note{}
	after := ""
	for {
		read, _, err := d.runDocQuery(docstore.Query{
			Namespace:  garden.Namespace,
			Collection: garden.CollectionNotes,
			Filters:    []docstore.Filter{{Field: "seed", Op: docstore.OpEq, Value: seedID}},
			Sort:       &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
			Limit:      page,
			After:      after,
		})
		if err != nil {
			return nil, err
		}
		for _, doc := range read.Documents {
			note, err := garden.DecodeNote(doc.Body)
			if err != nil {
				d.logf("garden: note %s has an unreadable body: %v", doc.ID, err)
				continue
			}
			notes = append(notes, note)
		}
		if len(read.Documents) < page {
			return notes, nil
		}
		after = read.Documents[len(read.Documents)-1].ID
	}
}

// freshestHandoff reads the one handoff a tender must see: the newest note of
// kind `handoff` on this seed. It is its own query rather than a scan of the
// notes `show` already read, because a handoff older than the newest few log
// entries is exactly the one that would fall out of that window — and a
// continuity surface that goes quiet once the log gets busy is worse than
// none.
//
// A read failure is not a refusal. The caller is claiming or reading a seed;
// losing the handoff is worth a log line, never a failed tend.
func (d *Daemon) freshestHandoff(seedID string) (*protocol.SeedNote, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	read, _, err := d.runDocQuery(docstore.Query{
		Namespace:  garden.Namespace,
		Collection: garden.CollectionNotes,
		Filters: []docstore.Filter{
			{Field: "seed", Op: docstore.OpEq, Value: seedID},
			{Field: "kind", Op: docstore.OpEq, Value: garden.NoteKindHandoff},
		},
		Sort:  &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
		Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(read.Documents) == 0 {
		return nil, nil
	}
	doc := read.Documents[0]
	note, err := garden.DecodeNote(doc.Body)
	if err != nil {
		return nil, fmt.Errorf("note %s has an unreadable body: %w", doc.ID, err)
	}
	wire := noteToProtocol(note, doc)
	return &wire, nil
}

// gardenHandoff is freshestHandoff for the two surfaces that render it, with the
// logging their contract wants: they answer with the seed either way.
func (d *Daemon) gardenHandoff(seedID string) *protocol.SeedNote {
	handoff, err := d.freshestHandoff(seedID)
	if err != nil {
		d.logf("garden: reading the freshest handoff on %s: %v", seedID, err)
		return nil
	}
	return handoff
}

func noteToProtocol(note garden.Note, doc docstore.Document) protocol.SeedNote {
	wire := protocol.SeedNote{
		ID:            note.ID,
		SeedID:        note.Seed,
		Kind:          note.Kind,
		Body:          note.Body,
		AuthorSession: note.AuthorSession,
		AuthorMember:  note.AuthorMember,
		CreatedAt:     doc.CreatedAt.UTC().Format(time.RFC3339),
	}
	if note.Artifact != nil {
		wire.Artifact = artifactToProtocol(*note.Artifact)
	}
	return wire
}

func artifactToProtocol(a garden.ArtifactReference) *protocol.SeedArtifactReference {
	wire := &protocol.SeedArtifactReference{Kind: a.Kind}
	if a.NotebookDocumentID != "" {
		wire.NotebookDocumentID = protocol.Ptr(a.NotebookDocumentID)
	}
	if a.Repository != "" {
		wire.Repository = protocol.Ptr(a.Repository)
	}
	if a.Path != "" {
		wire.Path = protocol.Ptr(a.Path)
	}
	if a.URL != "" {
		wire.URL = protocol.Ptr(a.URL)
	}
	return wire
}

// artifactFromProtocol reads a wire reference. It trims and validates nothing —
// garden.ValidateArtifact does both, in one place, so the CLI and the app are
// refused in the same words.
func artifactFromProtocol(wire *protocol.SeedArtifactReference) *garden.ArtifactReference {
	if wire == nil {
		return nil
	}
	return &garden.ArtifactReference{
		Kind:               wire.Kind,
		NotebookDocumentID: protocol.Deref(wire.NotebookDocumentID),
		Repository:         protocol.Deref(wire.Repository),
		Path:               protocol.Deref(wire.Path),
		URL:                protocol.Deref(wire.URL),
	}
}

func (d *Daemon) sendGardenResponse(conn net.Conn, resp protocol.Response) {
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		d.logf("garden: writing response: %v", err)
	}
}
