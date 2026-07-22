// Command agent-mirror spawns a child process under a PTY, records its
// output stream with timing, answers a minimal set of terminal queries so
// TUIs don't hang waiting for a real terminal, resizes the PTY partway
// through, and produces an analysis of the VT sequences observed before and
// after the resize.
//
// Dev-only tool to re-capture real agent vocabulary fixtures for
// internal/probetui (internal/probetui/testdata/agent-vocab-*.json); not
// shipped. Analysis uses internal/probetui/vtvocab, the same analyzer the
// probe's mirror tests run against captured fixtures.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/victorarias/attn/internal/probetui/vtvocab"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "agent-mirror: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]
	dashIdx := -1
	for i, a := range args {
		if a == "--" {
			dashIdx = i
			break
		}
	}
	if dashIdx == -1 {
		return fmt.Errorf("usage: agent-mirror [flags] -- <command> [args...]")
	}
	flagArgs := args[:dashIdx]
	cmdArgs := args[dashIdx+1:]
	if len(cmdArgs) == 0 {
		return fmt.Errorf("no command given after --")
	}

	fs := flag.NewFlagSet("agent-mirror", flag.ExitOnError)
	outDir := fs.String("out", "./capture", "output directory")
	cols := fs.Int("cols", 80, "initial cols")
	rows := fs.Int("rows", 24, "initial rows")
	cols2 := fs.Int("cols2", 62, "resize target cols")
	rows2 := fs.Int("rows2", 27, "resize target rows")
	phase1Dur := fs.Duration("phase1", 12*time.Second, "time to record before resize")
	phase2Dur := fs.Duration("phase2", 6*time.Second, "time to record after resize")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir out dir: %w", err)
	}

	rawPath := filepath.Join(*outDir, "raw.bin")
	timelinePath := filepath.Join(*outDir, "timeline.jsonl")
	phase2Path := filepath.Join(*outDir, "phase2.bin")
	analysisPath := filepath.Join(*outDir, "analysis.json")

	rawFile, err := os.Create(rawPath)
	if err != nil {
		return fmt.Errorf("create raw.bin: %w", err)
	}
	defer rawFile.Close()

	timelineFile, err := os.Create(timelinePath)
	if err != nil {
		return fmt.Errorf("create timeline.jsonl: %w", err)
	}
	defer timelineFile.Close()

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = buildEnv()

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: uint16(*rows),
		Cols: uint16(*cols),
	})
	if err != nil {
		return fmt.Errorf("start pty: %w", err)
	}

	start := time.Now()

	var (
		mu           sync.Mutex // guards rawFile/timelineFile writes and bytesWritten/boundary bookkeeping
		bytesWritten int64
		boundary     int64 = -1
	)
	var currentPhase atomic.Int32
	currentPhase.Store(1)

	// currentRows tracks the PTY row count in effect, for CPR replies.
	var currentRows atomic.Int32
	currentRows.Store(int32(*rows))

	responder := newQueryResponder(ptmx, &currentRows)

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 32*1024)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				tms := time.Since(start).Milliseconds()
				phase := currentPhase.Load()

				mu.Lock()
				rawFile.Write(chunk)
				bytesWritten += int64(n)
				fmt.Fprintf(timelineFile, "{\"t_ms\":%d,\"phase\":%d,\"bytes\":%d}\n", tms, phase, n)
				mu.Unlock()

				responder.handle(chunk)
			}
			if rerr != nil {
				return
			}
		}
	}()

	time.Sleep(*phase1Dur)

	mu.Lock()
	boundary = bytesWritten
	mu.Unlock()
	currentPhase.Store(2)
	currentRows.Store(int32(*rows2))

	if err := pty.Setsize(ptmx, &pty.Winsize{
		Rows: uint16(*rows2),
		Cols: uint16(*cols2),
	}); err != nil {
		fmt.Fprintln(os.Stderr, "agent-mirror: resize failed: "+err.Error())
	}

	time.Sleep(*phase2Dur)

	// Terminate the child: SIGTERM, then SIGKILL after 3s if still alive.
	waitDone := make(chan struct{})
	go func() {
		cmd.Wait()
		close(waitDone)
	}()
	if cmd.Process != nil {
		cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-waitDone:
	case <-time.After(3 * time.Second):
		if cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGKILL)
		}
		<-waitDone
	}

	ptmx.Close()
	<-readerDone

	rawFile.Sync()
	timelineFile.Sync()

	// Re-read raw.bin for post-hoc VT analysis, as specified.
	rawData, err := os.ReadFile(rawPath)
	if err != nil {
		return fmt.Errorf("re-read raw.bin: %w", err)
	}

	if boundary < 0 || boundary > int64(len(rawData)) {
		boundary = int64(len(rawData))
	}

	if err := os.WriteFile(phase2Path, rawData[boundary:], 0o644); err != nil {
		return fmt.Errorf("write phase2.bin: %w", err)
	}

	phase1Stats := vtvocab.Analyze(rawData[:boundary])
	phase2Stats := vtvocab.Analyze(rawData[boundary:])
	analysis := struct {
		Boundary int64         `json:"boundaryOffset"`
		Phase1   vtvocab.Stats `json:"phase1"`
		Phase2   vtvocab.Stats `json:"phase2"`
	}{Boundary: boundary, Phase1: phase1Stats, Phase2: phase2Stats}

	analysisBytes, err := json.MarshalIndent(analysis, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal analysis: %w", err)
	}
	if err := os.WriteFile(analysisPath, analysisBytes, 0o644); err != nil {
		return fmt.Errorf("write analysis.json: %w", err)
	}

	fmt.Println("agent-mirror capture complete:")
	fmt.Println("  " + rawPath)
	fmt.Println("  " + timelinePath)
	fmt.Println("  " + phase2Path)
	fmt.Println("  " + analysisPath)

	return nil
}

func buildEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, "TERM=") {
			continue
		}
		out = append(out, e)
	}
	out = append(out, "TERM=xterm-256color")
	return out
}

// --- live query responder -------------------------------------------------

var (
	reDA1       = regexp.MustCompile(`\x1b\[0?c`)
	reCPR       = regexp.MustCompile(`\x1b\[6n`)
	reOSCColorQ = regexp.MustCompile(`\x1b\](10|11|12);\?(?:\x07|\x1b\\)`)
)

type queryResponder struct {
	ptmx *os.File
	rows *atomic.Int32
	mu   sync.Mutex
}

func newQueryResponder(ptmx *os.File, rows *atomic.Int32) *queryResponder {
	return &queryResponder{ptmx: ptmx, rows: rows}
}

// handle scans a chunk of PTY output for terminal queries the child may have
// issued and writes minimal replies back into the PTY (i.e. to the child's
// stdin), so TUIs waiting on a real terminal's response don't hang. This is
// a best-effort, per-chunk byte scan; queries split across chunk boundaries
// are not reassembled.
func (q *queryResponder) handle(chunk []byte) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if reDA1.Match(chunk) {
		q.ptmx.Write([]byte("\x1b[?62c"))
	}
	if reCPR.Match(chunk) {
		r := q.rows.Load()
		q.ptmx.Write([]byte(fmt.Sprintf("\x1b[%d;1R", r)))
	}
	for _, m := range reOSCColorQ.FindAllSubmatch(chunk, -1) {
		code := string(m[1])
		q.ptmx.Write([]byte(fmt.Sprintf("\x1b]%s;rgb:0000/0000/0000\x1b\\", code)))
	}
}
