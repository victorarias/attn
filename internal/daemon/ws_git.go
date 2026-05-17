package daemon

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const (
	gitStatusRefreshReasonSubscribe = "subscribe"
	gitStatusRefreshReasonDirty     = "dirty"
	gitStatusRefreshReasonSafety    = "safety"
)

type gitStatusRefreshRequest struct {
	immediate bool
	reason    string
}

type gitStatusRefreshResult struct {
	duration time.Duration
	limited  bool
}

var (
	gitStatusRefreshDebounce     = 1 * time.Second
	gitStatusSafetyInterval      = 30 * time.Second
	gitStatusSlowSafetyInterval  = 2 * time.Minute
	gitStatusSlowRefreshDuration = 5 * time.Second
	gitStatusFullBudget          = 5 * time.Second
)

func (d *Daemon) handleSubscribeGitStatus(client *wsClient, msg *protocol.SubscribeGitStatusMessage) {
	client.stopGitStatusPoll()

	client.gitStatusMu.Lock()
	client.gitStatusDir = msg.Directory
	client.gitStatusStop = make(chan struct{})
	stopChan := client.gitStatusStop
	refreshChan := make(chan gitStatusRefreshRequest, 1)
	client.gitStatusRefresh = refreshChan
	client.gitStatusMu.Unlock()

	go d.runGitStatusScheduler(client, msg.Directory, stopChan, refreshChan)
	client.requestGitStatusRefresh(gitStatusRefreshRequest{immediate: true, reason: gitStatusRefreshReasonSubscribe})
}

func (d *Daemon) handleUnsubscribeGitStatusWS(client *wsClient) {
	d.logf("Unsubscribing from git status")
	client.stopGitStatusPoll()
}

func (d *Daemon) runGitStatusScheduler(client *wsClient, dir string, stop <-chan struct{}, refresh <-chan gitStatusRefreshRequest) {
	debounce := time.NewTimer(time.Hour)
	if !debounce.Stop() {
		<-debounce.C
	}
	safety := time.NewTimer(time.Hour)
	if !safety.Stop() {
		<-safety.C
	}
	defer debounce.Stop()
	defer safety.Stop()

	resetTimer := func(timer *time.Timer, delay time.Duration) {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
	}

	mode := gitStatusModeFull
	run := func(reason string) {
		result := d.sendGitStatusUpdate(client, dir, mode)
		if result.limited {
			mode = gitStatusModeTrackedOnly
		}
		interval := gitStatusSafetyInterval
		if result.duration >= gitStatusSlowRefreshDuration || result.limited {
			interval = gitStatusSlowSafetyInterval
			d.logf("git status refresh for %s took %s via %s; limited=%v; delaying safety refresh to %s", dir, result.duration.Round(time.Millisecond), reason, result.limited, interval)
		}
		resetTimer(safety, interval)
	}

	for {
		select {
		case <-stop:
			return
		case req := <-refresh:
			if req.immediate {
				run(req.reason)
				continue
			}
			resetTimer(debounce, gitStatusRefreshDebounce)
		case <-debounce.C:
			run(gitStatusRefreshReasonDirty)
		case <-safety.C:
			run(gitStatusRefreshReasonSafety)
		}
	}
}

func (d *Daemon) sendGitStatusUpdate(client *wsClient, dir string, mode gitStatusMode) gitStatusRefreshResult {
	client.gitStatusMu.Lock()
	currentDir := client.gitStatusDir
	lastHash := client.gitStatusHash
	client.gitStatusMu.Unlock()

	if dir == "" || currentDir != dir {
		return gitStatusRefreshResult{}
	}

	status, duration, err := d.coordinator().Status(dir, mode)
	if err != nil {
		d.logf("Git status error for %s: %v", dir, err)
		return gitStatusRefreshResult{duration: duration}
	}
	status.DurationMs = protocol.Ptr(int(duration.Milliseconds()))

	newHash := hashGitStatus(status)
	if newHash == lastHash {
		return gitStatusRefreshResult{duration: duration, limited: protocol.Deref(status.Limited)}
	}

	client.gitStatusMu.Lock()
	if client.gitStatusDir != dir {
		client.gitStatusMu.Unlock()
		return gitStatusRefreshResult{duration: duration, limited: protocol.Deref(status.Limited)}
	}
	client.gitStatusHash = newHash
	client.gitStatusMu.Unlock()

	d.sendToClient(client, status)
	return gitStatusRefreshResult{duration: duration, limited: protocol.Deref(status.Limited)}
}

func (d *Daemon) refreshGitStatusSubscribersForPath(path string) {
	if d.wsHub == nil {
		return
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	d.wsHub.ForEachClient(func(client *wsClient) {
		client.gitStatusMu.Lock()
		dir := client.gitStatusDir
		client.gitStatusMu.Unlock()
		if dir == "" || !sameOrNestedPath(path, dir) {
			return
		}
		client.requestGitStatusRefresh(gitStatusRefreshRequest{reason: gitStatusRefreshReasonDirty})
	})
}

func sameOrNestedPath(path, dir string) bool {
	cleanPath := filepath.Clean(path)
	cleanDir := filepath.Clean(dir)
	if cleanPath == cleanDir {
		return true
	}
	rel, err := filepath.Rel(cleanDir, cleanPath)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (d *Daemon) handleGetFileDiffWS(client *wsClient, msg *protocol.GetFileDiffMessage) {
	d.logf("Getting file diff for %s in %s", msg.Path, msg.Directory)
	go d.handleGetFileDiff(client, msg)
}

func (d *Daemon) handleGetFileDiff(client *wsClient, msg *protocol.GetFileDiffMessage) {
	result := protocol.FileDiffResultMessage{
		Event:     protocol.EventFileDiffResult,
		Directory: msg.Directory,
		Path:      msg.Path,
		Success:   false,
	}

	baseRef := "HEAD"
	if msg.BaseRef != nil && *msg.BaseRef != "" {
		baseRef = *msg.BaseRef
	}

	staged := msg.Staged != nil && *msg.Staged
	content, err := d.coordinator().FileDiff(msg.Directory, msg.Path, baseRef, staged)
	if err != nil {
		result.Error = protocol.Ptr("Failed to read file diff: " + err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Original = content.original
	result.Modified = content.modified
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleGetBranchDiffFilesWS(client *wsClient, msg *protocol.GetBranchDiffFilesMessage) {
	d.logf("Getting branch diff files for %s", msg.Directory)
	go d.handleGetBranchDiffFiles(client, msg)
}

func (d *Daemon) handleGetBranchDiffFiles(client *wsClient, msg *protocol.GetBranchDiffFilesMessage) {
	result := protocol.BranchDiffFilesResultMessage{
		Event:     protocol.EventBranchDiffFilesResult,
		Directory: msg.Directory,
		Success:   false,
	}

	baseRef := ""
	if msg.BaseRef != nil && *msg.BaseRef != "" {
		baseRef = *msg.BaseRef
	} else {
		defaultBranch, err := d.coordinator().DefaultBranch(msg.Directory)
		if err != nil {
			result.Error = protocol.Ptr("Failed to get default branch: " + err.Error())
			d.sendToClient(client, result)
			return
		}
		baseRef = "origin/" + defaultBranch
	}
	result.BaseRef = baseRef

	snapshot, err := d.coordinator().BranchDiffSnapshot(msg.Directory, baseRef)
	if err != nil {
		result.Error = protocol.Ptr("Failed to get branch diff: " + err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Files = snapshot.files
	result.Success = true
	d.sendToClient(client, result)
}
