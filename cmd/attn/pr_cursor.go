package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// prWaitCursor is what a previous wait on this pull request already reported.
//
// Without it, every call baselines from the pull request's current state, so
// anything that happened between one wait returning and the next one starting is
// absorbed into the new baseline and never reported: the agent answers a comment,
// waits again, and a remark that landed while it was working is now "already
// there". The wait then blocks for its whole timeout as if the PR were quiet.
// That is the failure this file exists to prevent, and it is worse than a wrong
// exit code because a swallowed event is invisible.
//
// It therefore records the three different shapes an event comes in, because
// "seen" means something different for each:
//
//   - comments are discrete, so their IDs are the record, and one already
//     reported is never news again;
//   - a verdict supersedes itself, so the record is when it was submitted. It is
//     the baseline a later wait measures a re-review against, not a suppression:
//     an approval or changes-requested that still stands is the pull request's
//     current answer and every wait should say so;
//   - a failing check is a condition that stays true, so the record is which
//     checks failed on which commit — the same failure on the same commit is not
//     news twice, while a different one, or the same one after a push, is.
//
// The cursor advances to what a wait reported, plus the state a first-ever wait
// deliberately treats as pre-existing context — in other words, exactly what the
// caller need not be told again. Advancing it to everything a poll saw would lose
// events permanently, silently.
type prWaitCursor struct {
	CommentIDs    []string  `json:"comment_ids,omitempty"`
	VerdictAt     time.Time `json:"verdict_at,omitempty"`
	FailureHead   string    `json:"failure_head,omitempty"`
	FailureChecks []string  `json:"failure_checks,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// MarshalJSON keeps zero timestamps out of the encoded cursor. `omitempty` does
// not apply to time.Time, and a cursor echoed to a caller with
// "verdict_at":"0001-01-01T00:00:00Z" reads as a bug rather than as "no verdict
// has been reported".
func (c prWaitCursor) MarshalJSON() ([]byte, error) {
	type payload struct {
		CommentIDs    []string   `json:"comment_ids,omitempty"`
		VerdictAt     *time.Time `json:"verdict_at,omitempty"`
		FailureHead   string     `json:"failure_head,omitempty"`
		FailureChecks []string   `json:"failure_checks,omitempty"`
		UpdatedAt     *time.Time `json:"updated_at,omitempty"`
	}
	out := payload{CommentIDs: c.CommentIDs, FailureHead: c.FailureHead, FailureChecks: c.FailureChecks}
	if !c.VerdictAt.IsZero() {
		out.VerdictAt = &c.VerdictAt
	}
	if !c.UpdatedAt.IsZero() {
		out.UpdatedAt = &c.UpdatedAt
	}
	return json.Marshal(out)
}

func (c prWaitCursor) empty() bool {
	return len(c.CommentIDs) == 0 && c.VerdictAt.IsZero() && c.FailureHead == ""
}

func (c prWaitCursor) seenComments() map[string]bool {
	seen := make(map[string]bool, len(c.CommentIDs))
	for _, id := range c.CommentIDs {
		seen[id] = true
	}
	return seen
}

// sameFailure reports whether a failing-checks observation is the one already
// reported. Check order comes from the API, so compare as a set.
func (c prWaitCursor) sameFailure(head string, checks []prCheck) bool {
	if c.FailureHead != head {
		return false
	}
	names := failedCheckNames(checks)
	sort.Strings(names)
	previous := append([]string(nil), c.FailureChecks...)
	sort.Strings(previous)
	return strings.Join(names, "\n") == strings.Join(previous, "\n")
}

// prCursorFileLimit caps how many comment IDs a cursor carries. A PR's comment
// surfaces are queried newest-100 anyway, so an ID older than that window can
// never come back as unseen and keeping it would grow the file forever.
const prCursorFileLimit = 500

// prCursorMaxAge is how long a cursor outlives its last use. Each file is a few
// hundred bytes, but one per pull request ever waited on is unbounded, and
// nothing else would ever clean the directory.
const prCursorMaxAge = 30 * 24 * time.Hour

// cursorPath locates one pull request's cursor. Every segment is a legal
// filename without escaping: a GitHub owner or repository name cannot contain a
// slash and the host is a domain, which also keeps the tree readable by hand.
func cursorPath(dir string, opts prWaitOptions) string {
	host := opts.Host
	if host == "" {
		host = "github.com"
	}
	return filepath.Join(dir, host, opts.Owner, opts.Name, fmt.Sprintf("%d.json", opts.Number))
}

// loadPRWaitCursor reads the cursor for this pull request. A missing file is the
// normal first call, not an error. A file that cannot be parsed is treated the
// same way: the cursor is an optimization over re-baselining, and refusing to
// wait because of a corrupt one would be worse than losing its history.
func loadPRWaitCursor(dir string, opts prWaitOptions) (prWaitCursor, error) {
	if dir == "" {
		return prWaitCursor{}, nil
	}
	data, err := os.ReadFile(cursorPath(dir, opts))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return prWaitCursor{}, nil
		}
		return prWaitCursor{}, err
	}
	var cursor prWaitCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return prWaitCursor{}, fmt.Errorf("parse cursor %s: %w", cursorPath(dir, opts), err)
	}
	return cursor, nil
}

// savePRWaitCursor writes the cursor atomically — temp file in the same
// directory, then rename — so a wait killed mid-write leaves either the old
// cursor or the new one. A half-written file would be unparseable and would drop
// the whole history for that pull request.
func savePRWaitCursor(dir string, opts prWaitOptions, cursor prWaitCursor, now time.Time) error {
	if dir == "" {
		return nil
	}
	if len(cursor.CommentIDs) > prCursorFileLimit {
		cursor.CommentIDs = cursor.CommentIDs[len(cursor.CommentIDs)-prCursorFileLimit:]
	}
	cursor.UpdatedAt = now
	path := cursorPath(dir, opts)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cursor, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".cursor-*")
	if err != nil {
		return err
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		os.Remove(temp.Name())
		return err
	}
	if err := temp.Close(); err != nil {
		os.Remove(temp.Name())
		return err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		os.Remove(temp.Name())
		return err
	}
	prunePRWaitCursors(dir, now)
	return nil
}

// prunePRWaitCursors drops cursors nothing has touched in prCursorMaxAge. Best
// effort: a failure to prune must never fail a wait.
func prunePRWaitCursors(dir string, now time.Time) {
	cutoff := now.Add(-prCursorMaxAge)
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		if info, statErr := entry.Info(); statErr == nil && info.ModTime().Before(cutoff) {
			os.Remove(path)
		}
		return nil
	})
}
