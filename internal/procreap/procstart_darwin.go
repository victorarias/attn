package procreap

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// processStartTime reads the process's birth time from
// sysctl kern.proc.pid.<pid> — kinfo_proc's p_starttime, a timeval, so
// microsecond resolution.
//
// `ps -o lstart=` is the obvious alternative and is the wrong tool: Darwin's
// ps(1) formats lstart with strftime %c, whole seconds and nothing below.
// Receipt for preferring the sysctl: pid reuse needs the pid space to wrap, and
// on this machine (M-series, kern.maxproc 8000, ceiling ~99998) the first reuse
// took 2m37s of nothing but forking — 92043 spawns at 588/s. So a 1-second
// stamp was not in fact a coin flip; it had two orders of magnitude of margin
// at that fork rate. But that margin is a timing coincidence measured on one
// idle laptop: it shrinks with a faster forker and with a lowered pid ceiling,
// and nothing in the system would tell us when it got thin. The gate that stops
// this package from signalling a stranger should not rest on that, and 1µs —
// eight orders of magnitude under the measured floor — costs nothing.
const stampResolution = time.Microsecond
func processStartTime(pid int) (string, error) {
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", fmt.Errorf("read start time of pid %d: %w", pid, err)
	}
	start := proc.Proc.P_starttime
	if start.Sec == 0 && start.Usec == 0 {
		return "", fmt.Errorf("pid %d reports no start time", pid)
	}
	return fmt.Sprintf("%d.%06d", start.Sec, start.Usec), nil
}
