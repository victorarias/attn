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

	fileDiffActive map[fileDiffCacheKey]*fileDiffRefresh
}

var (
	getGitStatusForDaemon     = getGitStatusForSubscription
	getDefaultBranchForDaemon = attngit.GetDefaultBranch
	readFileDiffForDaemon     = readFileDiff
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

type fileDiffCacheKey struct {
	directory string
	path      string
	baseRef   string
	headRef   string
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
		statusActive:   make(map[gitStatusCacheKey]*gitStatusRefresh),
		fileDiffActive: make(map[fileDiffCacheKey]*fileDiffRefresh),
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

func (c *gitCoordinator) FileDiff(directory, path, baseRef, headRef string, staged bool) (fileDiffContent, error) {
	key := fileDiffCacheKey{directory: directory, path: path, baseRef: baseRef, headRef: headRef, staged: staged}

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

	refresh.content, refresh.err = readFileDiffForDaemon(directory, path, baseRef, headRef, staged)

	c.mu.Lock()
	if c.fileDiffActive[key] == refresh {
		delete(c.fileDiffActive, key)
	}
	c.mu.Unlock()
	close(refresh.done)

	return refresh.content, refresh.err
}

func readFileDiff(directory, path, baseRef, headRef string, staged bool) (fileDiffContent, error) {
	content := fileDiffContent{}

	origOutput, origErr := attngit.Output(attngit.OpDiff, directory, "show", baseRef+":"+path)
	if origErr == nil {
		content.original = string(origOutput)
	}

	// When head_ref is set (presentation reader diffs), the modified side is
	// pinned to that ref instead of the working tree, and staged is ignored.
	if headRef != "" {
		headOutput, err := attngit.Output(attngit.OpDiff, directory, "show", headRef+":"+path)
		if err != nil {
			content.modified = ""
			return content, nil
		}
		content.modified = string(headOutput)
		return content, nil
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
