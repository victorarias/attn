package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Operation string

const (
	OpMetadata Operation = "metadata"
	OpStatus   Operation = "status"
	OpDiff     Operation = "diff"
	OpWorktree Operation = "worktree"
	OpNetwork  Operation = "network"
	OpClone    Operation = "clone"
)

const slowGitLogThreshold = 2 * time.Second

var (
	logMu       sync.RWMutex
	logf        func(format string, args ...interface{})
	timeoutMu   sync.RWMutex
	timeoutByOp = map[Operation]time.Duration{}
)

// SetLogFunc wires git command duration logging into the daemon logger.
func SetLogFunc(fn func(format string, args ...interface{})) {
	logMu.Lock()
	defer logMu.Unlock()
	logf = fn
}

func defaultTimeout(op Operation) time.Duration {
	timeoutMu.RLock()
	if timeout, ok := timeoutByOp[op]; ok {
		timeoutMu.RUnlock()
		return timeout
	}
	timeoutMu.RUnlock()

	switch op {
	case OpStatus, OpMetadata:
		return 2 * time.Minute
	case OpDiff:
		return 10 * time.Minute
	case OpWorktree, OpNetwork:
		return 30 * time.Minute
	case OpClone:
		return 60 * time.Minute
	default:
		return 2 * time.Minute
	}
}

func setTimeoutForTesting(op Operation, timeout time.Duration) func() {
	timeoutMu.Lock()
	previous, hadPrevious := timeoutByOp[op]
	timeoutByOp[op] = timeout
	timeoutMu.Unlock()

	return func() {
		timeoutMu.Lock()
		defer timeoutMu.Unlock()
		if hadPrevious {
			timeoutByOp[op] = previous
			return
		}
		delete(timeoutByOp, op)
	}
}

func runGitOutput(op Operation, dir string, args ...string) ([]byte, error) {
	return runGitCommand(op, dir, nil, false, args...)
}

func Output(op Operation, dir string, args ...string) ([]byte, error) {
	return runGitOutput(op, dir, args...)
}

func runGitCombined(op Operation, dir string, args ...string) ([]byte, error) {
	return runGitCommand(op, dir, nil, true, args...)
}

func runGitWithStdin(op Operation, dir string, stdin io.Reader, args ...string) ([]byte, error) {
	return runGitCommand(op, dir, stdin, false, args...)
}

func OutputWithStdin(op Operation, dir string, stdin io.Reader, args ...string) ([]byte, error) {
	return runGitWithStdin(op, dir, stdin, args...)
}

func runGitNoOutput(op Operation, dir string, args ...string) error {
	_, err := runGitCommand(op, dir, nil, true, args...)
	return err
}

func runGitCommand(op Operation, dir string, stdin io.Reader, combined bool, args ...string) ([]byte, error) {
	timeout := defaultTimeout(op)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = stdin
	}

	started := time.Now()
	var out []byte
	var err error
	if combined {
		out, err = cmd.CombinedOutput()
	} else {
		out, err = cmd.Output()
	}
	duration := time.Since(started)

	logGitCommand(op, dir, args, duration, ctx.Err())

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("git %s timed out after %s: git %s", op, timeout, strings.Join(redactGitArgs(args), " "))
	}
	return out, err
}

func logGitCommand(op Operation, dir string, args []string, duration time.Duration, ctxErr error) {
	logMu.RLock()
	fn := logf
	logMu.RUnlock()
	if fn == nil {
		return
	}
	if duration < slowGitLogThreshold && ctxErr == nil {
		return
	}
	status := "slow"
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		status = "timeout"
	}
	fn("git command %s: op=%s duration=%s dir=%s args=%q", status, op, duration.Round(time.Millisecond), dir, redactGitArgs(args))
}

func redactGitArgs(args []string) []string {
	redacted := make([]string, len(args))
	for i, arg := range args {
		redacted[i] = redactGitArg(arg)
	}
	return redacted
}

func redactGitArg(arg string) string {
	parsed, err := url.Parse(arg)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return arg
	}
	switch parsed.Scheme {
	case "http", "https", "ssh":
	default:
		return arg
	}
	if parsed.User != nil {
		parsed.User = url.User("REDACTED")
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery = "REDACTED"
	}
	if parsed.Fragment != "" {
		parsed.Fragment = "REDACTED"
	}
	return parsed.String()
}
