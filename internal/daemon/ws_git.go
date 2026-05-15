package daemon

import (
	"os"
	"path/filepath"
	"time"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

const gitStatusPollInterval = 2 * time.Second

func (d *Daemon) handleSubscribeGitStatus(client *wsClient, msg *protocol.SubscribeGitStatusMessage) {
	client.stopGitStatusPoll()

	client.gitStatusMu.Lock()
	client.gitStatusDir = msg.Directory
	client.gitStatusStop = make(chan struct{})
	client.gitStatusTicker = time.NewTicker(gitStatusPollInterval)
	stopChan := client.gitStatusStop
	ticker := client.gitStatusTicker
	client.gitStatusMu.Unlock()

	go d.sendGitStatusUpdate(client)

	go func() {
		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				d.sendGitStatusUpdate(client)
			}
		}
	}()
}

func (d *Daemon) handleUnsubscribeGitStatusWS(client *wsClient) {
	d.logf("Unsubscribing from git status")
	client.stopGitStatusPoll()
}

func (d *Daemon) sendGitStatusUpdate(client *wsClient) {
	client.gitStatusMu.Lock()
	dir := client.gitStatusDir
	lastHash := client.gitStatusHash
	client.gitStatusMu.Unlock()

	if dir == "" {
		return
	}

	status, err := getGitStatus(dir)
	if err != nil {
		d.logf("Git status error for %s: %v", dir, err)
		return
	}

	newHash := hashGitStatus(status)
	if newHash == lastHash {
		return
	}

	client.gitStatusMu.Lock()
	client.gitStatusHash = newHash
	client.gitStatusMu.Unlock()

	d.sendToClient(client, status)
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
