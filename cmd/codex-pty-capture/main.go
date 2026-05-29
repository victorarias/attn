package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	creackpty "github.com/creack/pty"
)

type eventLogger struct {
	mu sync.Mutex
	f  *os.File
}

func newEventLogger(path string) (*eventLogger, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &eventLogger{f: f}, nil
}

func (l *eventLogger) close() error {
	if l == nil || l.f == nil {
		return nil
	}
	return l.f.Close()
}

func (l *eventLogger) event(kind string, fields map[string]any) {
	if l == nil || l.f == nil {
		return
	}
	entry := map[string]any{
		"at":    time.Now().Format(time.RFC3339Nano),
		"event": kind,
	}
	for k, v := range fields {
		entry[k] = v
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.f.Write(append(data, '\n'))
}

func defaultOutDir() string {
	base := os.Getenv("TMPDIR")
	if base == "" {
		base = os.TempDir()
	}
	stamp := time.Now().Format("20060102-150405")
	return filepath.Join(base, "codex-pty-capture-"+stamp)
}

func terminalSize(tty *os.File) map[string]any {
	size, err := creackpty.GetsizeFull(tty)
	if err != nil || size == nil {
		return map[string]any{"error": errString(err)}
	}
	return map[string]any{
		"rows": uint16(size.Rows),
		"cols": uint16(size.Cols),
		"x":    uint16(size.X),
		"y":    uint16(size.Y),
	}
}

func currentWinsize(tty *os.File) *creackpty.Winsize {
	size, err := creackpty.GetsizeFull(tty)
	if err != nil || size == nil || size.Cols == 0 || size.Rows == 0 {
		return &creackpty.Winsize{Cols: 80, Rows: 24}
	}
	return size
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func controlFlags(data []byte) map[string]bool {
	return map[string]bool{
		"sync_start": bytesContains(data, []byte("\x1b[?2026h")),
		"sync_end":   bytesContains(data, []byte("\x1b[?2026l")),
		"clear":      bytesContains(data, []byte("\x1b[2J")) || bytesContains(data, []byte("\x1b[3J")),
		"alt_on":     bytesContains(data, []byte("\x1b[?1049h")),
		"alt_off":    bytesContains(data, []byte("\x1b[?1049l")),
	}
}

func bytesContains(data, needle []byte) bool {
	if len(needle) == 0 || len(data) < len(needle) {
		return false
	}
	for i := 0; i <= len(data)-len(needle); i++ {
		if string(data[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}

func previewBytes(data []byte, limit int) string {
	if limit <= 0 || len(data) == 0 {
		return ""
	}
	if len(data) > limit {
		data = data[:limit]
	}
	var b strings.Builder
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			fmt.Fprintf(&b, "\\x%02x", data[0])
			data = data[1:]
			continue
		}
		switch r {
		case '\x1b':
			b.WriteString("\\x1b")
		case '\r':
			b.WriteString("\\r")
		case '\n':
			b.WriteString("\\n")
		case '\t':
			b.WriteString("\\t")
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, "\\x%02x", r)
			} else {
				b.WriteRune(r)
			}
		}
		data = data[size:]
	}
	return b.String()
}

func commandFromArgs(args []string) []string {
	if len(args) == 0 {
		return []string{"codex"}
	}
	return args
}

func runStty(tty *os.File, args ...string) error {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = tty
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func sttyOutput(tty *os.File, args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = tty
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func main() {
	os.Exit(run())
}

func run() int {
	var outDir string
	var captureInput bool
	flag.StringVar(&outDir, "out", defaultOutDir(), "directory for output.raw and events.jsonl")
	flag.BoolVar(&captureInput, "capture-input", false, "also record stdin bytes to input.raw")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s [flags] -- [codex args...]\n\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	args := commandFromArgs(flag.Args())
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create output dir: %v\n", err)
		return 1
	}

	rawOut, err := os.Create(filepath.Join(outDir, "output.raw"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create output.raw: %v\n", err)
		return 1
	}
	defer rawOut.Close()

	events, err := newEventLogger(filepath.Join(outDir, "events.jsonl"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create events.jsonl: %v\n", err)
		return 1
	}
	defer events.close()

	var inputOut *os.File
	if captureInput {
		inputOut, err = os.Create(filepath.Join(outDir, "input.raw"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "create input.raw: %v\n", err)
			return 1
		}
		defer inputOut.Close()
	}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open /dev/tty: %v\n", err)
		return 1
	}
	defer tty.Close()

	originalStty, sttyErr := sttyOutput(tty, "-g")
	if sttyErr == nil && originalStty != "" {
		if err := runStty(tty, "raw", "-echo"); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to switch terminal to raw mode: %v\n", err)
		} else {
			defer runStty(tty, originalStty)
		}
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = os.Environ()
	initialSize := currentWinsize(tty)
	ptmx, err := creackpty.StartWithSize(cmd, initialSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start %q: %v\n", strings.Join(args, " "), err)
		return 1
	}
	defer ptmx.Close()

	events.event("start", map[string]any{
		"argv":          args,
		"pid":           cmd.Process.Pid,
		"out_dir":       outDir,
		"capture_input": captureInput,
		"term":          os.Getenv("TERM"),
		"colorterm":     os.Getenv("COLORTERM"),
		"ghostty":       os.Getenv("GHOSTTY_RESOURCES_DIR"),
		"size":          terminalSize(tty),
	})

	resizeCh := make(chan os.Signal, 1)
	signal.Notify(resizeCh, syscall.SIGWINCH)
	defer signal.Stop(resizeCh)
	go func() {
		for range resizeCh {
			err := creackpty.InheritSize(tty, ptmx)
			events.event("resize", map[string]any{
				"size":  terminalSize(tty),
				"error": errString(err),
			})
		}
	}()

	outputDone := make(chan error, 1)
	go func() {
		var seq uint64
		var offset int64
		buf := make([]byte, 32*1024)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				seq++
				sum := sha256.Sum256(chunk)
				if _, err := rawOut.Write(chunk); err != nil {
					outputDone <- err
					return
				}
				if _, err := os.Stdout.Write(chunk); err != nil {
					outputDone <- err
					return
				}
				flags := controlFlags(chunk)
				events.event("output", map[string]any{
					"seq":     seq,
					"offset":  offset,
					"bytes":   n,
					"sha256":  hex.EncodeToString(sum[:]),
					"flags":   flags,
					"preview": previewBytes(chunk, 160),
				})
				offset += int64(n)
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) || errors.Is(readErr, os.ErrClosed) || strings.Contains(readErr.Error(), "input/output error") {
					outputDone <- nil
					return
				}
				outputDone <- readErr
				return
			}
		}
	}()

	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		var reader io.Reader = os.Stdin
		if inputOut != nil {
			reader = io.TeeReader(os.Stdin, inputOut)
		}
		_, _ = io.Copy(ptmx, reader)
	}()

	waitErr := cmd.Wait()
	_ = ptmx.Close()
	outputErr := <-outputDone
	_ = rawOut.Sync()
	if inputOut != nil {
		_ = inputOut.Sync()
	}

	exitCode := 0
	if waitErr != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	events.event("exit", map[string]any{
		"exit_code":    exitCode,
		"wait_error":   errString(waitErr),
		"output_error": errString(outputErr),
		"out_dir":      outDir,
	})

	if sttyErr == nil && originalStty != "" {
		_ = runStty(tty, originalStty)
	}
	fmt.Fprintf(os.Stderr, "\n[codex-pty-capture] wrote %s\n", outDir)

	if outputErr != nil {
		return 1
	}
	return exitCode
}
