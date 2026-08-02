package daemon

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The wire-trace goldens are the safety net for the event-bus migration.
//
// Moving a broadcast onto the bus is only correct if clients cannot tell. That
// is a claim about bytes, so these tests record every payload the hub sends —
// through all five send paths, not just the typed Broadcast the older
// BroadcastRecorder hooks — and pin them.
//
// Two goldens, because a migration can break in two different ways:
//
//   - the producer golden drives each broadcaster directly, so a change to the
//     payload a fact projects shows up as a diff;
//   - the flow golden drives handlers the way a client does, so a call site that
//     stops emitting anything shows up as a missing line. A producer that is
//     migrated but never published from would still pass the producer golden.
//
// Regenerate with `go test ./internal/daemon -run TestWireTrace -update`, and
// read the diff — a moved line is a change in what the app receives.

var updateWireGoldens = flag.Bool("update", false, "update wire-trace golden files")

var (
	wireTimestampPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$`)
	wireUUIDPattern      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// wireRecorder attaches a trace to a daemon built by NewForTesting, which does
// not go through TestHarnessBuilder.
func wireRecorder(d *Daemon) *WireTrace {
	trace := &WireTrace{}
	d.wsHub.wireTap = trace.record
	return trace
}

// normalizeWirePayload renders one payload as stable, diffable JSON. Values that
// change between runs — timestamps, generated ids, temp directories, durations —
// become fixed placeholders so the golden captures shape and content rather than
// the clock.
func normalizeWirePayload(payload []byte, paths map[string]string) string {
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return fmt.Sprintf("<unparseable: %s>", string(payload))
	}
	normalized := normalizeWireValue("", decoded, paths)
	// HTML escaping off: the placeholders are angle-bracketed, and <tmp>
	// in a golden is unreadable in a diff.
	var buf strings.Builder
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(normalized); err != nil {
		return fmt.Sprintf("<unrenderable: %v>", err)
	}
	return strings.TrimRight(buf.String(), "\n")
}

func normalizeWireValue(key string, v any, paths map[string]string) any {
	if isEnvironmentProbedKey(key) {
		return "<probed>"
	}
	switch typed := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, child := range typed {
			out[k] = normalizeWireValue(k, child, paths)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = normalizeWireValue(key, child, paths)
		}
		return out
	case string:
		return normalizeWireString(typed, paths)
	case float64:
		// Durations are the only numbers that move between runs, and they are
		// always named for it.
		if strings.HasSuffix(key, "_ms") || strings.HasSuffix(key, "_seconds") {
			return "<duration>"
		}
		return typed
	default:
		return v
	}
}

// isEnvironmentProbedKey reports whether a value describes the host rather than
// the daemon. Agent availability is a PATH lookup, so it reads "true" on a
// machine with the CLI installed and "false" on a CI runner. Pinning it would
// make the golden a statement about the host, and the migration these goldens
// guard cannot change it either way.
//
// Matched by suffix on purpose: a new agent driver adds its own key, and the
// golden should not start depending on the runner the day that lands.
func isEnvironmentProbedKey(key string) bool {
	return strings.HasSuffix(key, "_available")
}

func normalizeWireString(s string, paths map[string]string) string {
	if wireTimestampPattern.MatchString(s) {
		return "<timestamp>"
	}
	if wireUUIDPattern.MatchString(s) {
		return "<uuid>"
	}
	// Longest first, so a nested temp dir is not half-replaced by its parent.
	for _, path := range sortedPathsLongestFirst(paths) {
		s = strings.ReplaceAll(s, path, paths[path])
	}
	if wireHomeDir != "" {
		s = strings.ReplaceAll(s, wireHomeDir, "<home>")
	}
	return s
}

// wireHomeDir is the host's home directory. Paths the daemon derives from it —
// the notebook root, say — would otherwise pin the golden to whoever ran it:
// /Users/victor on a laptop, /home/runner in CI. Tests must not redirect HOME,
// so the golden normalizes the value instead.
var wireHomeDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return strings.TrimRight(home, string(filepath.Separator))
}()

func sortedPathsLongestFirst(paths map[string]string) []string {
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	return keys
}

// renderWireTrace turns a trace into the golden's text form: one numbered,
// event-labelled block per payload.
func renderWireTrace(trace *WireTrace, paths map[string]string) string {
	payloads := trace.Payloads()
	names := trace.EventNames()
	var b strings.Builder
	for i, payload := range payloads {
		fmt.Fprintf(&b, "--- %02d %s\n", i+1, names[i])
		b.WriteString(normalizeWirePayload(payload, paths))
		b.WriteString("\n")
	}
	if len(payloads) == 0 {
		b.WriteString("(no wire traffic)\n")
	}
	return b.String()
}

func assertWireGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "wire", name+".golden")
	if *updateWireGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (regenerate with -update): %v", path, err)
	}
	if got == string(want) {
		return
	}
	t.Errorf("wire trace differs from %s — clients would receive different bytes.\n%s",
		path, firstWireDiff(string(want), got))
}

// firstWireDiff reports the first differing line with a little context, which is
// far more usable than dumping two multi-kilobyte traces.
func firstWireDiff(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	limit := len(wantLines)
	if len(gotLines) > limit {
		limit = len(gotLines)
	}
	at := func(lines []string, i int) string {
		if i < len(lines) {
			return lines[i]
		}
		return "<end of trace>"
	}
	for i := 0; i < limit; i++ {
		if at(wantLines, i) == at(gotLines, i) {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "first difference at line %d:\n", i+1)
		for j := max(0, i-3); j < min(limit, i+4); j++ {
			marker := "  "
			if j == i {
				marker = "> "
			}
			fmt.Fprintf(&b, "%sgolden: %s\n%s  live: %s\n", marker, at(wantLines, j), marker, at(gotLines, j))
		}
		return b.String()
	}
	return "traces differ only in trailing whitespace"
}
