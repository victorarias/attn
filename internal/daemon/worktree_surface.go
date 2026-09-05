package daemon

import (
	"encoding/json"
	"net"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// A page that fits an 80x24 terminal with a header and the omitted notice. The
// surface asks for everything; only the CLI pages.
const defaultWorktreeListLimit = 20

// A tripwire, not a budget: the largest registry measured is 147 rows across two
// repositories, so nothing healthy comes near it.
const maxWorktreeListLimit = 5000

// Reads the registry only, so it answers at request speed however slow the
// repository is.
func (d *Daemon) worktreeListResult(mainRepo string, limit int) *protocol.WorktreeListResult {
	if limit <= 0 {
		limit = maxWorktreeListLimit
	}
	if limit > maxWorktreeListLimit {
		limit = maxWorktreeListLimit
	}

	var rows []*store.Worktree
	if mainRepo != "" {
		rows = d.store.ListWorktreesByRepo(git.CanonicalizePath(mainRepo))
	} else {
		rows = d.store.ListWorktrees()
	}

	omitted := 0
	if len(rows) > limit {
		omitted = len(rows) - limit
		rows = rows[:limit]
	}

	// Both are required arrays on the wire. A nil slice marshals to null, which
	// the panel iterates and the app dies rendering.
	result := &protocol.WorktreeListResult{
		Worktrees:    make([]protocol.Worktree, 0, len(rows)),
		Repositories: make([]protocol.WorktreeRepository, 0),
		Omitted:      omitted,
	}
	seenRepo := make(map[string]bool)
	for _, row := range rows {
		result.Worktrees = append(result.Worktrees, protocolWorktree(row))
		if seenRepo[row.MainRepo] {
			continue
		}
		seenRepo[row.MainRepo] = true
		repo := protocol.WorktreeRepository{MainRepo: row.MainRepo}
		if record := d.store.RepoIntegrationBranch(row.MainRepo); record != nil {
			repo.IntegrationBranch = protocol.Ptr(record.Branch)
			repo.IntegrationSource = protocol.Ptr(record.Source)
		}
		result.Repositories = append(result.Repositories, repo)
	}
	return result
}

func (d *Daemon) setWorktreeKeep(path string, keep bool) (*protocol.Worktree, error) {
	path = git.CanonicalizePath(path)
	if !d.store.SetWorktreePin(path, keep, time.Now()) {
		return nil, &worktreeNotFoundError{path: path}
	}
	wt := d.store.GetWorktree(path)
	if wt == nil {
		return nil, &worktreeNotFoundError{path: path}
	}
	// The pin decides the next verdict, so the row must not keep a stale reason
	// until the next tick.
	if keep {
		d.store.SetWorktreeSweep(path, store.WorktreeSweepPinned, "kept forever by you", time.Time{})
	} else {
		d.store.SetWorktreeSweep(path, store.WorktreeSweepUnknown, "unpinned; the next sweep decides", time.Time{})
	}
	wt = d.store.GetWorktree(path)
	d.publishWorktreeState(wt)
	out := protocolWorktree(wt)
	return &out, nil
}

func (d *Daemon) worktreeSweepLogResult(mainRepo string, limit int) *protocol.WorktreeSweepLogResult {
	if limit <= 0 {
		limit = defaultWorktreeListLimit
	}
	if limit > maxWorktreeListLimit {
		limit = maxWorktreeListLimit
	}
	entries, omitted := d.store.WorktreeSweepLog(mainRepo, limit)
	result := &protocol.WorktreeSweepLogResult{
		Entries: make([]protocol.WorktreeSweepEntry, 0, len(entries)),
		Omitted: omitted,
	}
	for _, entry := range entries {
		result.Entries = append(result.Entries, protocolSweepEntry(entry))
	}
	return result
}

// Pulls the cron entry in rather than running inline: no request path may wait
// on minutes of git.
func (d *Daemon) queueWorktreeRefresh() bool {
	queue := d.jobQueueRef()
	if queue == nil {
		return false
	}
	if _, err := queue.Enqueue(worktreeSweepKind, jobs.EnqueueOptions{
		UniqueKey: jobs.CronKey, RunNow: true,
	}); err != nil {
		d.logf("worktree refresh: queueing a pass: %v", err)
		return false
	}
	return true
}

func (d *Daemon) handleWorktreeList(conn net.Conn, msg *protocol.WorktreeListMessage) {
	result := d.worktreeListResult(protocol.Deref(msg.MainRepo), int(protocol.Deref(msg.Limit)))
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, WorktreeListResult: result})
}

func (d *Daemon) handleWorktreeKeep(conn net.Conn, msg *protocol.WorktreeKeepMessage) {
	wt, err := d.setWorktreeKeep(msg.Path, msg.Keep)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                 true,
		WorktreeKeepResult: &protocol.WorktreeKeepResult{Worktree: *wt},
	})
}

func (d *Daemon) handleWorktreeSweepLog(conn net.Conn, msg *protocol.WorktreeSweepLogMessage) {
	result := d.worktreeSweepLogResult(protocol.Deref(msg.MainRepo), int(protocol.Deref(msg.Limit)))
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, WorktreeSweepLogResult: result})
}

func (d *Daemon) handleWorktreeRefresh(conn net.Conn, _ *protocol.WorktreeRefreshMessage) {
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                    true,
		WorktreeRefreshResult: &protocol.WorktreeRefreshResult{Queued: d.queueWorktreeRefresh()},
	})
}

func (d *Daemon) handleWorktreeListWS(client *wsClient, msg *protocol.WorktreeListMessage) {
	result := d.worktreeListResult(protocol.Deref(msg.MainRepo), int(protocol.Deref(msg.Limit)))
	d.sendToClient(client, &protocol.WebSocketEvent{
		Event:              protocol.EventWorktreeListResult,
		RequestID:          msg.RequestID,
		Success:            protocol.Ptr(true),
		WorktreeListResult: result,
	})
}

func (d *Daemon) handleWorktreeKeepWS(client *wsClient, msg *protocol.WorktreeKeepMessage) {
	event := &protocol.WebSocketEvent{
		Event: protocol.EventWorktreeKeepResult, RequestID: msg.RequestID, Success: protocol.Ptr(true),
	}
	wt, err := d.setWorktreeKeep(msg.Path, msg.Keep)
	if err != nil {
		event.Success = protocol.Ptr(false)
		event.Error = protocol.Ptr(err.Error())
	} else {
		event.Worktrees = []protocol.Worktree{*wt}
	}
	d.sendToClient(client, event)
}

func (d *Daemon) handleWorktreeSweepLogWS(client *wsClient, msg *protocol.WorktreeSweepLogMessage) {
	result := d.worktreeSweepLogResult(protocol.Deref(msg.MainRepo), int(protocol.Deref(msg.Limit)))
	d.sendToClient(client, &protocol.WebSocketEvent{
		Event:                  protocol.EventWorktreeSweepLogResult,
		RequestID:              msg.RequestID,
		Success:                protocol.Ptr(true),
		WorktreeSweepLogResult: result,
	})
}

func (d *Daemon) handleWorktreeRefreshWS(client *wsClient, msg *protocol.WorktreeRefreshMessage) {
	d.sendToClient(client, &protocol.WebSocketEvent{
		Event:     protocol.EventWorktreeRefreshResult,
		RequestID: msg.RequestID,
		Success:   protocol.Ptr(d.queueWorktreeRefresh()),
	})
}

func (d *Daemon) projectWorktreeStateChanged(ev bus.Event) {
	worktree, ok := decodeFact[protocol.Worktree](d, ev)
	if !ok {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventWorktreeStateChanged,
		Worktrees: []protocol.Worktree{worktree},
	})
}

func (d *Daemon) projectWorktreeSwept(ev bus.Event) {
	entry, ok := decodeFact[protocol.WorktreeSweepEntry](d, ev)
	if !ok {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:      protocol.EventWorktreeSwept,
		SweepEntry: &entry,
	})
}
