package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

var errAutomationReviewWithdrawn = errors.New(store.AutomationReviewWithdrawnError)

func (d *Daemon) automationRunPullRequest(ctx context.Context, definitionID, requestID, rawURL string) (*store.AutomationRun, error) {
	if strings.TrimSpace(requestID) == "" {
		return nil, errors.New("request_id is required")
	}
	if existing, err := d.store.GetManualAutomationRun(definitionID, requestID); err != nil {
		return nil, err
	} else if existing != nil {
		occurrence, err := d.store.GetAutomationOccurrence(existing.OccurrenceID)
		if err != nil || occurrence == nil {
			return nil, errors.Join(errors.New("existing automation occurrence missing"), err)
		}
		return d.automationRun(ctx, definitionID, requestID, occurrence.PayloadJSON)
	}
	def, err := d.store.GetAutomationDefinition(definitionID)
	if err != nil || def == nil {
		if err == nil {
			err = fmt.Errorf("automation %q not found", definitionID)
		}
		return nil, err
	}
	var spec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(def.SpecJSON), &spec); err != nil {
		return nil, err
	}
	if spec.Trigger.Type != "manual" {
		return nil, fmt.Errorf("automation %q is provider-driven and cannot be run manually yet", definitionID)
	}
	if spec.Location.Type != "repository_worktree" {
		return nil, errors.New("--pr-url requires a repository_worktree automation")
	}
	host, owner, repository, number, err := automation.ParsePullRequestURL(rawURL)
	if err != nil {
		return nil, err
	}
	if d.ghRegistry == nil {
		return nil, fmt.Errorf("GitHub host %s is not authenticated", host)
	}
	client, ok := d.ghRegistry.Get(host)
	if !ok {
		return nil, fmt.Errorf("GitHub host %s is not authenticated", host)
	}
	snapshot, err := client.FetchPullRequestSnapshot(owner+"/"+repository, number)
	if err != nil {
		return nil, err
	}
	if snapshot.Number != number || !strings.EqualFold(snapshot.BaseRepository, owner+"/"+repository) {
		return nil, errors.New("GitHub response does not match requested pull request")
	}
	if snapshot.State != "open" {
		return nil, fmt.Errorf("pull request is %s; only open pull requests can be reviewed", snapshot.State)
	}
	input := pullRequestAutomationInput(host, owner, repository, snapshot)
	canonical, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	if _, err := automation.ParsePullRequestInput(canonical); err != nil {
		return nil, err
	}
	return d.automationRun(ctx, definitionID, requestID, string(canonical))
}
func pullRequestAutomationInput(host, owner, repository string, snapshot *github.PullRequestSnapshot) automation.PullRequestInput {
	return automation.PullRequestInput{
		Provider: "github", Host: host, Owner: owner, Repository: repository, Number: snapshot.Number,
		URL: strings.TrimSuffix(snapshot.URL, "/"), Title: snapshot.Title, Body: snapshot.Body,
		Author: snapshot.Author, Draft: snapshot.Draft, State: snapshot.State,
		HeadSHA: snapshot.HeadSHA, HeadRef: snapshot.HeadRef, HeadRepository: snapshot.HeadRepository,
		BaseSHA: snapshot.BaseSHA, BaseRef: snapshot.BaseRef,
	}
}

// observeGitHubReviewRequests consumes one host's already-refreshed PR snapshot.
// It does not poll GitHub itself: only a newly active durable edge performs the
// focused PR GET needed to pin the immutable review input.
func (d *Daemon) observeGitHubReviewRequests(host string, prs []*protocol.PR, observedAt time.Time) {
	definitions, err := d.store.ListAutomationDefinitions()
	if err != nil {
		d.logf("automation GitHub observation list definitions: %v", err)
		return
	}
	client, ok := d.ghRegistry.Get(host)
	if !ok {
		return
	}
	for i := range definitions {
		definition := definitions[i]
		if !definition.Enabled {
			continue
		}
		var spec automation.DefinitionSpec
		if err := json.Unmarshal([]byte(definition.SpecJSON), &spec); err != nil {
			d.logf("automation GitHub observation parse %s: %v", definition.ID, err)
			continue
		}
		if spec.Trigger.Type != "github_review_requested" {
			continue
		}
		bySubject := make(map[string]*protocol.PR)
		var subjects []string
		for _, pr := range prs {
			if pr == nil || pr.ApprovedByMe || pr.Role != protocol.PRRoleReviewer || pr.Reason != protocol.PRReasonReviewNeeded || pr.State != protocol.PRStateWaiting {
				continue
			}
			identity, err := automation.CanonicalRepositoryIdentity(host + "/" + pr.Repo)
			if err != nil || !spec.Trigger.Repositories.Matches(identity) {
				continue
			}
			subject := identity + "#" + fmt.Sprint(pr.Number)
			if _, exists := bySubject[subject]; exists {
				continue
			}
			bySubject[subject] = pr
			subjects = append(subjects, subject)
		}
		candidates, err := d.reconcileAutomationReviewRequests(definition.ID, host, subjects, observedAt)
		if err != nil {
			d.logf("automation GitHub observation reconcile %s: %v", definition.ID, err)
			continue
		}
		for _, candidate := range candidates {
			pr := bySubject[candidate.SubjectKey]
			if pr == nil {
				continue
			}
			observationLock := d.automationObservationLock(definition.ID, candidate.SubjectKey, candidate.Cycle)
			observationLock.Lock()
			needsClaim, err := d.store.AutomationReviewRequestNeedsClaim(definition.ID, candidate.SubjectKey, candidate.Cycle)
			if err != nil || !needsClaim {
				observationLock.Unlock()
				if err != nil {
					d.logf("automation GitHub observation recheck %s: %v", candidate.SubjectKey, err)
				}
				continue
			}
			repositoryParts := strings.Split(pr.Repo, "/")
			if len(repositoryParts) != 2 {
				observationLock.Unlock()
				d.logf("automation GitHub observation invalid repository %q", pr.Repo)
				continue
			}
			providerSnapshot, err := client.FetchPullRequestSnapshot(pr.Repo, pr.Number)
			if err != nil {
				observationLock.Unlock()
				d.logf("automation GitHub observation fetch %s: %v", candidate.SubjectKey, err)
				continue
			}
			if providerSnapshot.Number != pr.Number || !strings.EqualFold(providerSnapshot.BaseRepository, pr.Repo) || providerSnapshot.State != "open" || providerSnapshot.Draft {
				observationLock.Unlock()
				d.logf("automation GitHub observation ignored mismatched snapshot for %s", candidate.SubjectKey)
				continue
			}
			input := pullRequestAutomationInput(host, repositoryParts[0], repositoryParts[1], providerSnapshot)
			payload, err := json.Marshal(input)
			if err != nil {
				observationLock.Unlock()
				continue
			}
			if _, err := automation.ParsePullRequestInput(payload); err != nil {
				observationLock.Unlock()
				d.logf("automation GitHub observation invalid snapshot %s: %v", candidate.SubjectKey, err)
				continue
			}
			effective, err := automation.Effective(spec, definition.Revision)
			if err != nil {
				observationLock.Unlock()
				continue
			}
			snapshotJSON, err := json.Marshal(effective)
			if err != nil {
				observationLock.Unlock()
				continue
			}
			run, _, err := d.store.ClaimGitHubReviewAutomationRun(definition.ID, candidate.SubjectKey, candidate.Cycle, definition.Revision, string(payload), string(snapshotJSON), observedAt, newAutomationRunReservation())
			observationLock.Unlock()
			if err != nil {
				d.logf("automation GitHub observation claim %s: %v", candidate.SubjectKey, err)
				continue
			}
			// A run now exists for this definition (freshly claimed, or the
			// idempotent dedup of an already-claimed one) whether or not
			// delivery below succeeds; broadcast so a WS client watching this
			// definition's runs sees it appear without waiting on the
			// delivery outcome.
			d.broadcastAutomationsChanged(definition.ID)
			d.automationMu.Lock()
			current, loadErr := d.store.GetAutomationRun(run.ID)
			if loadErr == nil && current != nil && current.State == "pending" {
				if deliverErr := d.deliverObservedAutomationRun(current); deliverErr != nil {
					_, deliverErr = d.handleAutomationDeliveryError(current, deliverErr)
					loadErr = deliverErr
				}
			}
			d.automationMu.Unlock()
			if loadErr != nil {
				d.logf("automation GitHub observation deliver %s: %v", candidate.SubjectKey, loadErr)
			}
		}
	}
}
func (d *Daemon) reconcileAutomationReviewRequests(definitionID, host string, subjects []string, observedAt time.Time) ([]store.AutomationReviewRequestCandidate, error) {
	d.automationMu.Lock()
	defer d.automationMu.Unlock()
	// Finish any cancellation made durable by an earlier observation before a
	// fresh provider snapshot can reactivate the edge. This closes the daemon-exit
	// window between recording withdrawal and stopping a partially launched PTY.
	if err := d.cancelWithdrawnAutomationRuns(definitionID, host); err != nil {
		return nil, err
	}
	candidates, err := d.store.ReconcileAutomationReviewRequests(definitionID, host, subjects, observedAt)
	if err != nil {
		return nil, err
	}
	if err := d.cancelWithdrawnAutomationRuns(definitionID, host); err != nil {
		return nil, err
	}
	return candidates, nil
}
func (d *Daemon) cancelWithdrawnAutomationRuns(definitionID, host string) error {
	withdrawn, err := d.store.ListWithdrawnGitHubReviewUndeliveredRuns(definitionID, host)
	if err != nil {
		return err
	}
	var cancelErr error
	for i := range withdrawn {
		if err := d.cancelWithdrawnAutomationRun(&withdrawn[i]); err != nil {
			cancelErr = errors.Join(cancelErr, err)
		}
	}
	return cancelErr
}
func (d *Daemon) cancelWithdrawnAutomationRun(run *store.AutomationRun) error {
	if run == nil {
		return nil
	}
	ticket, ticketErr := d.store.GetTicket(run.TicketID)
	if ticketErr != nil {
		return ticketErr
	}
	// Continuation cycles reuse the delivered origin's session. Withdrawal fails
	// that occurrence, but must not tear down a reviewer already handed off to the
	// ordinary ticket/session lifecycle.
	if ticket != nil && ticket.AutomationRunID == run.ID {
		if d.hasAutomationSession(run.SessionID) {
			// Keep the durable run, ticket, workspace, pane, and worktree as evidence,
			// but stop and forget the unrequested runtime. A later initial-cycle
			// re-request may safely recreate the same reserved session ID.
			if err := d.terminateSessionChecked(run.SessionID, syscall.SIGTERM); err != nil {
				return fmt.Errorf("stop withdrawn automation reviewer: %w", err)
			}
			d.forgetSession(run.SessionID)
		}
	}
	failureComment := automationFailureComment(run, ticket, store.AutomationReviewWithdrawnError)
	if run.State == "failed" {
		if ticket == nil || (ticket.AutomationRunID == run.ID && ticket.Status == store.TicketStatusFailed) {
			return nil
		}
		if ticket.AutomationRunID != run.ID {
			author := "automation:" + run.DefinitionID
			for _, activity := range ticket.Activity {
				if activity.Author == author && activity.Comment == failureComment {
					return nil
				}
			}
		}
	}
	_, failErr := d.failAutomationRun(run, errAutomationReviewWithdrawn)
	return failErr
}
func (d *Daemon) hasAutomationSession(sessionID string) bool {
	if d.store.Get(sessionID) != nil {
		return true
	}
	if d.ptyBackend == nil {
		return false
	}
	for _, liveSessionID := range d.ptyBackend.SessionIDs(context.Background()) {
		if liveSessionID == sessionID {
			return true
		}
	}
	return false
}
