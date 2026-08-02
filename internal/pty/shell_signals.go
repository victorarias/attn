package pty

import (
	"fmt"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Shell state signals: the terminal's own answer to "is a command running",
// for sessions with no harness to ask.
//
// A shell pane has none of the signals the agents provide — no hooks, no
// transcript, no title glyph contract — but two independent readings exist.
//
// The first is the kernel's: which process group owns the terminal's
// foreground. An interactive shell sitting at its prompt holds the foreground
// itself; the moment it runs a command, job control hands the foreground to
// the command's process group and takes it back when the command ends.
// TIOCGPGRP on the PTY master reads that directly, so it works identically for
// every shell with job control and needs nothing injected into the user's
// shell. It is a level, polled once a second.
//
// The second is the shell's own, when shell integration is present: OSC 133
// markers in the output stream. C (pre-exec) is a busy edge the instant a
// command starts, D (command end) is a not-busy edge carrying the exit code,
// A (prompt start) is a not-busy edge. Edges are immediate — a command that
// starts and finishes between two polls is still seen — and they come from
// whichever shell the user is actually typing at, including one on the far
// side of an ssh session whose remote integration passes markers through.
//
// The two disagree in exactly one situation: a foreground program that
// contains an inner shell at its prompt (ssh at a remote prompt, a nested
// shell). The poll reads busy — a program does own the foreground — while the
// markers say the user is at a prompt. shellSignalArbiter reconciles them with
// one rule: a prompt marker keeps meaning "at prompt" only while the same
// foreground program it arrived from stays in the foreground. The shell
// itself taking the foreground back, or the foreground moving to a different
// program, returns the verdict to the poll.
//
// One honest limit remains: without integration, a foreground program that is
// itself waiting (an idle vim) reads as busy, because from this terminal's
// point of view a program does own the foreground.

const (
	// shellForegroundPollInterval is how often the foreground owner is read.
	// One ioctl per second per shell pane; the resolver ticks at the same rate,
	// so polling faster buys nothing.
	shellForegroundPollInterval = time.Second

	// shellCommandDetailLimit bounds how much of a marker's cmdline is carried
	// into the observation detail (trace/evidence strings, not UI).
	shellCommandDetailLimit = 80
)

// shellSignalArbiter merges the foreground poll and the OSC 133 marker stream
// into one coherent heartbeat claim. It is shared by two goroutines — the
// poller and the session read loop — hence the lock; emission keeps the
// change-or-keepalive rule the harness title observer uses.
type shellSignalArbiter struct {
	mu sync.Mutex

	// shellPgid is the shell's own process group — the foreground owner that
	// means "at the prompt". Constant for the session's life: the shell is the
	// PTY's session leader and never changes group.
	shellPgid int

	seg osc133ScanSegmenter

	// markerAtPrompt says the most recent marker verdict was a prompt; ownerPgid
	// is the foreground program that verdict arrived from (0 when it arrived
	// while the shell itself held the foreground, in which case the poll's own
	// shell-pgid reading already agrees and no override is needed).
	markerAtPrompt bool
	ownerPgid      int
	// lastPolledFgPgid is what the poller last saw; it is what a prompt marker
	// binds its ownership to.
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
		// The shell reading its own prompt is definitive: whatever an inner
		// shell said before, the user is typing at this one now.
		claim, detail = claimNotBusy, "shell at prompt"
		a.markerAtPrompt, a.ownerPgid = false, 0
	case a.markerAtPrompt && fgPgid == a.ownerPgid:
		// The program the prompt marker came from still owns the foreground:
		// an inner shell (ssh, a nested shell) is sitting at its prompt.
		claim, detail = claimNotBusy, "inner shell at prompt"
	default:
		claim, detail = claimBusy, "foreground command running"
		// A different program took the foreground since the marker's verdict;
		// the verdict no longer describes what is running.
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
	// The segmenter is only ever fed from the read loop, but marker handling
	// shares claim state with the poller, so take the lock across the feed.
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seg.Feed(chunk, func(_ []byte, m *osc133Marker) {
		if m == nil {
			return
		}
		if obs, ok := a.observeMarker(m, now); ok {
			out = append(out, obs)
		}
	})
	return out
}

// observeMarker folds one marker into the merged claim. Caller holds mu.
//
// Command-start and command-end are edges, not level repaints: each one is a
// distinct event carrying information a level cannot (the exit code, the
// cmdline), and they arrive once per command rather than at a repaint rate —
// so they skip the keepalive dedup. Prompt-start is a level restate and keeps
// it: shells redraw prompts freely, and a D;exit already announced the edge.
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

// bindPromptVerdict records a prompt verdict and pins it to the foreground
// program it arrived from. Binding to the last polled reading — rather than to
// "whatever is foreground at the next poll" — is what keeps the verdict from
// leaking onto a command the user starts immediately after the prompt: a new
// command gets a fresh pgid, which won't match, and the poll rules again.
func (a *shellSignalArbiter) bindPromptVerdict() {
	a.markerAtPrompt = true
	a.ownerPgid = 0
	if a.lastPolledFgPgid != 0 && a.lastPolledFgPgid != a.shellPgid {
		a.ownerPgid = a.lastPolledFgPgid
	}
}

// emit applies the change-or-keepalive rule: a level re-states itself only
// when it changed or when "still true" is old enough to be news. Caller holds
// mu.
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
// level changes through onState until the session exits. It runs in its own
// goroutine, spawned only for shell panes.
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
				// The fd is gone or the ioctl failed; whatever the last level
				// was still stands, and process-exit evidence outranks it.
				continue
			}
			if obs, ok := s.shellSignals.ObservePoll(fgPgid, time.Now()); ok {
				s.onState(obs)
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
// foreground right now. It takes writeMu for the same reason resize does:
// Fd() must not race the ptmx close.
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
