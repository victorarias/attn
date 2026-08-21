package daemon

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

var gardenRingEvents = map[garden.Verb]string{
	garden.VerbTend: "tended", garden.VerbPark: "parked", garden.VerbHarvest: "harvested",
	garden.VerbWither: "withered", garden.VerbReplant: "replanted",
}

// handleSeedWatch adds or removes this session's explicit subscription. A
// dispatch subscription is separate and automatic, so unwatch never severs the
// relationship between a delegator and its delegate.
func (d *Daemon) handleSeedWatch(conn net.Conn, msg *protocol.SeedWatchMessage) {
	verb := "watch"
	watching := !protocol.Deref(msg.Unwatch)
	if !watching {
		verb = "unwatch"
	}
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, verb, err)
		return
	}
	seed, _, err := d.readSeed(msg.SeedID)
	if err != nil {
		d.sendGardenError(conn, verb, err)
		return
	}
	sessionID := strings.TrimSpace(msg.SourceSessionID)
	if sessionID == "" || d.store.Get(sessionID) == nil {
		d.sendGardenError(conn, verb, fmt.Errorf("watching is for a live attn session; pass --session or run it inside one"))
		return
	}
	changed, err := d.store.SetGardenSeedWatch(sessionID, seed.ID, watching, time.Now())
	if err != nil {
		d.sendGardenError(conn, verb, err)
		return
	}
	if !watching {
		d.consumeSeedBell(sessionID, seed.ID)
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedWatchResult: &protocol.SeedWatchResult{
		SeedID: seed.ID, Watching: watching, Changed: changed,
	}})
}

func (d *Daemon) seedWatching(sessionID, seedID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || d.store == nil {
		return false
	}
	watching, err := d.store.GardenSeedWatching(sessionID, seedID)
	if err != nil {
		d.logf("garden bell: reading watch session=%s seed=%s: %v", sessionID, seedID, err)
	}
	return watching
}

func (d *Daemon) consumeSeedBell(sessionID, seedID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || d.store == nil {
		return
	}
	consumed, err := d.store.ConsumeGardenSeedBell(sessionID, seedID)
	if err != nil {
		d.logf("garden bell: consuming session=%s seed=%s: %v", sessionID, seedID, err)
		return
	}
	if consumed {
		d.forgetQueuedAgentMessages(sessionID)
	}
}

// ringSeedActivity finds the audience from the current graph. Watching an
// ancestor covers descendants planted later because no subscription is copied
// down the tree.
func (d *Daemon) ringSeedActivity(seedID, eventKind string, excludedSessionIDs ...string) {
	if d.store == nil {
		return
	}
	read, err := d.readGarden()
	if err != nil {
		d.logf("garden bell: reading graph for %s: %v", seedID, err)
		return
	}
	ancestors := gardenSeedAncestors(read.seeds, seedID)
	if len(ancestors) == 0 {
		return
	}
	targets := map[string]bool{}
	watches, err := d.store.GardenSeedWatches()
	if err != nil {
		d.logf("garden bell: reading watches for %s: %v", seedID, err)
		return
	}
	for _, watch := range watches {
		if ancestors[watch.SeedID] {
			targets[watch.WatcherSessionID] = true
		}
	}
	dispatches, err := d.readGardenDispatchesAt(ancestors)
	if err != nil {
		d.logf("garden bell: reading dispatches for %s: %v", seedID, err)
		return
	}
	for _, dispatch := range dispatches {
		if ancestors[dispatch.Crown] && dispatch.DispatcherSession != "" {
			targets[dispatch.DispatcherSession] = true
		}
	}

	for _, sessionID := range excludedSessionIDs {
		delete(targets, strings.TrimSpace(sessionID))
	}
	for sessionID := range targets {
		if d.store.Get(sessionID) == nil {
			continue
		}
		d.claimAndDeliverSeedBell(sessionID, seedID, eventKind)
	}
}

func gardenSeedAncestors(seeds []garden.Seed, seedID string) map[string]bool {
	parents := make(map[string]string, len(seeds))
	known := make(map[string]bool, len(seeds))
	for _, seed := range seeds {
		known[seed.ID] = true
		for _, edge := range seed.Edges {
			if edge.Kind == garden.EdgePartOf {
				parents[seed.ID] = edge.To
				break
			}
		}
	}
	ancestors := map[string]bool{}
	for at := seedID; known[at] && !ancestors[at]; at = parents[at] {
		ancestors[at] = true
	}
	return ancestors
}

func (d *Daemon) readGardenDispatchesAt(seedIDs map[string]bool) ([]garden.Dispatch, error) {
	var dispatches []garden.Dispatch
	for seedID := range seedIDs {
		after := ""
		for {
			read, _, err := d.runDocQuery(docstore.Query{
				Namespace: garden.Namespace, Collection: garden.CollectionDispatches,
				Filters: []docstore.Filter{{Field: "crown", Op: docstore.OpEq, Value: seedID}},
				Limit:   docstore.MaxLimit, After: after,
			})
			if err != nil {
				return nil, err
			}
			for _, doc := range read.Documents {
				dispatch, err := garden.DecodeDispatch(doc.Body)
				if err != nil {
					return nil, err
				}
				dispatches = append(dispatches, dispatch)
			}
			if len(read.Documents) < docstore.MaxLimit {
				break
			}
			after = read.Documents[len(read.Documents)-1].ID
		}
	}
	return dispatches, nil
}

func (d *Daemon) claimAndDeliverSeedBell(sessionID, seedID, eventKind string) {
	now := time.Now()
	message := store.AgentMessage{
		ID: uuid.NewString(), TargetSessionID: sessionID,
		Content:   fmt.Sprintf("🔔 %s moved: %s — read it with `attn seed show %s`.", seedID, eventKind, seedID),
		CreatedAt: now.UTC().Format(time.RFC3339),
	}
	claimed, err := d.store.ClaimGardenSeedBell(sessionID, seedID, eventKind, message, now)
	if err != nil {
		d.logf("garden bell: claiming session=%s seed=%s: %v", sessionID, seedID, err)
		return
	}
	if !claimed {
		return
	}
	d.noteQueuedAgentMessage(sessionID)
	go d.drainQueuedAgentMessages(sessionID)
}
