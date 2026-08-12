package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The garden's daemon half: the collections it lives in, the three commands
// agents plant and read with, and the snapshot the app's panel renders.
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
	for _, schema := range []docstore.CollectionSchema{garden.SeedsSchema(), garden.NotesSchema()} {
		redeclared, err := d.store.DefineDocumentCollection(schema, time.Now())
		if err != nil {
			d.logf("garden: declaring %s/%s: %v", schema.Namespace, schema.Collection, err)
			continue
		}
		if redeclared {
			d.publishCollectionRedeclared(schema.Namespace, schema.Collection)
		}
	}
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
		return garden.Seed{}, docstore.Document{}, fmt.Errorf("no seed %s is planted here; `attn seed ls --all` lists the garden", id)
	}
	seed, err := garden.Decode(doc.Body)
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	return seed, *doc, nil
}

// querySeeds reads the garden, newest first. An empty workspaceID with scoped
// true means "the seeds that belong to no workspace", which is a real answer and
// not the same as the whole garden.
func (d *Daemon) querySeeds(workspaceID string, scoped bool, limit int) ([]protocol.Seed, error) {
	q := docstore.Query{
		Namespace:  garden.Namespace,
		Collection: garden.CollectionSeeds,
		Sort:       &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
		Limit:      limit,
	}
	if scoped {
		q.Filters = append(q.Filters, docstore.Filter{
			Field: "workspace_id", Op: docstore.OpEq, Value: workspaceID,
		})
	}
	read, _, err := d.runDocQuery(q)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.Seed, 0, len(read.Documents))
	for _, doc := range read.Documents {
		seed, err := garden.Decode(doc.Body)
		if err != nil {
			// One unreadable body must not blank the panel; name it and go on.
			d.logf("garden: seed %s has an unreadable body: %v", doc.ID, err)
			continue
		}
		out = append(out, seedToProtocol(seed, doc))
	}
	return out, nil
}

// countSeeds is what makes a truncated push honest: the panel shows a bounded
// list and says how many the garden holds.
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

func seedToProtocol(seed garden.Seed, doc docstore.Document) protocol.Seed {
	out := protocol.Seed{
		ID:             seed.ID,
		Title:          seed.Title,
		Body:           seed.Body,
		Status:         seed.Status,
		StepSlug:       seed.StepSlug,
		WorkspaceID:    seed.WorkspaceID,
		PlanterSession: seed.PlanterSession,
		PlanterMember:  seed.PlanterMember,
		TenderSession:  seed.TenderSession,
		TenderMember:   seed.TenderMember,
		Edges:          make([]protocol.SeedEdge, 0, len(seed.Edges)),
		Template:       seed.Template,
		Gate:           seed.Gate,
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
	seeds, err := d.querySeeds("", false, gardenSnapshotLimit)
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("garden: reading seeds for broadcast: %v", err)
		}
		return nil
	}
	return seeds
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
		total := d.countSeeds()
		if total > len(seeds) {
			d.logf("garden: %d seeds, pushing the newest %d (limit %d); the panel reports the rest",
				total, len(seeds), gardenSnapshotLimit)
		}
		// GardenSeedsUpdatedMessage is its own top-level event, so the wsHub's
		// WebSocketEvent-only broadcastListener cannot see it; tests use this hook.
		if d.gardenBroadcastHook != nil {
			d.gardenBroadcastHook(seeds)
		}
		if d.wsHub == nil {
			return
		}
		d.broadcastMessage(&protocol.GardenSeedsUpdatedMessage{
			Event: protocol.EventGardenSeedsUpdated,
			Seeds: seeds,
			Total: total,
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
	workspaceID, err := d.gardenWorkspaceFor(sessionID, msg.WorkspaceID)
	if err != nil {
		d.sendGardenError(conn, "plant", err)
		return
	}

	seed := garden.Seed{
		Title:          title,
		Body:           body,
		Status:         garden.StatusPlanted,
		StepSlug:       garden.StepSlug(title),
		WorkspaceID:    workspaceID,
		PlanterSession: sessionID,
		PlanterMember:  strings.TrimSpace(protocol.Deref(msg.Member)),
		Edges:          []garden.Edge{},
		Vars:           []garden.Var{},
	}
	seed, doc, err := d.mintAndPlant(*schema, seed)
	if err != nil {
		d.sendGardenError(conn, "plant", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{
		Ok:              true,
		SeedPlantResult: &protocol.SeedPlantResult{Seed: seedToProtocol(seed, doc)},
	})
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

// gardenWorkspaceFor resolves the workspace a planting is stamped with:
// --workspace if given, otherwise the calling session's. A session attn does not
// know, or one in no workspace, plants a seed with no workspace — which is a
// real state, listed under --all.
func (d *Daemon) gardenWorkspaceFor(sessionID string, override *string) (string, error) {
	if override != nil {
		id := strings.TrimSpace(*override)
		if id == "" {
			return "", fmt.Errorf("--workspace was given with no value; omit it to take the workspace of the session you are in")
		}
		return id, nil
	}
	if sessionID == "" {
		return "", nil
	}
	// decoratedSession, not store.Get: a session's workspace is decorated at
	// broadcast time from the live registry, and the persisted column only leads
	// during startup. Reading the record directly stamps every seed with an empty
	// workspace and the panel scopes them all away.
	session := d.decoratedSession(sessionID)
	if session == nil {
		return "", nil
	}
	return strings.TrimSpace(session.WorkspaceID), nil
}

func (d *Daemon) handleSeedList(conn net.Conn, msg *protocol.SeedListMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "ls", err)
		return
	}
	all := msg.All != nil && *msg.All
	workspaceID := ""
	if !all {
		sessionID := strings.TrimSpace(protocol.Deref(msg.SourceSessionID))
		resolved, err := d.gardenWorkspaceFor(sessionID, msg.WorkspaceID)
		if err != nil {
			d.sendGardenError(conn, "ls", err)
			return
		}
		// A caller with no workspace to scope to must be told, not handed the
		// seeds that happen to have none. The scope is the whole point of the
		// flag-free form.
		if resolved == "" && msg.WorkspaceID == nil && sessionID == "" {
			d.sendGardenError(conn, "ls", errors.New(
				"there is no session to take a workspace from, so there is no default scope; pass --all for the whole garden, or --workspace <id> for one"))
			return
		}
		workspaceID = resolved
	}

	seeds, err := d.querySeeds(workspaceID, !all, gardenSnapshotLimit)
	if err != nil {
		d.sendGardenError(conn, "ls", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{
		Ok: true,
		SeedListResult: &protocol.SeedListResult{
			Seeds: seeds, WorkspaceID: workspaceID, All: all,
		},
	})
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
	d.sendGardenResponse(conn, protocol.Response{
		Ok:             true,
		SeedShowResult: &protocol.SeedShowResult{Seed: seedToProtocol(seed, doc)},
	})
}

func (d *Daemon) sendGardenResponse(conn net.Conn, resp protocol.Response) {
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		d.logf("garden: writing response: %v", err)
	}
}
