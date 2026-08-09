package pty

import (
	"fmt"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Shell state signals: "is a command running" for panes with no harness. Two
// independent readings: the kernel's foreground process group (TIOCGPGRP on
// the PTY master, a level polled 1/s) and OSC 133 markers when shell
// integration is present (C = busy edge, D/A = not-busy edges; edges catch
// commands shorter than a poll and pass through ssh). They disagree in one
// case — a foreground program containing an inner shell at its prompt —
// reconciled by one rule: a prompt marker means "at prompt" only while the
// same foreground program it arrived from keeps the foreground. Honest limit:
// without integration, an idle vim reads as busy.

const (
	// shellForegroundPollInterval: one ioctl per second per shell pane; the
	// resolver ticks at the same rate, so polling faster buys nothing.
	shellForegroundPollInterval = time.Second

	// shellCommandDetailLimit bounds the cmdline carried into observation
	// detail (trace/evidence strings, not UI).
	shellCommandDetailLimit = 80
)

// shellSignalArbiter merges the foreground poll and the OSC 133 marker stream
// into one heartbeat claim. Shared by two goroutines — the poller and the read
// loop — hence the lock.
type shellSignalArbiter struct {
	mu sync.Mutex

	// shellPgid is the shell's own process group — the foreground owner that
	// means "at the prompt". Constant for the session's life.
	shellPgid int

	// seg is this arbiter's own feed segmenter, run over the RAW chunk
	// independently of wireFeeder's — same machine, same framing, no
	// dependence on the wire path's state. Only marker emissions are taken.
	seg feedSegmenter

	// markerAtPrompt says the most recent marker verdict was a prompt;
	// ownerPgid is the foreground program it arrived from (0 when the shell
	// itself held the foreground, where the poll already agrees).
	markerAtPrompt bool
	ownerPgid      int
	// lastPolledFgPgid is what the poller last saw; a prompt marker binds its
	// ownership to it.
	lastPolledFgPgid int

	lastClaim string
	lastEmit  time.Time
}

func newShellSignalArbiter(shellPgid int) *shellSignalArbiter {
	return &shellSignalArbiter{shellPgid: shellPgid}
}

// ObservePoll folds one foreground reading into the merged claim.
func (a *shellSignalArbiter) ObservePoll(fgPgid int, now time.Time) (Observation, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastPolledFgPgid = fgPgid

	var claim, detail string
	switch {
	case fgPgid == a.shellPgid:
		// The shell holding its own prompt is definitive.
		claim, detail = claimNotBusy, "shell at prompt"
		a.markerAtPrompt, a.ownerPgid = false, 0
	case a.markerAtPrompt && fgPgid == a.ownerPgid:
		// An inner shell (ssh, nested shell) is sitting at its prompt.
		claim, detail = claimNotBusy, "inner shell at prompt"
	default:
		claim, detail = claimBusy, "foreground command running"
		// A different program took the foreground; the verdict is stale.
		a.markerAtPrompt, a.ownerPgid = false, 0
	}
	return a.emit(claim, detail, now)
}

// ObserveOutput scans one chunk of PTY output for OSC 133 markers and folds
// them into the merged claim, oldest first. It never modifies the chunk.
func (a *shellSignalArbiter) ObserveOutput(chunk []byte, now time.Time) []Observation {
	if a == nil || len(chunk) == 0 {
		return nil
	}
	var out []Observation
	// Marker handling shares claim state with the poller, so hold the lock
	// across the feed.
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seg.Feed(chunk, func(seg feedSegment) {
		if seg.Marker == nil {
			return
		}
		if obs, ok := a.observeMarker(seg.Marker, now); ok {
			out = append(out, obs)
		}
	})
	return out
}

// observeMarker folds one marker into the merged claim. Caller holds mu.
// Command-start/end are edges — once per command, carrying what a level cannot
// — so they skip the keepalive dedup. Prompt-start is a level restate and
// keeps it: shells redraw prompts freely.
func (a *shellSignalArbiter) observeMarker(m *osc133Marker, now time.Time) (Observation, bool) {
	switch m.Kind {
	case osc133PreExec:
		a.markerAtPrompt, a.ownerPgid = false, 0
		detail := "command started"
		if m.Cmdline != nil && *m.Cmdline != "" {
			detail = "command started: " + truncateDetail(*m.Cmdline, shellCommandDetailLimit)
		}
		return a.emitEdge(claimBusy, detail, now), true
	case osc133CommandEnd:
		a.bindPromptVerdict()
		detail := "command finished"
		if m.ExitCode != nil {
			detail = fmt.Sprintf("command exited %d", *m.ExitCode)
		}
		return a.emitEdge(claimNotBusy, detail, now), true
	case osc133PromptStart:
		a.bindPromptVerdict()
		return a.emit(claimNotBusy, "shell at prompt", now)
	default:
		return Observation{}, false
	}
}

// bindPromptVerdict pins a prompt verdict to the LAST polled foreground
// program, which keeps the verdict off a command started right after the
// prompt: the new command's fresh pgid won't match, and the poll rules again.
func (a *shellSignalArbiter) bindPromptVerdict() {
	a.markerAtPrompt = true
	a.ownerPgid = 0
	if a.lastPolledFgPgid != 0 && a.lastPolledFgPgid != a.shellPgid {
		a.ownerPgid = a.lastPolledFgPgid
	}
}

// emit applies the change-or-keepalive rule. Caller holds mu.
func (a *shellSignalArbiter) emit(claim, detail string, now time.Time) (Observation, bool) {
	if claim == a.lastClaim && now.Sub(a.lastEmit) < heartbeatKeepalive {
		return Observation{}, false
	}
	return a.emitEdge(claim, detail, now), true
}

// emitEdge emits unconditionally, still feeding the dedup state so a level
// restate right after an edge stays suppressed. Caller holds mu.
func (a *shellSignalArbiter) emitEdge(claim, detail string, now time.Time) Observation {
	a.lastClaim = claim
	a.lastEmit = now
	return newObservation(SourceHeartbeat, claim, detail, now)
}

func truncateDetail(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

// runShellForegroundPoller reads the foreground owner on a ticker and reports
// level changes through onState until the session exits. Its own goroutine,
// spawned only for shell panes.
func (s *Session) runShellForegroundPoller(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.exited:
			return
		case <-ticker.C:
			fgPgid, ok := s.foregroundProcessGroup()
			if !ok {
				// fd gone or ioctl failed; the last level stands.
				continue
			}
			if obs, ok := s.shellSignals.ObservePoll(fgPgid, time.Now()); ok {
				s.emitSignal(obs)
			}
		}
	}
}

// childProcessGroup is the spawned shell's own process group. The spawn starts
// the child as session leader (Setsid), so its group id is its pid; asking the
// kernel first covers any arrangement where it is not.
func (s *Session) childProcessGroup() int {
	pid := s.cmd.Process.Pid
	if pgid, err := syscall.Getpgid(pid); err == nil && pgid > 0 {
		return pgid
	}
	return pid
}

// foregroundProcessGroup reads which process group owns the terminal's
// foreground right now. Takes writeMu for the same reason resize does: Fd()
// must not race the ptmx close.
func (s *Session) foregroundProcessGroup() (int, bool) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.ptmxClosed || s.ptmx == nil {
		return 0, false
	}
	pgid, err := unix.IoctlGetInt(int(s.ptmx.Fd()), unix.TIOCGPGRP)
	if err != nil || pgid <= 0 {
		return 0, false
	}
	return pgid, true
}
