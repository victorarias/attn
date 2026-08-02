package daemon

import "github.com/victorarias/attn/internal/protocol"

func (d *Daemon) handleApprovePRWS(client *wsClient, msg *protocol.ApprovePRMessage) {
	d.logf("Processing approve for %s", msg.ID)
	go func() {
		ghClient, repo, number, _, err := d.clientForPRID(msg.ID)
		if err == nil {
			err = ghClient.ApprovePR(repo, number)
		}
		result := protocol.PRActionResultMessage{
			Event:   protocol.EventPRActionResult,
			Action:  "approve",
			ID:      msg.ID,
			Success: err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("Approve failed for %s: %v", msg.ID, err)
		} else {
			d.logf("Approve succeeded for %s", msg.ID)
			d.store.MarkPRApproved(msg.ID)
			d.store.SetPRHot(msg.ID)
			go d.fetchPRDetailsImmediate(msg.ID)
		}
		d.sendToClient(client, result)
		d.logf("Sent approve result to client")
		d.RefreshPRs()
	}()
}

func (d *Daemon) handleMergePRWS(client *wsClient, msg *protocol.MergePRMessage) {
	go func() {
		ghClient, repo, number, _, err := d.clientForPRID(msg.ID)
		if err == nil {
			err = ghClient.MergePR(repo, number, msg.Method)
		}
		result := protocol.PRActionResultMessage{
			Event:   protocol.EventPRActionResult,
			Action:  "merge",
			ID:      msg.ID,
			Success: err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		}
		d.sendToClient(client, result)
		d.RefreshPRs()
	}()
}

func (d *Daemon) handleMutePRWS(msg *protocol.MutePRMessage) {
	pr := d.store.GetPR(msg.ID)
	wasMuted := pr != nil && pr.Muted

	d.store.ToggleMutePR(msg.ID)

	d.coalesceSnapshots(func() {
		d.publishFact(FactPRMuteChanged, msg.ID, nil)
		if wasMuted {
			d.store.SetPRHot(msg.ID)
			go d.fetchPRDetailsImmediate(msg.ID)
			d.publishFact(FactPRHeatChanged, msg.ID, nil)
		}
	})
}

func (d *Daemon) handleMuteRepoWS(msg *protocol.MuteRepoMessage) {
	repoState := d.store.GetRepoState(msg.Repo)
	wasMuted := repoState != nil && repoState.Muted

	d.store.ToggleMuteRepo(msg.Repo)

	// One coalesced push per wire message, in the order the old direct calls
	// made them: the PRs that went hot, then the repo list.
	d.coalesceSnapshots(func() {
		if wasMuted {
			for _, pr := range d.store.ListPRsByRepo(msg.Repo) {
				d.store.SetPRHot(pr.ID)
				go d.fetchPRDetailsImmediate(pr.ID)
				d.publishFact(FactPRHeatChanged, pr.ID, nil)
			}
		}
		d.publishFact(FactRepoMuteChanged, msg.Repo, nil)
	})
}

func (d *Daemon) handleMuteAuthorWS(msg *protocol.MuteAuthorMessage) {
	d.store.ToggleMuteAuthor(msg.Author)
	d.publishFact(FactAuthorMuteChanged, msg.Author, nil)
}

func (d *Daemon) handleRefreshPRsWS(client *wsClient) {
	d.logf("Refreshing PRs on request")
	go func() {
		err := d.doRefreshPRsWithResult()
		result := protocol.RefreshPRsResultMessage{
			Event:   protocol.EventRefreshPRsResult,
			Success: err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("Refresh PRs failed: %v", err)
		} else {
			d.logf("Refresh PRs succeeded")
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleFetchPRDetailsWS(client *wsClient, msg *protocol.FetchPRDetailsMessage) {
	d.logf("Fetching PR details")
	go func() {
		updatedPRs, err := d.fetchPRDetailsForID(msg.ID)
		result := protocol.WebSocketEvent{
			Event:   protocol.EventFetchPRDetailsResult,
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("Fetch PR details failed: %v", err)
		} else {
			result.Prs = protocol.PRsToValues(updatedPRs)
			d.coalesceSnapshots(func() {
				for _, pr := range updatedPRs {
					d.publishFact(FactPRDetailsChanged, pr.ID, nil)
				}
			})
			d.logf("Fetch PR details succeeded")
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handlePRVisitedWS(msg *protocol.PRVisitedMessage) {
	d.logf("Marking PR %s as visited", msg.ID)
	d.store.MarkPRVisited(msg.ID)
	d.coalesceSnapshots(func() {
		d.publishFact(FactPRVisited, msg.ID, nil)
		// Visiting one PR warms every PR in its repo, so each of those changed too.
		if _, repo, _, err := protocol.ParsePRID(msg.ID); err == nil {
			for _, pr := range d.store.ListPRs("") {
				if pr.Repo == repo {
					d.store.SetPRHot(pr.ID)
					go d.fetchPRDetailsImmediate(pr.ID)
					d.publishFact(FactPRHeatChanged, pr.ID, nil)
				}
			}
		} else {
			d.store.SetPRHot(msg.ID)
			go d.fetchPRDetailsImmediate(msg.ID)
			d.publishFact(FactPRHeatChanged, msg.ID, nil)
		}
	})
}

// publishPRSetChanges recovers per-PR facts from a bulk replacement of the PR
// set. A poll or refresh overwrites the whole list, so nothing in the call
// itself says which PRs moved — the diff around it does, and one fact per moved
// PR is what a consumer can act on.
//
// The wire push is unchanged in shape: every pr.* fact projects to the same
// whole-list prs_updated, coalesced here so a refresh that touched twenty PRs
// still sends one message. It does change in frequency: a poll that found
// nothing new now publishes nothing and therefore sends nothing, where before
// it re-pushed the identical list to every client on every tick.
func (d *Daemon) publishPRSetChanges(before, after []*protocol.PR) {
	beforeByID := make(map[string]*protocol.PR, len(before))
	for _, pr := range before {
		beforeByID[pr.ID] = pr
	}
	afterByID := make(map[string]struct{}, len(after))
	for _, pr := range after {
		afterByID[pr.ID] = struct{}{}
	}

	d.coalesceSnapshots(func() {
		// Slice order, not map order: the facts land in the durable log, and a
		// consumer replaying them should see the same sequence the daemon saw.
		for _, pr := range after {
			previous, existed := beforeByID[pr.ID]
			switch {
			case !existed:
				d.publishFact(FactPRAppeared, pr.ID, nil)
			case !wireEqual(previous, pr):
				d.publishFact(FactPRUpdated, pr.ID, nil)
			}
		}
		for _, pr := range before {
			if _, still := afterByID[pr.ID]; !still {
				d.publishFact(FactPRDisappeared, pr.ID, nil)
			}
		}
	})
}

func (d *Daemon) projectPRsUpdated() {
	d.projectSnapshot(snapshotPRs, func() {
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event: protocol.EventPRsUpdated,
			Prs:   protocol.PRsToValues(d.store.ListPRs("")),
		})
	})
}

func (d *Daemon) projectRepoStatesUpdated() {
	d.projectSnapshot(snapshotRepos, func() {
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event: protocol.EventReposUpdated,
			Repos: protocol.RepoStatesToValues(d.store.ListRepoStates()),
		})
	})
}

func (d *Daemon) projectAuthorStatesUpdated() {
	d.projectSnapshot(snapshotAuthors, func() {
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:   protocol.EventAuthorsUpdated,
			Authors: protocol.AuthorStatesToValues(d.store.ListAuthorStates()),
		})
	})
}
