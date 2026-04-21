// Probe: spawn an agent binary in a raw PTY, capture every byte it emits,
// and drive a configurable resize sequence. Answers the question "is the
// agent actually emitting redraw bytes in response to SIGWINCH?" without
// any terminal emulator between the PTY and the observer — so whatever the
// probe sees is upstream of xterm.js, ghostty, or attn's forwarding. When
// a pane's content looks stale after a resize, run this to find out
// whether the agent is silent or whether something downstream is losing
// the bytes.
//
// Usage:
//
//	go run ./tools/pty-resize-probe              # defaults: claude, ./probe.log
//	AGENT=codex go run ./tools/pty-resize-probe  # other agent
//	LOG=/tmp/x.log go run ./tools/pty-resize-probe
//
// The scripted sequence below mirrors scenario-tr205 (wide→narrow grow/
// shrink at ~400 ms spacing) because that's the flake it was first built
// to diagnose. Edit the `seq` slice for other scenarios.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
)

func main() {
	agent := os.Getenv("AGENT")
	if agent == "" {
		agent = "claude"
	}
	logPath := os.Getenv("LOG")
	if logPath == "" {
		logPath = "probe.log"
	}

	cmd := exec.Command(agent)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 118, Rows: 23})
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
	}()

	log, err := os.Create(logPath)
	if err != nil {
		panic(err)
	}
	defer log.Close()

	dataCh := make(chan []byte, 1024)
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				b := make([]byte, n)
				copy(b, buf[:n])
				dataCh <- b
			}
			if err != nil {
				close(dataCh)
				return
			}
		}
	}()

	logf := func(format string, args ...any) {
		ts := time.Now().Format("15:04:05.000")
		line := fmt.Sprintf("[%s] "+format+"\n", append([]any{ts}, args...)...)
		fmt.Print(line)
		log.WriteString(line)
	}

	collect := func(d time.Duration) []byte {
		var out []byte
		timer := time.After(d)
		for {
			select {
			case b, ok := <-dataCh:
				if !ok {
					return out
				}
				out = append(out, b...)
			case <-timer:
				return out
			}
		}
	}

	logf("baseline: draining 6s at 126x51 (agent=%s)", agent)
	_ = pty.Setsize(ptmx, &pty.Winsize{Cols: 126, Rows: 51})
	baseline := collect(6 * time.Second)
	logf("baseline bytes=%d", len(baseline))
	_, _ = fmt.Fprintf(log, "baseline payload (hex):\n%x\n\n", baseline)

	// Mimic scenario TR-205 close-pane grow sequence at ~400ms between resizes:
	//   126x51 → 62x49 → 40x49 → 30x49 → 40x49 → 62x49 → 126x51
	seq := []struct{ cols, rows uint16 }{
		{62, 49},
		{40, 49},
		{30, 49},
		{40, 49},
		{62, 49},
		{126, 51},
	}
	for i, r := range seq {
		logf("RESIZE #%d -> %dx%d", i+1, r.cols, r.rows)
		if err := pty.Setsize(ptmx, &pty.Winsize{Cols: r.cols, Rows: r.rows}); err != nil {
			logf("  Setsize err: %v", err)
		}
		// mirror scenario: ~400ms between resizes, tiny window for the agent to redraw
		out := collect(400 * time.Millisecond)
		logf("  after #%d (400ms window) bytes=%d", i+1, len(out))
		_, _ = fmt.Fprintf(log, "post-resize#%d payload (hex):\n%x\n\n", i+1, out)
	}

	// After burst, drain for 5 more seconds to see if the agent catches up.
	logf("final drain 5s after last resize")
	tail := collect(5 * time.Second)
	logf("tail bytes=%d", len(tail))
	_, _ = fmt.Fprintf(log, "tail payload (hex):\n%x\n\n", tail)

	logf("done; log at %s", logPath)
}
