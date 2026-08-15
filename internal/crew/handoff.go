package crew

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Filing a member's letter. A handoff is the member's day-line: written by the
// agent in its own words at the close of a day, addressed to whoever wakes as
// this member next. attn never generates or edits the prose — it decides the
// name, refuses anything that would overwrite, and hands back where it landed.
//
// The line is append-only because a filed letter is somebody's honest closure:
// a correction is a new letter, never a rewrite of one that has already been
// read. The name is minted here so every letter sorts chronologically by name,
// which is what makes the freshest one findable without reading any of them.

// HandoffStampLayout is a letter's name stamp: UTC, to the minute, the shape
// the simulation's 23 filed letters already use (`2026-08-13T22-20Z`).
// Lexicographic order over these names is chronological order, which is the
// whole reason the freshest-letter read is a sort and not a stat of every file.
const HandoffStampLayout = "2006-01-02T15-04Z"

// MaxHandoffBytes bounds one letter. Measured 2026-08-14 over the simulation's
// 23 filed letters: the largest is 6,601 bytes. This is a tripwire only
// something that is not a letter touches — a pasted transcript, a spilled log —
// and the refusal names both numbers.
const MaxHandoffBytes = 64000

// HandoffFileName is the name a letter filed at `at` takes.
func HandoffFileName(member string, at time.Time) string {
	return at.UTC().Format(HandoffStampLayout) + "-" + member + ".md"
}

// ErrHandoffExists is the append-only refusal: a letter is already filed under
// the name this one would take.
var ErrHandoffExists = errors.New("a letter is already filed under that name")

// FileHandoff writes one letter into a member's home and returns where it
// landed. It refuses an empty note (there is no letter to file), a note past
// the limit, and — the append-only rule — any name already taken. Nothing is
// created on a refusal past the handoffs directory itself.
func FileHandoff(homeDir, member, note string, at time.Time) (string, error) {
	if err := ValidateHandoffNote(note); err != nil {
		return "", err
	}
	dir := filepath.Join(homeDir, HandoffsDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("making %s's handoffs directory at %s: %w", DisplayName(member), dir, err)
	}
	path := filepath.Join(dir, HandoffFileName(member, at))
	// O_EXCL is the enforcement, not a check before one: two letters racing for
	// the same minute cannot both land, and neither can a name a hand-written
	// file already holds.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return "", fmt.Errorf("%w: %s — a filed letter is never overwritten, so file the correction as its own letter a minute from now", ErrHandoffExists, path)
		}
		return "", fmt.Errorf("filing %s's letter at %s: %w", DisplayName(member), path, err)
	}
	defer file.Close()
	if _, err := file.WriteString(ensureTrailingNewline(note)); err != nil {
		return "", fmt.Errorf("writing %s's letter at %s: %w", DisplayName(member), path, err)
	}
	return path, nil
}

// ValidateHandoffNote judges a letter's text alone, so a caller can refuse
// before anything reaches disk.
func ValidateHandoffNote(note string) error {
	if strings.TrimSpace(note) == "" {
		return errors.New("a handoff is the letter you write to your successor; there is nothing to file")
	}
	if len(note) > MaxHandoffBytes {
		return fmt.Errorf("this letter is %d bytes and one letter's limit is %d — the longest letter ever filed is 6,601, so this is something other than a letter", len(note), MaxHandoffBytes)
	}
	return nil
}

func ensureTrailingNewline(note string) string {
	if note == "" || note[len(note)-1] == '\n' {
		return note
	}
	return note + "\n"
}
