package daemon

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

type gitCoordinator struct {
	mu sync.Mutex

	statusActive map[gitStatusCacheKey]*gitStatusRefresh

	branchDiffCache  map[branchDiffCacheKey]branchDiffSnapshot
	branchDiffActive map[branchDiffCacheKey]*branchDiffRefresh

	fileDiffActive map[fileDiffCacheKey]*fileDiffRefresh
}

var (
	getGitStatusForDaemon       = getGitStatusForSubscription
	getDefaultBranchForDaemon   = attngit.GetDefaultBranch
	getBranchDiffFilesForDaemon = attngit.GetBranchDiffFiles
	readFileDiffForDaemon       = readFileDiff
)

type gitStatusCacheKey struct {
	directory string
	mode      gitStatusMode
}

type gitStatusRefresh struct {
	done     chan struct{}
	status   *protocol.GitStatusUpdateMessage
	err      error
	duration time.Duration
}

type branchDiffCacheKey struct {
	directory string
	baseRef   string
}

type branchDiffSnapshot struct {
	baseRef string
	raw     []attngit.DiffFileInfo
	files   []protocol.BranchDiffFile
}

type branchDiffRefresh struct {
	done     chan struct{}
	snapshot branchDiffSnapshot
	err      error
}

type fileDiffCacheKey struct {
	directory string
	path      string
	baseRef   string
	staged    bool
}

type fileDiffContent struct {
	original string
	modified string
}

type fileDiffRefresh struct {
	done    chan struct{}
	content fileDiffContent
	err     error
}

func newGitCoordinator() *gitCoordinator {
	return &gitCoordinator{
		statusActive:     make(map[gitStatusCacheKey]*gitStatusRefresh),
		branchDiffCache:  make(map[branchDiffCacheKey]branchDiffSnapshot),
		branchDiffActive: make(map[branchDiffCacheKey]*branchDiffRefresh),
		fileDiffActive:   make(map[fileDiffCacheKey]*fileDiffRefresh),
	}
}

func (d *Daemon) coordinator() *gitCoordinator {
	d.gitCoordMu.Lock()
	defer d.gitCoordMu.Unlock()
	if d.gitCoord == nil {
		d.gitCoord = newGitCoordinator()
	}
	return d.gitCoord
}

func (c *gitCoordinator) Status(directory string, mode gitStatusMode) (*protocol.GitStatusUpdateMessage, time.Duration, error) {
	key := gitStatusCacheKey{directory: directory, mode: mode}

	c.mu.Lock()
	if refresh, ok := c.statusActive[key]; ok {
		done := refresh.done
		c.mu.Unlock()
		<-done
		return cloneGitStatusUpdate(refresh.status), refresh.duration, refresh.err
	}

	refresh := &gitStatusRefresh{done: make(chan struct{})}
	c.statusActive[key] = refresh
	c.mu.Unlock()

	started := time.Now()
	status, err := getGitStatusForDaemon(directory, mode)
	refresh.duration = time.Since(started)
	refresh.status = status
	refresh.err = err

	c.mu.Lock()
	if c.statusActive[key] == refresh {
		delete(c.statusActive, key)
	}
	c.mu.Unlock()
	close(refresh.done)

	return cloneGitStatusUpdate(status), refresh.duration, err
}

func (c *gitCoordinator) DefaultBranch(directory string) (string, error) {
	return getDefaultBranchForDaemon(directory)
}

func (c *gitCoordinator) BranchDiffSnapshot(directory, baseRef string) (branchDiffSnapshot, error) {
	key := branchDiffCacheKey{directory: directory, baseRef: baseRef}

	c.mu.Lock()
	if cached, ok := c.branchDiffCache[key]; ok {
		if _, running := c.branchDiffActive[key]; !running {
			refresh := &branchDiffRefresh{done: make(chan struct{})}
			c.branchDiffActive[key] = refresh
			go c.finishBranchDiffRefresh(key, refresh)
		}
		c.mu.Unlock()
		return cloneBranchDiffSnapshot(cached), nil
	}

	if refresh, ok := c.branchDiffActive[key]; ok {
		done := refresh.done
		c.mu.Unlock()
		<-done
		if refresh.err != nil {
			return branchDiffSnapshot{}, refresh.err
		}
		return cloneBranchDiffSnapshot(refresh.snapshot), nil
	}

	refresh := &branchDiffRefresh{done: make(chan struct{})}
	c.branchDiffActive[key] = refresh
	c.mu.Unlock()

	c.finishBranchDiffRefresh(key, refresh)
	if refresh.err != nil {
		return branchDiffSnapshot{}, refresh.err
	}
	return cloneBranchDiffSnapshot(refresh.snapshot), nil
}

func (c *gitCoordinator) RefreshBranchDiffFiles(directory, baseRef string) ([]attngit.DiffFileInfo, error) {
	key := branchDiffCacheKey{directory: directory, baseRef: baseRef}

	c.mu.Lock()
	if refresh, ok := c.branchDiffActive[key]; ok {
		done := refresh.done
		c.mu.Unlock()
		<-done
		if refresh.err != nil {
			return nil, refresh.err
		}
		return cloneDiffFileInfos(refresh.snapshot.raw), nil
	}

	refresh := &branchDiffRefresh{done: make(chan struct{})}
	c.branchDiffActive[key] = refresh
	c.mu.Unlock()

	c.finishBranchDiffRefresh(key, refresh)
	if refresh.err != nil {
		return nil, refresh.err
	}
	return cloneDiffFileInfos(refresh.snapshot.raw), nil
}

func (c *gitCoordinator) finishBranchDiffRefresh(key branchDiffCacheKey, refresh *branchDiffRefresh) {
	files, err := getBranchDiffFilesForDaemon(key.directory, key.baseRef)
	if err != nil {
		refresh.err = err
		c.mu.Lock()
		if c.branchDiffActive[key] == refresh {
			delete(c.branchDiffActive, key)
		}
		c.mu.Unlock()
		close(refresh.done)
		return
	}

	snapshot := branchDiffSnapshot{
		baseRef: key.baseRef,
		raw:     cloneDiffFileInfos(files),
		files:   gitDiffFilesToProtocol(files),
	}
	refresh.snapshot = snapshot

	c.mu.Lock()
	c.branchDiffCache[key] = cloneBranchDiffSnapshot(snapshot)
	if c.branchDiffActive[key] == refresh {
		delete(c.branchDiffActive, key)
	}
	c.mu.Unlock()
	close(refresh.done)
}

func (c *gitCoordinator) FileDiff(directory, path, baseRef string, staged bool) (fileDiffContent, error) {
	key := fileDiffCacheKey{directory: directory, path: path, baseRef: baseRef, staged: staged}

	c.mu.Lock()
	if refresh, ok := c.fileDiffActive[key]; ok {
		done := refresh.done
		c.mu.Unlock()
		<-done
		return refresh.content, refresh.err
	}

	refresh := &fileDiffRefresh{done: make(chan struct{})}
	c.fileDiffActive[key] = refresh
	c.mu.Unlock()

	refresh.content, refresh.err = readFileDiffForDaemon(directory, path, baseRef, staged)

	c.mu.Lock()
	if c.fileDiffActive[key] == refresh {
		delete(c.fileDiffActive, key)
	}
	c.mu.Unlock()
	close(refresh.done)

	return refresh.content, refresh.err
}

func readFileDiff(directory, path, baseRef string, staged bool) (fileDiffContent, error) {
	content := fileDiffContent{}

	origOutput, origErr := attngit.Output(attngit.OpDiff, directory, "show", baseRef+":"+path)
	if origErr == nil {
		content.original = string(origOutput)
	}

	if staged {
		stagedOutput, err := attngit.Output(attngit.OpDiff, directory, "show", ":"+path)
		if err != nil {
			return fileDiffContent{}, err
		}
		content.modified = string(stagedOutput)
		return content, nil
	}

	filePath := filepath.Join(directory, path)
	modified, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			content.modified = ""
			return content, nil
		}
		return fileDiffContent{}, err
	}
	content.modified = string(modified)
	return content, nil
}

func gitDiffFilesToProtocol(files []attngit.DiffFileInfo) []protocol.BranchDiffFile {
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
	return protoFiles
}

func cloneGitStatusUpdate(status *protocol.GitStatusUpdateMessage) *protocol.GitStatusUpdateMessage {
	if status == nil {
		return nil
	}
	cloned := *status
	cloned.Staged = cloneGitFileChanges(status.Staged)
	cloned.Unstaged = cloneGitFileChanges(status.Unstaged)
	cloned.Untracked = cloneGitFileChanges(status.Untracked)
	return &cloned
}

func cloneGitFileChanges(files []protocol.GitFileChange) []protocol.GitFileChange {
	if files == nil {
		return nil
	}
	cloned := make([]protocol.GitFileChange, len(files))
	copy(cloned, files)
	return cloned
}

func cloneBranchDiffSnapshot(snapshot branchDiffSnapshot) branchDiffSnapshot {
	snapshot.raw = cloneDiffFileInfos(snapshot.raw)
	snapshot.files = cloneBranchDiffFiles(snapshot.files)
	return snapshot
}

func cloneDiffFileInfos(files []attngit.DiffFileInfo) []attngit.DiffFileInfo {
	if files == nil {
		return nil
	}
	cloned := make([]attngit.DiffFileInfo, len(files))
	copy(cloned, files)
	return cloned
}

func cloneBranchDiffFiles(files []protocol.BranchDiffFile) []protocol.BranchDiffFile {
	if files == nil {
		return nil
	}
	cloned := make([]protocol.BranchDiffFile, len(files))
	copy(cloned, files)
	return cloned
}
