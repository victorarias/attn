package daemon

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type seedResumeOutcome struct {
	SessionID      string
	WorkspaceID    string
	AlreadyRunning bool
}

func (d *Daemon) resumeSeed(seedID string) (*seedResumeOutcome, error) {
	return d.resumeSeedFromReview(seedID, nil)
}

func (d *Daemon) resumeSeedFromReview(
	seedID string,
	review *protocol.SeedReviewActionContext,
) (*seedResumeOutcome, error) {
	seedID = strings.TrimSpace(seedID)
	if seedID == "" {
		return nil, fmt.Errorf("seed_id is required")
	}
	if err := d.requireHome(garden.Surface); err != nil {
		return nil, err
	}
	expectedRev := int64(0)
	if review != nil {
		item, err := d.validateGardenReviewAction(review, seedID, "resume")
		if err != nil {
			return nil, err
		}
		expectedRev = item.SeedRev
	}
	seed, seedDoc, err := d.readSeed(seedID)
	if err != nil {
		return nil, err
	}
	if expectedRev > 0 && seedDoc.Rev != expectedRev {
		return nil, fmt.Errorf("%s changed since you reviewed it; refresh the garden", seedID)
	}
	if garden.Closed(seed.Status) {
		return nil, fmt.Errorf("%s is %s; replant it before resuming its agent", seed.ID, seed.Status)
	}
	continuation := d.continuationForSeed(seed)
	if continuation == nil {
		if tender := strings.TrimSpace(seed.TenderSession); tender != "" {
			return nil, fmt.Errorf("%s was tended by session %s, but no continuation was saved", seedID, tender)
		}
		return nil, fmt.Errorf("%s has no agent conversation to resume", seedID)
	}
	execution := continuation.Execution
	sessionID := strings.TrimSpace(execution.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%s has no agent conversation to resume", seedID)
	}
	actor := garden.Tender{Session: sessionID}
	if _, err := garden.Transition(seed, garden.VerbTend, garden.Ask{Actor: actor}, d.sessionExists); err != nil {
		return nil, err
	}
	if existing := d.gardenSession(sessionID); existing != nil {
		if _, _, _, err := d.applySeedTransitionDetailedAtRevision(
			seedID, garden.VerbTend, garden.Ask{Actor: actor}, "", expectedRev); err != nil {
			return nil, err
		}
		if err := d.resolveGardenReviewAction(review, seedID, "resume"); err != nil {
			d.logf("Garden review: settle %s after Resume: %v", seedID, err)
		}
		return &seedResumeOutcome{
			SessionID: existing.ID, WorkspaceID: existing.WorkspaceID, AlreadyRunning: true,
		}, nil
	}
	if !continuation.ResumeAvailable {
		reason := strings.TrimSpace(continuation.ResumeReason)
		if reason == "" {
			reason = "the original conversation is unavailable"
		}
		return nil, fmt.Errorf("%s cannot resume: %s", seedID, reason)
	}
	afterSpawn := func() error {
		if _, err := d.validateGardenReviewAction(review, seedID, "resume"); err != nil {
			return err
		}
		return d.bindResumedSeed(seed, seedDoc, sessionID, strings.TrimSpace(execution.Cwd),
			strings.TrimSpace(execution.Agent), strings.TrimSpace(execution.Resume))
	}
	reopened, err := d.reopenSessionRuntime(sessionReopenPlan{
		SessionID:   sessionID,
		Directory:   execution.Cwd,
		Agent:       execution.Agent,
		ResumeID:    execution.Resume,
		Title:       seed.Title,
		WorkspaceID: reopenWorkspaceID(sessionID),
	}, d.newDelegationRollback(), afterSpawn)
	if err != nil {
		return nil, err
	}
	if err := d.resolveGardenReviewAction(review, seedID, "resume"); err != nil {
		d.logf("Garden review: settle %s after Resume: %v", seedID, err)
	}

	d.logf("resume: reopened seed %q as session %s", seedID, sessionID)
	return &seedResumeOutcome{SessionID: reopened.SessionID, WorkspaceID: reopened.WorkspaceID}, nil
}

func (d *Daemon) bindResumedSeed(
	seed garden.Seed,
	seedDoc docstore.Document,
	sessionID, directory, agent, resumeID string,
) error {
	next, err := garden.Transition(seed, garden.VerbTend, garden.Ask{
		Actor: garden.Tender{Session: sessionID},
	}, d.sessionExists)
	if err != nil {
		return fmt.Errorf("reclaim %s after resume: %w", seed.ID, err)
	}
	if next.Status != seed.Status {
		next.StateChangedAt = formatGardenTime(d.gardenTime())
	}
	next.LastExecutionID = sessionID

	seedSchema, err := d.seedsCollection()
	if err != nil {
		return err
	}
	dispatchSchema, err := d.dispatchesCollection()
	if err != nil {
		return err
	}
	seedBody, err := next.Encode()
	if err != nil {
		return err
	}

	dispatch, dispatchDoc, found, err := d.gardenDispatchDocument(sessionID)
	if err != nil {
		return err
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return fmt.Errorf("resumed session %s is not tracked", sessionID)
	}
	dispatch = mergeGardenExecution(dispatch, observedGardenExecution(session, resumeID, d.gardenTime()))
	dispatch.SessionID = sessionID
	dispatch.Crown = seed.ID
	dispatch.SupersededBy = ""
	if dispatch.Cwd == "" {
		dispatch.Cwd = directory
	}
	if dispatch.Agent == "" {
		dispatch.Agent = agent
	}
	dispatch.Resume = resumeID
	dispatchBody, err := dispatch.Encode()
	if err != nil {
		return err
	}

	seedExpected := seedDoc.Rev
	dispatchExpected := docstore.ExpectAbsent
	if found {
		dispatchExpected = dispatchDoc.Rev
	}
	seedFact := documentChangedFact(garden.Namespace, garden.CollectionSeeds, seed.ID, false)
	dispatchFact := documentChangedFact(garden.Namespace, garden.CollectionDispatches, sessionID, false)
	commits := []store.DocumentCommit{
		{
			Write: store.DocumentWrite{Schema: *seedSchema, ID: seed.ID, Body: seedBody, Expected: &seedExpected},
			Fact:  seedFact,
		},
		{
			Write: store.DocumentWrite{Schema: *dispatchSchema, ID: sessionID, Body: dispatchBody, Expected: &dispatchExpected},
			Fact:  dispatchFact,
		},
	}
	written, err := d.store.CommitDocumentWrites(commits, d.gardenTime())
	if err != nil {
		if docstore.IsConflict(err) {
			return fmt.Errorf("%s changed while its conversation was resuming; refresh it and try again", seed.ID)
		}
		return err
	}
	d.announceCommittedWrite(seedFact, written[0].Seq)
	d.announceCommittedWrite(dispatchFact, written[1].Seq)
	d.publishFact(FactGardenTended, seed.ID, nil)
	d.rememberDispatchProjection(sessionID, dispatch, written[1].Rev)
	d.ringSeedActivity(seed.ID, gardenRingEvents[garden.VerbTend], sessionID, "")
	return nil
}

func (d *Daemon) handleSeedResume(client *wsClient, msg *protocol.SeedResumeMessage) {
	requestID := protocol.Deref(msg.RequestID)
	outcome, err := d.resumeSeedFromReview(msg.SeedID, msg.Review)
	response := protocol.SeedResumeResultMessage{
		Event:     protocol.EventSeedResumeResult,
		RequestID: requestID,
		Success:   err == nil,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	} else {
		response.SessionID = protocol.Ptr(outcome.SessionID)
		response.WorkspaceID = protocol.Ptr(outcome.WorkspaceID)
		if outcome.AlreadyRunning {
			response.AlreadyRunning = protocol.Ptr(true)
		}
	}
	d.sendToClient(client, response)
}
