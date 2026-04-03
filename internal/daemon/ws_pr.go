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

	if wasMuted {
		d.store.SetPRHot(msg.ID)
		go d.fetchPRDetailsImmediate(msg.ID)
	}
	d.broadcastPRs()
}

func (d *Daemon) handleMuteRepoWS(msg *protocol.MuteRepoMessage) {
	repoState := d.store.GetRepoState(msg.Repo)
	wasMuted := repoState != nil && repoState.Muted

	d.store.ToggleMuteRepo(msg.Repo)

	if wasMuted {
		prs := d.store.ListPRsByRepo(msg.Repo)
		for _, pr := range prs {
			d.store.SetPRHot(pr.ID)
			go d.fetchPRDetailsImmediate(pr.ID)
		}
		if len(prs) > 0 {
			d.broadcastPRs()
		}
	}
	d.broadcastRepoStates()
}

func (d *Daemon) handleMuteAuthorWS(msg *protocol.MuteAuthorMessage) {
	d.store.ToggleMuteAuthor(msg.Author)
	d.broadcastAuthorStates()
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
			d.broadcastPRs()
			d.logf("Fetch PR details succeeded")
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handlePRVisitedWS(msg *protocol.PRVisitedMessage) {
	d.logf("Marking PR %s as visited", msg.ID)
	d.store.MarkPRVisited(msg.ID)
	if _, repo, _, err := protocol.ParsePRID(msg.ID); err == nil {
		for _, pr := range d.store.ListPRs("") {
			if pr.Repo == repo {
				d.store.SetPRHot(pr.ID)
				go d.fetchPRDetailsImmediate(pr.ID)
			}
		}
	} else {
		d.store.SetPRHot(msg.ID)
		go d.fetchPRDetailsImmediate(msg.ID)
	}
	d.broadcastPRs()
}

func (d *Daemon) broadcastPRs() {
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventPRsUpdated,
		Prs:   protocol.PRsToValues(d.store.ListPRs("")),
	})
}

func (d *Daemon) broadcastRepoStates() {
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventReposUpdated,
		Repos: protocol.RepoStatesToValues(d.store.ListRepoStates()),
	})
}

func (d *Daemon) broadcastAuthorStates() {
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   protocol.EventAuthorsUpdated,
		Authors: protocol.AuthorStatesToValues(d.store.ListAuthorStates()),
	})
}
