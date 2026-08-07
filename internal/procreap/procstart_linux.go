package procreap

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// stampResolution is how finely a stamp from this platform separates two
// births: USER_HZ = 100, so one clock tick. Receipt that this is enough — reuse
// needs the pid space to wrap, and on the Linux target (pid_max 4194304) four
// minutes of nothing but forking — 1949409 spawns at 8123/s — produced no reuse
// at all; a wrap needs roughly 8.6 minutes at that rate. The stamp moves five
// orders of magnitude faster than the floor it has to beat. Two processes born
// inside the same tick do share a stamp, which is why the gate's property is
// resolution-versus-reuse and not "any two processes differ".
const stampResolution = 10 * time.Millisecond

// processStartTime reads /proc/<pid>/stat's starttime field: clock ticks since
// boot. This is the canonical Linux process identity stamp and the kernel
// exposes nothing finer.
func processStartTime(pid int) (string, error) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", fmt.Errorf("read start time of pid %d: %w", pid, err)
	}
	// comm (field 2) is an arbitrary string in parentheses; everything after
	// the closing paren is space-separated. starttime is field 22 overall, so
	// the 20th after the state field that follows the paren.
	rest := string(raw)
	i := strings.LastIndexByte(rest, ')')
	if i < 0 {
		return "", fmt.Errorf("unparseable /proc/%d/stat", pid)
	}
	fields := strings.Fields(rest[i+1:])
	if len(fields) < 20 {
		return "", fmt.Errorf("unparseable /proc/%d/stat: %d fields after comm", pid, len(fields))
	}
	return fields[19], nil
}
