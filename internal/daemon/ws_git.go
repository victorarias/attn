package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/git"
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

var (
	gitStatusRefreshDebounce     = 1 * time.Second
	gitStatusSafetyInterval      = 30 * time.Second
	gitStatusSlowSafetyInterval  = 2 * time.Minute
	gitStatusSlowRefreshDuration = 5 * time.Second
	getGitStatusForDaemon        = getGitStatus
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

	run := func(reason string) {
		duration := d.sendGitStatusUpdate(client, dir)
		interval := gitStatusSafetyInterval
		if duration >= gitStatusSlowRefreshDuration {
			interval = gitStatusSlowSafetyInterval
			d.logf("git status refresh for %s took %s via %s; delaying safety refresh to %s", dir, duration.Round(time.Millisecond), reason, interval)
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

func (d *Daemon) sendGitStatusUpdate(client *wsClient, dir string) time.Duration {
	client.gitStatusMu.Lock()
	currentDir := client.gitStatusDir
	lastHash := client.gitStatusHash
	client.gitStatusMu.Unlock()

	if dir == "" || currentDir != dir {
		return 0
	}

	started := time.Now()
	status, err := getGitStatusForDaemon(dir)
	duration := time.Since(started)
	if err != nil {
		d.logf("Git status error for %s: %v", dir, err)
		return duration
	}

	newHash := hashGitStatus(status)
	if newHash == lastHash {
		return duration
	}

	client.gitStatusMu.Lock()
	if client.gitStatusDir != dir {
		client.gitStatusMu.Unlock()
		return duration
	}
	client.gitStatusHash = newHash
	client.gitStatusMu.Unlock()

	d.sendToClient(client, status)
	return duration
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

	origOutput, origErr := git.Output(git.OpDiff, msg.Directory, "show", baseRef+":"+msg.Path)

	var original string
	if origErr == nil {
		original = string(origOutput)
	}

	var modified string
	if msg.Staged != nil && *msg.Staged {
		stagedOutput, err := git.Output(git.OpDiff, msg.Directory, "show", ":"+msg.Path)
		if err != nil {
			result.Error = protocol.Ptr("Failed to read staged file: " + err.Error())
			d.sendToClient(client, result)
			return
		}
		modified = string(stagedOutput)
	} else {
		filePath := filepath.Join(msg.Directory, msg.Path)
		content, err := os.ReadFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				modified = ""
			} else {
				result.Error = protocol.Ptr("Failed to read file: " + err.Error())
				d.sendToClient(client, result)
				return
			}
		} else {
			modified = string(content)
		}
	}

	result.Original = original
	result.Modified = modified
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
		defaultBranch, err := git.GetDefaultBranch(msg.Directory)
		if err != nil {
			result.Error = protocol.Ptr("Failed to get default branch: " + err.Error())
			d.sendToClient(client, result)
			return
		}
		baseRef = "origin/" + defaultBranch
	}
	result.BaseRef = baseRef

	files, err := git.GetBranchDiffFiles(msg.Directory, baseRef)
	if err != nil {
		result.Error = protocol.Ptr("Failed to get branch diff: " + err.Error())
		d.sendToClient(client, result)
		return
	}

	protoFiles := make([]protocol.BranchDiffFile, len(files))
	for i, f := range files {
		protoFiles[i] = protocol.BranchDiffFile{
			Path:   f.Path,
			Status: f.Status,
		}
		if f.OldPath != "" {
			protoFiles[i].OldPath = &f.OldPath
		}
		if f.Additions > 0 {
			protoFiles[i].Additions = &f.Additions
		}
		if f.Deletions > 0 {
			protoFiles[i].Deletions = &f.Deletions
		}
		if f.HasUncommitted {
			protoFiles[i].HasUncommitted = &f.HasUncommitted
		}
	}

	result.Files = protoFiles
	result.Success = true
	d.sendToClient(client, result)
}
