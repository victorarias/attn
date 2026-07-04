package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"
)

// daemonLogTimestampPattern matches the leading "[2006-01-02 15:04:05]"
// timestamp internal/logging writes at the start of every log line (see
// internal/logging/logging.go's log() and truncationMarker()). Lines that
// don't start with a bracketed timestamp (e.g. a multi-line value logged
// mid-message) are treated as continuations of the most recent timestamped
// line — see filterSinceLines.
var daemonLogTimestampPattern = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\]`)

const daemonLogTimestampLayout = "2006-01-02 15:04:05"

// readLinesFile reads every line of path without bufio.Scanner's default token
// size limit, which is too small for some diagnostic lines (e.g. an incident
// record's embedded ring-buffer context can exceed 64KiB). Modeled on
// internal/transcript/parser.go's readJSONLLines, reimplemented locally here
// since that helper is unexported.
func readLinesFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no such file: %s", path)
		}
		return nil, err
	}
	defer f.Close()
	return readLines(f)
}

func readLines(r io.Reader) ([]string, error) {
	br := bufio.NewReader(r)
	var lines []string
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimRight(line, "\n")
			line = bytes.TrimRight(line, "\r")
			lines = append(lines, string(line))
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return lines, nil
			}
			return lines, err
		}
	}
}

// tailLines returns at most the last n entries of lines, preserving order.
// n <= 0 returns lines unchanged (no tail limit applied).
func tailLines(lines []string, n int) []string {
	if n <= 0 || len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// grepLines filters lines to those matching the Go regexp pattern. An empty
// pattern is a no-op (returns lines unchanged).
func grepLines(lines []string, pattern string) ([]string, error) {
	if pattern == "" {
		return lines, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid --grep pattern: %w", err)
	}
	var out []string
	for _, line := range lines {
		if re.MatchString(line) {
			out = append(out, line)
		}
	}
	return out, nil
}

// filterSinceLines keeps lines whose leading daemon.log timestamp is at or
// after cutoff. A line with no parseable leading timestamp is a continuation
// of the most recently seen timestamped line (e.g. a wrapped/multi-line log
// value), so it is included only when that most recent timestamped line
// matched — never on its own.
func filterSinceLines(lines []string, cutoff time.Time) []string {
	var out []string
	matching := false
	for _, line := range lines {
		if m := daemonLogTimestampPattern.FindStringSubmatch(line); m != nil {
			ts, err := time.ParseInLocation(daemonLogTimestampLayout, m[1], time.Local)
			if err != nil {
				// Looked timestamped but didn't parse; treat like an
				// untimestamped continuation line rather than dropping it
				// silently.
				if matching {
					out = append(out, line)
				}
				continue
			}
			matching = !ts.Before(cutoff)
			if matching {
				out = append(out, line)
			}
			continue
		}
		if matching {
			out = append(out, line)
		}
	}
	return out
}
