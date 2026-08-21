package daemon

import (
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/garden"
)

// Every delegation binds a seed, and that binding is the dispatch record: one
// document keyed by the delegate's session id, so "which seed does this session
// report to" is one read from anywhere rather than a field private to this
// file. Design: docs/plans/2026-08-18-delegation-reporting-on-seeds.md.
//
// A delegation aimed at an existing seed (`--plot`) binds it and claims it: the
// seed's plan is where the delegate's work reports, and the delegate is the one
// working it. Any other delegation plants its own seed: the delegation's name is
// the title, the brief is the body, the delegating session is the planter and
// the delegate session is the tender. Either way the delegate holds the seed its
// launch prompt names.
//
// The bind runs before the runtime spawns, for the same reason the crown
// dispatch always has: the delegate's own prompt names the seed, and a prompt
// cannot name a seed that does not exist yet.

// bindDelegationSeed binds the seed and answers with its id, or with "" when
// the garden could not take it.
//
// A planted seed rides beside the ticket during the transition — the ticket is
// still the authoritative channel back to a delegate — so a failure there is a
// log line, not a refused launch: trading a working delegation for a missing
// log is the wrong way round. A delegation that explicitly named a crown is the
// exception and keeps failing outright, because it asked to be aimed and a
// silently unaimed delegate is not what was asked for.
func (d *Daemon) bindDelegationSeed(sessionID, plannerSessionID, brief, name, crown, cwd, agent string, fromChief bool) (string, error) {
	seedID, err := d.bindDelegatedSeed(sessionID, plannerSessionID, brief, name, crown, cwd, agent, fromChief)
	switch {
	case err == nil:
		d.logf("delegate: bound seed %q to session %s", seedID, sessionID)
	case crown != "" && !delegationSeedUnavailable(err):
		return "", fmt.Errorf("dispatch %s at %s: %w", sessionID, crown, err)
	case delegationSeedUnavailable(err):
		d.logf("delegate: no seed bound to session %s: %v", sessionID, err)
	default:
		d.logf("delegate: binding a seed to session %s failed: %v", sessionID, err)
	}
	return seedID, nil
}

// bindDelegatedSeed is the bind itself. It is idempotent through the dispatch
// record, so a delegation resumed after a daemon crash re-binds what it already
// planted instead of planting a second seed for the same session.
func (d *Daemon) bindDelegatedSeed(sessionID, plannerSessionID, brief, name, crown, cwd, agent string, fromChief bool) (string, error) {
	if err := d.requireHome(garden.Surface); err != nil {
		return "", err
	}
	if bound, ok := d.gardenDispatchCrown(sessionID); ok {
		return bound, nil
	}
	seedID := strings.TrimSpace(crown)
	if seedID == "" {
		seed, err := d.plantDelegatedSeed(sessionID, plannerSessionID, brief, name)
		if err != nil {
			return "", err
		}
		seedID = seed.ID
	} else if err := d.tendDispatchedSeed(sessionID, plannerSessionID, seedID); err != nil {
		return "", err
	}
	if err := d.recordGardenDispatch(sessionID, seedID, plannerSessionID, cwd, agent, fromChief); err != nil {
		return "", fmt.Errorf("bind %s to session %s: %w", seedID, sessionID, err)
	}
	d.ringSeedActivity(seedID, gardenRingEvents[garden.VerbTend], sessionID, plannerSessionID)
	return seedID, nil
}

// plantDelegatedSeed plants the delegation's seed already tended by its
// delegate. Planting and claiming are one write rather than a plant followed by
// a tend: a seed that exists unheld for a moment is a seed `ready` can offer to
// somebody else. The claim still goes through garden.Transition, so the rules
// decide what tending means here exactly as they do everywhere.
func (d *Daemon) plantDelegatedSeed(sessionID, plannerSessionID, brief, name string) (garden.Seed, error) {
	title := strings.TrimSpace(name)
	if title == "" {
		// Delegation names it from --name, an adopted ticket's title, or the
		// directory basename, so this is the empty-brief-and-no-name corner alone.
		title = "delegated work"
	}
	body := strings.TrimSpace(brief)
	if err := garden.ValidatePlant(title, body); err != nil {
		return garden.Seed{}, err
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return garden.Seed{}, err
	}
	seed := garden.Seed{
		Title:          title,
		Body:           body,
		Status:         garden.StatusPlanted,
		StepSlug:       garden.StepSlug(title),
		PlanterSession: plannerSessionID,
		PlanterMember:  d.resolveTenderMember("", plannerSessionID),
		Edges:          []garden.Edge{},
		Vars:           []garden.Var{},
	}
	// A delegation launched from a session that itself reports to a seed leaves
	// lineage by construction: the new seed is born part-of the caller's seed,
	// so a delegate's delegations read as its plot instead of as orphans. Only
	// a seed the delegation creates nests this way — dispatching at an existing
	// seed (--plot) leaves nesting to the caller, who knows whose work it is.
	if parent, ok := d.gardenDispatchCrown(plannerSessionID); ok && parent != "" {
		seed.Edges = append(seed.Edges, garden.Edge{Kind: garden.EdgePartOf, To: parent})
	}
	tender := garden.Tender{Session: sessionID}
	// Nothing holds an unwritten seed, so the claim cannot be contested and the
	// liveness predicate is never consulted.
	seed, err = garden.Transition(seed, garden.VerbTend, tender, "", func(string) bool { return false })
	if err != nil {
		return garden.Seed{}, err
	}
	seed, _, err = d.mintAndPlant(*schema, seed)
	return seed, err
}

// delegationSeedUnavailable reports whether a bind failure is the garden simply
// not being here. It is named rather than inferred from the message, because it
// is the one failure that is not a defect.
func delegationSeedUnavailable(err error) bool {
	var fenced *enrollment.FencedError
	return errors.As(err, &fenced)
}

// tendDispatchedSeed claims an existing seed for the delegate about to launch.
//
// The delegate's own prompt tells it the seed is its work and it is its tender,
// so a dispatch that leaves the claim on somebody else ships an agent whose
// prompt is a lie — which is how a deleted session kept holding a seed while a
// replacement worked it. The claim goes through garden.Transition like every
// other tend, so a tender whose session is still around refuses the take-over
// rather than losing it silently; validateDispatchCrown already answered that
// case before anything was created, and this is the race backstop behind it.
func (d *Daemon) tendDispatchedSeed(sessionID, plannerSessionID, seedID string) error {
	actor := garden.Tender{Session: sessionID, Member: d.resolveTenderMember("", sessionID)}
	if _, _, err := d.applySeedTransitionAs(seedID, garden.VerbTend, actor, "", d.dispatchSessionLive(plannerSessionID)); err != nil {
		return fmt.Errorf("tend %s as session %s: %w", seedID, sessionID, err)
	}
	return nil
}

// dispatchSessionLive is the liveness the dispatch's claim reads: every session
// as it really is, except the delegating one, which is handing the seed over
// and so does not hold it against its own delegate.
func (d *Daemon) dispatchSessionLive(plannerSessionID string) func(string) bool {
	planner := strings.TrimSpace(plannerSessionID)
	return func(sessionID string) bool {
		if planner != "" && strings.TrimSpace(sessionID) == planner {
			return false
		}
		return d.sessionExists(sessionID)
	}
}
