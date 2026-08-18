package daemon

import "github.com/victorarias/attn/internal/protocol"

// currentStateProjection is the state-bearing part of Initial State. Keeping
// its assembly here gives app handlers the same local-and-relayed view as the
// frontend without exposing settings, warnings, or protocol metadata.
type currentStateProjection struct {
	Sessions    []protocol.Session
	Endpoints   []protocol.EndpointInfo
	Workspaces  []protocol.Workspace
	Prs         []protocol.PR
	Repos       []protocol.RepoState
	Authors     []protocol.AuthorState
	GithubHosts []string
	Tickets     []protocol.TicketRow
	Seeds       []protocol.Seed
	Crew        []protocol.CrewMember
	Apps        []protocol.AppRegistryEntry
}

func (d *Daemon) currentStateProjection() currentStateProjection {
	return currentStateProjection{
		Sessions:    d.mergedSessionsForBroadcast(),
		Endpoints:   d.listEndpointInfos(),
		Workspaces:  d.listWorkspaces(),
		Prs:         protocol.PRsToValues(d.store.ListPRs("")),
		Repos:       protocol.RepoStatesToValues(d.store.ListRepoStates()),
		Authors:     protocol.AuthorStatesToValues(d.store.ListAuthorStates()),
		GithubHosts: d.gitHubHosts(),
		Tickets:     d.ticketsForBroadcast(),
		Seeds:       d.seedsForBroadcast(),
		Crew:        d.crewForBroadcast(),
		Apps:        d.appRegistryForWire(),
	}
}

// appCurrentStateSnapshot is the SDK's bounded current-state read. The bus
// position is captured before the projection is assembled, so every mutation
// at or below it is already visible and any later mutation still has a fact.
type appCurrentStateSnapshot struct {
	AsOfSeq     int64                       `json:"asOfSeq"`
	Sessions    []protocol.Session          `json:"sessions"`
	Endpoints   []protocol.EndpointInfo     `json:"endpoints"`
	Workspaces  []protocol.Workspace        `json:"workspaces"`
	Prs         []protocol.PR               `json:"prs"`
	Repos       []protocol.RepoState        `json:"repos"`
	Authors     []protocol.AuthorState      `json:"authors"`
	GithubHosts []string                    `json:"githubHosts"`
	Tickets     []protocol.TicketRow        `json:"tickets"`
	Seeds       []protocol.Seed             `json:"seeds"`
	Crew        []protocol.CrewMember       `json:"crew"`
	Apps        []protocol.AppRegistryEntry `json:"apps"`
}

func (d *Daemon) appCurrentStateSnapshot() (appCurrentStateSnapshot, error) {
	_, head, err := d.store.BusBounds()
	if err != nil {
		return appCurrentStateSnapshot{}, err
	}
	state := d.currentStateProjection()
	return appCurrentStateSnapshot{
		AsOfSeq:     head,
		Sessions:    snapshotSlice(state.Sessions),
		Endpoints:   snapshotSlice(state.Endpoints),
		Workspaces:  snapshotSlice(state.Workspaces),
		Prs:         snapshotSlice(state.Prs),
		Repos:       snapshotSlice(state.Repos),
		Authors:     snapshotSlice(state.Authors),
		GithubHosts: snapshotSlice(state.GithubHosts),
		Tickets:     snapshotSlice(state.Tickets),
		Seeds:       snapshotSlice(state.Seeds),
		Crew:        snapshotSlice(state.Crew),
		Apps:        snapshotSlice(state.Apps),
	}, nil
}

func snapshotSlice[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}
